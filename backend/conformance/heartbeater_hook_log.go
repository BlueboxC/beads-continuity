package conformance

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/hooks"
)

// NewUpdateHookLog observes completed hooks through a disposable executable.
// Windows keeps its separate process-hook qualification boundary.
func NewUpdateHookLog(t *testing.T) (*hooks.Runner, func(context.Context) ([]string, error)) {
	t.Helper()
	if runtime.GOOS == "windows" {
		return nil, nil
	}
	dir := t.TempDir()
	logPath := filepath.Join(dir, "updates.log")
	quoted := "'" + strings.ReplaceAll(logPath, "'", "'\"'\"'") + "'"
	body := "#!/bin/sh\nprintf '%s\\n' \"$1\" >> " + quoted + "\n"
	if err := os.WriteFile(filepath.Join(dir, hooks.HookOnUpdate), []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(filepath.Join(dir, hooks.HookOnUpdate), 0700); err != nil { // #nosec G302 -- this disposable fixture must execute its owner-only update hook.
		t.Fatal(err)
	}
	runner := hooks.NewRunner(dir)
	return runner, func(ctx context.Context) ([]string, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		if !runner.Wait(runner.Timeout()) {
			return nil, fmt.Errorf("update hook did not finish")
		}
		data, err := os.ReadFile(logPath) // #nosec G304 -- fixed hook log inside the caller-owned disposable fixture directory.
		if os.IsNotExist(err) {
			return nil, nil
		}
		if err != nil {
			return nil, err
		}
		return strings.Fields(string(data)), nil
	}
}
