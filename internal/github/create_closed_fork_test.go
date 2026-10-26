package github

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/types"
)

type closedPushFixtureStore struct {
	tracker.Store
	issue    *types.Issue
	metadata map[string]string
}

func (s *closedPushFixtureStore) SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error) {
	return []*types.Issue{s.issue}, nil
}
func (s *closedPushFixtureStore) UpdateIssue(_ context.Context, _ string, updates map[string]interface{}, _ string) error {
	if ref, ok := updates["external_ref"].(string); ok {
		s.issue.ExternalRef = &ref
	}
	return nil
}
func (s *closedPushFixtureStore) GetLocalMetadata(_ context.Context, key string) (string, error) {
	return s.metadata[key], nil
}
func (s *closedPushFixtureStore) SetLocalMetadata(_ context.Context, key, value string) error {
	s.metadata[key] = value
	return nil
}

func TestForkCreateClosedGitHubIssueAndRetry(t *testing.T) {
	for _, tc := range []struct {
		name      string
		status    types.Status
		failClose bool
		legacy    bool
	}{
		{"closed", types.StatusClosed, false, false}, {"open", types.StatusOpen, false, false}, {"failed close retries same issue", types.StatusClosed, true, false},
		{"legacy cached closed issue", types.StatusClosed, false, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var mu sync.Mutex
			var remote Issue
			posts, patches, gets := 0, 0, 0
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				mu.Lock()
				defer mu.Unlock()
				w.Header().Set("Content-Type", "application/json")
				switch r.Method + " " + r.URL.Path {
				case "POST /repos/owner/repo/issues":
					posts++
					var body struct {
						Title  string   `json:"title"`
						Body   string   `json:"body"`
						Labels []string `json:"labels"`
					}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "bad body", 400)
						return
					}
					remote = Issue{ID: 100, Number: 42, Title: body.Title, Body: body.Body, State: "open", HTMLURL: "http://" + r.Host + "/owner/repo/issues/42"}
					for _, label := range body.Labels {
						remote.Labels = append(remote.Labels, Label{Name: label})
					}
				case "PATCH /repos/owner/repo/issues/42":
					patches++
					var body map[string]interface{}
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						http.Error(w, "bad body", 400)
						return
					}
					if body["state"] != "closed" {
						t.Errorf("PATCH state: %v", body)
					}
					if patches == 1 && !tc.legacy && len(body) != 1 {
						t.Errorf("create follow-up must only change state: %v", body)
					}
					if tc.failClose && patches == 1 {
						w.WriteHeader(422)
						_, _ = fmt.Fprint(w, `{"message":"fixture rejected close"}`)
						return
					}
					remote.State = "closed"
				case "GET /repos/owner/repo/issues/42":
					gets++
				default:
					t.Errorf("unexpected HTTP request: %s %s", r.Method, r.URL.Path)
					http.Error(w, "unexpected", 404)
					return
				}
				if err := json.NewEncoder(w).Encode(remote); err != nil {
					t.Error(err)
				}
			}))
			defer srv.Close()
			baseURL := srv.URL
			tr := &Tracker{client: NewClient("fixture-token", "owner", "repo").WithBaseURL(baseURL), config: DefaultMappingConfig()}
			issue := &types.Issue{ID: "bd-fixture", Title: "fixture", Description: "body", Status: tc.status, IssueType: types.TypeTask, Priority: 2}
			if tc.status == types.StatusOpen && PushContentHash(issue, tr.config) != "66069b26a5744ad20503c2cb58873efadf3a85e5203754f6a22943d3e5584e4f" {
				t.Fatal("open-issue fingerprint changed from the previous cache format")
			}
			store := &closedPushFixtureStore{issue: issue, metadata: map[string]string{}}
			engine := tracker.NewEngine(tr, store, "fixture")
			engine.PushHooks = &tracker.PushHooks{
				ContentEqual: func(local *types.Issue, external *tracker.TrackerIssue) bool {
					gh, ok := external.Raw.(*Issue)
					return ok && PushFieldsEqual(local, gh, tr.config)
				},
				ContentHash: func(local *types.Issue) string { return PushContentHash(local, tr.config) },
				TargetScope: tr.PushTargetScope,
			}
			const legacyHash = "9bc23614c173cdf6224bfaf63505c534babad1afeb5c25af911ee83e9a97c8a0"
			if tc.legacy {
				ref := baseURL + "/owner/repo/issues/42"
				issue.ExternalRef = &ref
				scope := tr.PushTargetScope()
				store.metadata["github.pushhash.bd-fixture"] = fmt.Sprintf("v3:%d:%s%d:%s%s", len(legacyHash), legacyHash, len(scope), scope, ref)
				mu.Lock()
				remote = Issue{ID: 100, Number: 42, Title: issue.Title, Body: issue.Description, State: "open", HTMLURL: ref, Labels: []Label{{Name: "type::task"}, {Name: "priority::medium"}}}
				mu.Unlock()
			}
			first, err := engine.Sync(context.Background(), tracker.SyncOptions{Push: true})
			if err != nil {
				t.Fatal(err)
			}
			if tc.legacy {
				mu.Lock()
				p, c, g, state := posts, patches, gets, remote.State
				mu.Unlock()
				if first.Stats.Updated != 1 || p != 0 || c != 1 || g != 1 || state != "closed" {
					t.Fatalf("old create-only baseline suppressed reconciliation: %+v POST/PATCH/GET=%d/%d/%d state=%s", first, p, c, g, state)
				}
				second, err := engine.Sync(context.Background(), tracker.SyncOptions{Push: true})
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				defer mu.Unlock()
				if second.Stats.Skipped != 1 || posts != p || patches != c || gets != g {
					t.Fatalf("reconciled legacy issue was not cached: %+v", second)
				}
				return
			}
			if first.Stats.Created != 1 || issue.ExternalRef == nil || *issue.ExternalRef != baseURL+"/owner/repo/issues/42" {
				t.Fatalf("created issue link lost: %+v / %+v", first, issue)
			}
			mu.Lock()
			firstPosts, firstPatches, firstState := posts, patches, remote.State
			mu.Unlock()
			wantPatches := 0
			if tc.status == types.StatusClosed {
				wantPatches = 1
			}
			if firstPosts != 1 || firstPatches != wantPatches {
				t.Errorf("initial HTTP POST/PATCH = %d/%d, want 1/%d", firstPosts, firstPatches, wantPatches)
			}
			hash := store.metadata["github.pushhash.bd-fixture"]
			if tc.failClose {
				if len(first.Warnings) != 1 || !strings.Contains(strings.Join(first.Warnings, " "), "failed to close") {
					t.Errorf("partial close not surfaced: %+v", first.Warnings)
				}
				if firstState != "open" || hash != "" {
					t.Errorf("partial create was cached as converged: state=%s hash-present=%v", firstState, hash != "")
				}
				if firstPatches != 1 {
					return
				}
				second, err := engine.Sync(context.Background(), tracker.SyncOptions{Push: true})
				if err != nil {
					t.Fatal(err)
				}
				mu.Lock()
				retryPosts, retryPatches, retryGets, retryState := posts, patches, gets, remote.State
				mu.Unlock()
				if second.Stats.Updated != 1 || retryPosts != 1 || retryPatches != 2 || retryGets != 1 || retryState != "closed" {
					t.Fatalf("retry did not close the original issue: %+v POST/PATCH/GET=%d/%d/%d state=%s", second, retryPosts, retryPatches, retryGets, retryState)
				}
			} else if hash == "" || len(first.Warnings) != 0 {
				t.Fatalf("converged create did not retain its no-op baseline: %+v", first)
			}
			mu.Lock()
			beforePosts, beforePatches, beforeGets := posts, patches, gets
			mu.Unlock()
			third, err := engine.Sync(context.Background(), tracker.SyncOptions{Push: true})
			if err != nil {
				t.Fatal(err)
			}
			mu.Lock()
			defer mu.Unlock()
			if third.Stats.Skipped != 1 || posts != beforePosts || patches != beforePatches || gets != beforeGets {
				t.Fatalf("converged unchanged push was not a no-op: %+v POST/PATCH/GET=%d/%d/%d", third, posts, patches, gets)
			}
		})
	}
}
