package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/tracker"
)

type projectScopeStore struct{ tracker.Store }

func (projectScopeStore) GetConfig(context.Context, string) (string, error) { return "", nil }

func TestExtractIdentifierScopedToProject(t *testing.T) {
	tr := &Tracker{client: NewClient("token", "https://forge.example.test/gitlab", "acme/team/repo-b"), projectPath: "acme/team/repo-b"}
	for _, tt := range []struct{ ref, want string }{
		{"https://forge.example.test/gitlab/acme/team/repo-b/-/issues/7", "7"},
		{"https://forge.example.test/gitlab/acme/team/repo-b/-/work_items/7", "7"},
		{"https://forge.example.test/gitlab/acme/team/repo-b/-/milestones/7", "milestone:7"},
		{"gitlab:7", "7"},
		{"https://forge.example.test/gitlab/acme/team/repo-a/-/issues/7", ""},
		{"https://other.test/gitlab/acme/team/repo-b/-/issues/7", ""},
		{"https://forge.example.test/other/acme/team/repo-b/-/issues/7", ""},
		{"https://forge.example.test/gitlab/acme/team/repo-b/-/issues/7/extra", ""},
	} {
		if got := tr.ExtractIdentifier(tt.ref); got != tt.want {
			t.Errorf("%q = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestNumericProjectScopeResolution(t *testing.T) {
	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)
	var paths []string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.Path)
		if r.URL.Path != "/api/v4/projects/202" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"id":202,"path_with_namespace":"acme/repo-b"}`))
	}))
	defer srv.Close()
	t.Setenv("GITLAB_TOKEN", "dummy")
	t.Setenv("GITLAB_URL", srv.URL)
	t.Setenv("GITLAB_PROJECT_ID", "202")
	t.Setenv("GITLAB_PROJECT_PATH", "")
	tr := &Tracker{}
	if err := tr.Init(context.Background(), projectScopeStore{}); err != nil {
		t.Fatal(err)
	}
	if got := tr.ExtractIdentifier(srv.URL + "/acme/repo-b/-/issues/7"); got != "7" {
		t.Fatalf("current project: %q", got)
	}
	if got := tr.ExtractIdentifier(srv.URL + "/acme/repo-a/-/issues/7"); got != "" {
		t.Fatalf("foreign project: %q", got)
	}
	if len(paths) != 1 {
		t.Fatalf("project metadata lookups: %v", paths)
	}
}

func TestProjectScopeRefusesStaleOrUnverifiedMetadata(t *testing.T) {
	for _, tt := range []struct {
		name, configuredPath, body string
		status                     int
	}{
		{"stale configured path", "acme/repo-a", `{"id":202,"path_with_namespace":"acme/repo-b"}`, http.StatusOK},
		{"different project ID", "", `{"id":101,"path_with_namespace":"acme/repo-a"}`, http.StatusOK},
		{"missing path", "", `{"id":202}`, http.StatusOK},
		{"lookup refused", "", `{}`, http.StatusForbidden},
	} {
		t.Run(tt.name, func(t *testing.T) {
			config.ResetForTesting()
			t.Cleanup(config.ResetForTesting)
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				w.WriteHeader(tt.status)
				_, _ = w.Write([]byte(tt.body))
			}))
			defer srv.Close()
			t.Setenv("GITLAB_TOKEN", "dummy")
			t.Setenv("GITLAB_URL", srv.URL)
			t.Setenv("GITLAB_PROJECT_ID", "202")
			t.Setenv("GITLAB_PROJECT_PATH", tt.configuredPath)
			if err := (&Tracker{}).Init(context.Background(), projectScopeStore{}); err == nil {
				t.Fatal("accepted stale or unverified project scope")
			}
		})
	}
}
