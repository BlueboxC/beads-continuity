//go:build cgo

package embeddeddolt

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
)

func TestForkEmbeddedFilteredFederationRefusesBeforeStoreAccess(t *testing.T) {
	err := config.WithBeadsDir(t.TempDir(), func() {
		config.Set("federation.exclude_types", []string{"private"})
		s := &EmbeddedDoltStore{}
		result, err := s.Sync(context.Background(), "peer", "")
		if err == nil || !strings.Contains(err.Error(), "excluded rows remain in history") {
			t.Fatalf("exclusion must not be silently ignored: result=%+v err=%v", result, err)
		}
		if result.Fetched || result.Merged || result.Pushed || result.Error == nil {
			t.Fatalf("refusal reports side effects: %+v", result)
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
