package gitlab

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

type forkGitLabOriginTransport struct{ endpoint *url.URL }

func (rt forkGitLabOriginTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	copy := req.Clone(req.Context())
	u := *req.URL
	u.Scheme, u.Host = rt.endpoint.Scheme, rt.endpoint.Host
	copy.URL, copy.Host = &u, rt.endpoint.Host
	return http.DefaultTransport.RoundTrip(copy)
}

// Keep web/API identity coherent while every network call stays on this
// test's loopback server. Upstream fixtures use gitlab.com web URLs.
func forkGitLabOriginClient(t *testing.T, endpoint, webURL, projectID string) *Client {
	t.Helper()
	base := endpoint
	if webURL == "" || strings.HasPrefix(webURL, "https://gitlab.com/") {
		base = "https://gitlab.com"
	}
	u, err := url.Parse(endpoint)
	if err != nil || (u.Hostname() != "127.0.0.1" && u.Hostname() != "localhost") {
		t.Fatalf("test endpoint must be loopback: %q", endpoint)
	}
	c := NewClient("token", base, projectID)
	c.HTTPClient.Transport = forkGitLabOriginTransport{endpoint: u}
	return c
}

func TestForkEpicMilestonePassRefusesForeignProject(t *testing.T) {
	var base string
	requests, writes := 0, 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		requests++
		if r.Method == http.MethodPut {
			writes++
		}
		if strings.Contains(r.URL.Path, "milestones") {
			_ = json.NewEncoder(w).Encode([]Milestone{{ID: 500, IID: 5}})
		} else {
			_ = json.NewEncoder(w).Encode(Issue{ID: 100, IID: 10})
		}
	}))
	defer srv.Close()
	base = srv.URL
	epic := gitLabIssue("epic", base+"/group/project/-/milestones/5", types.TypeEpic)
	child := gitLabIssue("foreign", base+"/other/project/-/issues/10", types.TypeTask)
	tr := &Tracker{client: NewClient("token", base, "123"), projectPath: "group/project", store: &gitLabLinkFakeStore{deps: map[string][]*types.IssueWithDependencyMetadata{"foreign": {{Issue: *epic, DependencyType: types.DepParentChild}}}}}
	count, errs := tr.PushEpicMilestones(context.Background(), []*types.Issue{child}, EpicMilestoneOptions{})
	if count != 0 || len(errs) != 0 || requests != 0 || writes != 0 {
		t.Fatalf("foreign milestone pass reached default project: count=%d errors=%v requests=%d writes=%d", count, errs, requests, writes)
	}
}

func TestForkExistingForeignLinkDoesNotSatisfyLocalLink(t *testing.T) {
	posts := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodPost {
			posts++
			_, _ = w.Write([]byte(`{}`))
			return
		}
		_, _ = w.Write([]byte(`[{"iid":20,"project_id":999,"link_type":"relates_to"}]`))
	}))
	defer srv.Close()
	r := NewLinkResolver(NewClient("token", srv.URL, "123"))
	res := r.PushLinks(context.Background(), []DependencyLink{{SourceIID: 10, TargetIID: 20, LinkType: gitLabLinkRelatesTo}}, PushLinkOptions{})
	if len(res.Errors) != 0 || res.Created != 1 || posts != 1 {
		t.Fatalf("foreign link suppressed configured-project link: %+v posts=%d", res, posts)
	}
}

func TestForkLinkIdentityUsesProjectIDWithoutURL(t *testing.T) {
	deps := issueLinksToDependencies(&Issue{IID: 7, ProjectID: 100}, []IssueLink{{IID: 7, ProjectID: 200, LinkType: gitLabLinkBlocks}}, DefaultMappingConfig())
	if len(deps) != 1 || deps[0].FromProjectID != 200 || deps[0].ToProjectID != 100 {
		t.Fatalf("same IID in distinct project IDs collapsed: %+v", deps)
	}
}

func TestForkGitLabMatchingPreservesConfiguredOrigin(t *testing.T) {
	tr := &Tracker{client: NewClient("token", "https://gitlab.example.com/install", "123"), projectPath: "group/project"}
	for _, ref := range []string{
		"https://other.gitlab.example.com/install/group/project/-/issues/7",
		"http://gitlab.example.com/install/group/project/-/issues/7",
		"https://user@gitlab.example.com/install/group/project/-/issues/7",
		"https://gitlab.example.com/other/group/project/-/issues/7",
	} {
		if got := tr.MatchKey(ref); got != "" {
			t.Errorf("out-of-scope ref got matching key: %q => %q", ref, got)
		}
		if got := tr.LocalIdentityKey(&types.Issue{ExternalRef: &ref, SourceSystem: "gitlab:123:7"}); got != "" {
			t.Errorf("out-of-scope identity: %q => %q", ref, got)
		}
		if tr.ProjectScope().Contains(context.Background(), &types.Issue{ExternalRef: &ref}) {
			t.Errorf("out-of-scope project admitted: %q", ref)
		}
	}
	ref := "https://gitlab.example.com/install/renamed/project/-/issues/7"
	if got := tr.LocalIdentityKey(&types.Issue{ExternalRef: &ref, SourceSystem: "gitlab:123:7"}); got != "gitlab:123:7" {
		t.Fatalf("same-instance rename lost identity: %q", got)
	}
}
