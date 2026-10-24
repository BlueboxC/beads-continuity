package graphstore

import (
	"context"
	"errors"
	"strconv"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
)

func TestPublicIdentityCompatibilityDiagnosticBeforeStore(t *testing.T) {
	// A zero Store has no database: refusal must precede any read or transaction.
	var s Store
	for _, resolve := range []func(context.Context, string) (string, error){s.ResolveExactAllocatedResourcePath, s.ResolveAllocatedResourcePath} {
		for _, tc := range []struct{ key, reason string }{{"..", "dot segment"}, {"a//b", "nonempty"}} {
			path, err := resolve(context.Background(), tc.key)
			if path != "" || !errors.Is(err, storage.ErrValidation) {
				t.Fatalf("unexpected refusal: %q %v", path, err)
			}
			for _, want := range []string{strconv.Quote(tc.key), tc.reason, "choose an explicit compatible replacement ID"} {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("diagnostic %q omits %q", err, want)
				}
			}
		}
	}
}
