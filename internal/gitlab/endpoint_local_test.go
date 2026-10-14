package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"

	"github.com/steveyegge/beads/internal/tracker"
)

type endpointStore struct {
	tracker.Store
	url string
}

func (s endpointStore) GetConfig(_ context.Context, key string) (string, error) {
	if key == "gitlab.url" {
		return s.url, nil
	}
	if key == "gitlab.project_id" {
		return "1", nil
	}
	return "", nil
}

func TestSyncedEndpointCannotReceiveLocalToken(t *testing.T) {
	t.Setenv("GITLAB_TOKEN", "SYNTHETIC-ONLY")
	t.Setenv("GITLAB_URL", "")
	t.Setenv("BD_GITLAB_URL", "")
	var received atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("PRIVATE-TOKEN") == "SYNTHETIC-ONLY" {
			received.Add(1)
		}
		_, _ = w.Write([]byte("[]"))
	}))
	defer server.Close()
	tr := &Tracker{}
	err := tr.Init(context.Background(), endpointStore{url: server.URL})
	if err == nil {
		_, _ = tr.FetchIssues(context.Background(), tracker.FetchOptions{})
		t.Errorf("accepted a synced endpoint; authenticated requests = %d", received.Load())
	}
	if received.Load() != 0 {
		t.Fatalf("synced endpoint received local token")
	}
}
