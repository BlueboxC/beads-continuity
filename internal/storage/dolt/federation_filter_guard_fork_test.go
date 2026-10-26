package dolt

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/config"
)

// With no backing pool, any commit or transfer is a test failure. Rejected
// filtering must precede them, including the default wisp exclusion.
func TestForkFilteredFederationRefusesBeforeStoreAccess(t *testing.T) {
	err := config.WithBeadsDir(t.TempDir(), func() {
		for _, excluded := range [][]string{{"wisp"}, {"private"}} {
			config.Set("federation.exclude_types", excluded)
			s := &DoltStore{branch: "main"}
			result, err := s.Sync(context.Background(), "peer", "")
			if err == nil || !strings.Contains(err.Error(), "excluded rows remain in history") {
				t.Fatalf("unsafe filtering must refuse before store access: result=%+v err=%v", result, err)
			}
			if result.Fetched || result.Merged || result.Pushed || result.Error == nil {
				t.Fatalf("refusal reports side effects: %+v", result)
			}
		}
	})
	if err != nil {
		t.Fatal(err)
	}
}
