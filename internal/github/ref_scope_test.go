package github

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
	"github.com/steveyegge/beads/internal/tracker/conformance"
	"github.com/steveyegge/beads/internal/types"
)

// Contributor scenarios from NahumLitvin's PR #7794, plus endpoint controls.
func TestExtractIdentifierScopedToRepository(t *testing.T) {
	tr := &Tracker{client: NewClient("token", "acme", "repo-b")}
	for _, tt := range []struct{ ref, want string }{
		{"https://github.com/acme/repo-b/issues/42", "42"},
		{"https://github.com/ACME/Repo-B/issues/42", "42"},
		{"github:42", "42"},
		{"https://github.com/acme/repo-a/issues/42", ""},
		{"https://github.com/other/repo-b/issues/42", ""},
		{"https://github.com.evil.test/acme/repo-b/issues/42", ""},
		{"https://gitlab.com/acme/repo-b/issues/42", ""},
		{"https://github.com/acme/repo-b/issues/42/extra", ""},
		{"https://api.github.com/repos/acme/repo-b/issues/42", "42"},
	} {
		if got := tr.ExtractIdentifier(tt.ref); got != tt.want {
			t.Errorf("ExtractIdentifier(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestExtractIdentifierEnterpriseEndpoint(t *testing.T) {
	tr := &Tracker{client: NewClient("token", "acme", "repo").WithBaseURL("https://forge.example.test/git/api/v3")}
	for _, ref := range []string{"https://forge.example.test/git/acme/repo/issues/7", "https://forge.example.test/git/api/v3/repos/acme/repo/issues/7"} {
		if got := tr.ExtractIdentifier(ref); got != "7" {
			t.Errorf("%s: %q", ref, got)
		}
	}
	for _, ref := range []string{"https://other.test/git/acme/repo/issues/7", "https://forge.example.test/other/acme/repo/issues/7"} {
		if got := tr.ExtractIdentifier(ref); got != "" {
			t.Errorf("foreign %s: %q", ref, got)
		}
	}
}

// Adapted from gascity-eddie's upstream PR #8068. Recognition of the GitHub
// family keeps another repository's existing link out of the create path.
func TestForeignGitHubReferenceSkippedInPreviewAndPush(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Errorf("unexpected request for foreign reference: %s %s", r.Method, r.URL.Path)
		http.NotFound(w, r)
	}))
	t.Cleanup(server.Close)
	gt := &Tracker{client: NewClient("token", "acme", "repo-b").WithBaseURL(server.URL), config: DefaultMappingConfig()}
	ref := server.URL + "/acme/repo-a/issues/42"
	store := conformance.NewStore()
	store.Issues["bd-1"] = &types.Issue{
		ID: "bd-1", Title: "Foreign link", Status: types.StatusOpen,
		IssueType: types.TypeTask, Priority: 2, ExternalRef: &ref,
	}
	e := tracker.NewEngine(gt, store, "fixture")
	var messages []string
	e.OnMessage = func(message string) { messages = append(messages, message) }
	for _, preview := range []bool{true, false} {
		got, err := e.Sync(context.Background(), tracker.SyncOptions{Push: true, DryRun: preview})
		if err != nil {
			t.Fatalf("Sync(dry_run=%t): %v", preview, err)
		}
		if got.Stats.Skipped != 1 || got.Stats.Updated != 0 || got.Stats.Created != 0 {
			t.Errorf("Sync(dry_run=%t): %+v, want one skip and no remote operation", preview, got.Stats)
		}
	}
	if joined := strings.Join(messages, "\n"); strings.Contains(joined, "Would update") || strings.Contains(joined, "Would create") {
		t.Errorf("foreign reference appeared as an outgoing mutation: %q", joined)
	}
}
