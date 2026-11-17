package remotecache

import (
	"os"
	"strings"
	"testing"
)

// Adapted from #7881. These are inert URL components and exercise parsing
// only. Accepted named and URL arguments remain compatible.
func TestValidateRemoteURLRefusesOptionShapedSSHComponents(t *testing.T) {
	for _, remote := range []string{
		"ssh://-fixture-user@example.invalid/project",
		"ssh://%2Dfixture-user@example.invalid/project",
		"ssh://-fixture-host/project",
		"git+ssh://-fixture-user@example.invalid/project",
		"-fixture-user@example.invalid:project",
	} {
		if err := ValidateRemoteURL(remote); err == nil {
			t.Errorf("ValidateRemoteURL(%q) accepted an option-shaped component", remote)
		}
	}
	for _, remote := range []string{
		"ssh://fixture@example.invalid/project",
		"git+ssh://example.invalid/project",
		"fixture@example.invalid:project",
		"https://example.invalid/project",
		"file:///fixtures/repository",
	} {
		if err := ValidateRemoteURL(remote); err != nil {
			t.Errorf("ValidateRemoteURL(%q): %v", remote, err)
		}
	}
}

func TestRemoteCacheEnsureRejectsSSHOptionShapeBeforeToolLookup(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("PATH", t.TempDir())
	cache := &Cache{Dir: dir}
	if _, err := cache.Ensure(t.Context(), "ssh://-fixture-user@example.invalid/project"); err == nil ||
		!strings.Contains(err.Error(), "invalid remote URL") {
		t.Errorf("Ensure error = %v, want URL validation before any Dolt lookup", err)
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Errorf("invalid remote created cache entries %v", entries)
	}
}
