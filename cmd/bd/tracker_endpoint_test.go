package main

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

type endpointCLIStore struct {
	storage.DoltStorage
	endpoint string
	failure  error
	reads    int
}

func (s *endpointCLIStore) GetConfig(_ context.Context, key string) (string, error) {
	if strings.HasSuffix(key, ".url") || key == "linear.api_endpoint" {
		s.reads++
		return s.endpoint, s.failure
	}
	return "", nil
}

func TestDirectTrackerConstructorsRequireLocalEndpoint(t *testing.T) {
	oldStore, oldPath := store, dbPath
	t.Cleanup(func() { store, dbPath = oldStore, oldPath })
	dbPath = ""
	for _, tc := range []struct {
		env string
		get func() error
	}{
		{"GITHUB_API_URL", func() error { _, err := getGitHubConfig(); return err }},
		{"GITLAB_URL", func() error { _, err := getGitLabConfig(); return err }},
		{"AZURE_DEVOPS_URL", func() error { _, err := getADOConfig(); return err }},
	} {
		t.Run(tc.env, func(t *testing.T) {
			t.Setenv(tc.env, "")
			st := &endpointCLIStore{endpoint: "https://untrusted.invalid"}
			store = st
			if err := tc.get(); err == nil {
				t.Fatal("accepted synced URL")
			}
			st.endpoint = ""
			st.failure = errors.New("offline")
			if err := tc.get(); !errors.Is(err, st.failure) {
				t.Fatalf("lost store error: %v", err)
			}
			st.reads = 0
			t.Setenv(tc.env, "https://operator.example/subpath")
			if err := tc.get(); err != nil || st.reads != 0 {
				t.Fatalf("local override: %v, endpoint reads=%d", err, st.reads)
			}
		})
	}
}

func TestLinearAPIOnlyRejectsSyncedEndpointInBothAuthModes(t *testing.T) {
	t.Setenv("LINEAR_API_ENDPOINT", "")
	for _, oauth := range []bool{false, true} {
		t.Run(map[bool]string{false: "api-key", true: "oauth"}[oauth], func(t *testing.T) {
			t.Setenv("LINEAR_API_KEY", "SYNTHETIC-ONLY")
			t.Setenv("LINEAR_OAUTH_CLIENT_ID", "")
			t.Setenv("LINEAR_OAUTH_CLIENT_SECRET", "")
			if oauth {
				t.Setenv("LINEAR_OAUTH_CLIENT_ID", "client")
				t.Setenv("LINEAR_OAUTH_CLIENT_SECRET", "SYNTHETIC-ONLY")
			}
			st := &configOnlyTrackerStore{config: map[string]string{"linear.api_endpoint": "https://untrusted.invalid/graphql"}}
			if client, err := buildLinearClientAPIOnly(context.Background(), "", st); err == nil || client != nil {
				t.Fatalf("accepted synced endpoint: %v %v", client, err)
			}
		})
	}
}
