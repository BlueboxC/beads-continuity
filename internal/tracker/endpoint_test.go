package tracker_test

import (
	"context"
	"errors"
	"testing"

	"github.com/steveyegge/beads/internal/ado"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/github"
	"github.com/steveyegge/beads/internal/gitlab"
	"github.com/steveyegge/beads/internal/jira"
	"github.com/steveyegge/beads/internal/linear"
	"github.com/steveyegge/beads/internal/tracker"
)

type endpointConfig struct {
	tracker.Store
	values map[string]string
	err    error
	reads  int
}

func (s *endpointConfig) GetConfig(_ context.Context, key string) (string, error) {
	s.reads++
	return s.values[key], s.err
}

func TestEndpointRequiresLocalSelection(t *testing.T) {
	for _, key := range []string{"github.url", "gitlab.url", "jira.url", "ado.url", "linear.api_endpoint"} {
		t.Run(key, func(t *testing.T) {
			config.ResetForTesting()
			t.Cleanup(config.ResetForTesting)
			st := &endpointConfig{values: map[string]string{key: "https://untrusted.invalid"}}
			if _, err := tracker.ReadEndpoint(context.Background(), st, key, ""); err == nil {
				t.Fatal("accepted synced destination")
			}
			st.values = nil
			if value, err := tracker.ReadEndpoint(context.Background(), st, key, ""); err != nil || value != "" {
				t.Fatalf("default: %q %v", value, err)
			}
			st.err = errors.New("unavailable")
			if _, err := tracker.ReadEndpoint(context.Background(), st, key, ""); !errors.Is(err, st.err) {
				t.Fatalf("read error lost: %v", err)
			}
			st.reads = 0
			t.Setenv("TEST_TRACKER_ENDPOINT", "https://local.example/api/base")
			if value, err := tracker.ReadEndpoint(context.Background(), st, key, "TEST_TRACKER_ENDPOINT"); err != nil || value != "https://local.example/api/base" || st.reads != 0 {
				t.Fatalf("local env: %q %v reads=%d", value, err, st.reads)
			}
			if err := config.Initialize(); err != nil {
				t.Fatal(err)
			}
			config.Set(key, "https://yaml.example/base")
			if value, err := tracker.ReadEndpoint(context.Background(), st, key, "TEST_TRACKER_ENDPOINT"); err != nil || value != "https://yaml.example/base" || st.reads != 0 {
				t.Fatalf("local configuration: %q %v reads=%d", value, err, st.reads)
			}
		})
	}
}

func TestAllTrackerInitializersRejectSyncedEndpoints(t *testing.T) {
	t.Setenv("GITHUB_TOKEN", "SYNTHETIC-ONLY")
	t.Setenv("GITLAB_TOKEN", "SYNTHETIC-ONLY")
	t.Setenv("JIRA_API_TOKEN", "SYNTHETIC-ONLY")
	t.Setenv("AZURE_DEVOPS_PAT", "SYNTHETIC-ONLY")
	t.Setenv("LINEAR_API_KEY", "SYNTHETIC-ONLY")
	for _, tc := range []struct {
		key, env   string
		newTracker func() tracker.IssueTracker
	}{
		{"github.url", "GITHUB_API_URL", func() tracker.IssueTracker { return &github.Tracker{} }},
		{"gitlab.url", "GITLAB_URL", func() tracker.IssueTracker { return &gitlab.Tracker{} }},
		{"jira.url", "JIRA_URL", func() tracker.IssueTracker { return &jira.Tracker{} }},
		{"ado.url", "AZURE_DEVOPS_URL", func() tracker.IssueTracker { return &ado.Tracker{} }},
		{"linear.api_endpoint", "LINEAR_API_ENDPOINT", func() tracker.IssueTracker { return &linear.Tracker{} }},
	} {
		t.Run(tc.key, func(t *testing.T) {
			config.ResetForTesting()
			t.Cleanup(config.ResetForTesting)
			t.Setenv(tc.env, "")
			st := &endpointConfig{values: map[string]string{tc.key: "https://untrusted.invalid", "github.owner": "owner", "github.repo": "repo", "gitlab.project_id": "1", "jira.project": "PROJ", "ado.project": "project", "ado.org": "org", "linear.team_id": "team"}}
			if err := tc.newTracker().Init(context.Background(), st); err == nil {
				t.Fatal("accepted synced endpoint")
			}
		})
	}
}
