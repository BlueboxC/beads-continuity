package fix

import (
	"os"
	"path/filepath"

	"github.com/steveyegge/beads/internal/beads"
)

// PlanRedirectStub lists individual disposable runtime files and retained
// entry names. Callers must establish redirect-stub ownership first.
func PlanRedirectStub(beadsDir string) (remove, kept []string, err error) {
	entries, err := os.ReadDir(beadsDir)
	if err != nil {
		return nil, nil, err
	}
	hasRedirect, hasConfig := false, false
	for _, entry := range entries {
		switch entry.Name() {
		case "redirect":
			hasRedirect = true
		case "config.yaml", "metadata.json", "config.json":
			hasConfig = true
		}
	}
	stateLeftover := false
	if hasRedirect && !hasConfig {
		// Use discovery's own parser and detached-checkout normalization.
		stateLeftover = filepath.Clean(beads.FollowRedirect(beadsDir)) != filepath.Clean(beadsDir)
	}
	for _, entry := range entries {
		name := entry.Name()
		if name == "redirect" || name == ".gitkeep" || (hasRedirect && (name == "metadata.json" || name == "config.json")) {
			continue
		}
		runtime := false
		switch name {
		case "daemon.lock", "daemon.log", "daemon.pid", "bd.sock":
			runtime = true
		case ".local_version", "last-touched":
			runtime = stateLeftover
		}
		if runtime && (entry.Type().IsRegular() || (name == "bd.sock" && entry.Type()&os.ModeType == os.ModeSocket)) {
			remove = append(remove, filepath.Join(beadsDir, name))
		} else {
			kept = append(kept, name)
		}
	}
	return remove, kept, nil
}
