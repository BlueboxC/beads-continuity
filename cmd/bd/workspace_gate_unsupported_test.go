package main

import (
	"context"
	"os"
	"strings"
	"testing"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/lockfile"
	"github.com/steveyegge/beads/internal/workspacegate"
)

func TestCommandUnsupportedGateRefuses(t *testing.T) {
	oldJSON := jsonOutput
	jsonOutput = false
	t.Cleanup(func() { jsonOutput = oldJSON })
	resetGateTestEnv(t)
	t.Cleanup(releaseWorkspaceGates)
	old := acquireCommandGateSet
	t.Cleanup(func() { acquireCommandGateSet = old })
	acquireCommandGateSet = func(context.Context, workspacegate.Mode, workspacegate.Options, ...workspacegate.Gate) (*workspacegate.MultiHandle, error) {
		return nil, &lockfile.UnsupportedError{Op: "probe", Path: "/fixture/.beads.gate.lock", Err: os.ErrInvalid}
	}
	for _, readonly := range []bool{false, true} {
		t.Run(map[bool]string{false: "ordinary", true: "readonly"}[readonly], func(t *testing.T) {
			setReadonlyMode(t, readonly)
			cmd := &cobra.Command{Use: "list"}
			var err error
			message := captureStderr(t, func() { err = acquireCommandWorkspaceGates(t.Context(), cmd, newGateTestWorkspace(t)) })
			if err == nil {
				t.Fatal("unsupported filesystem admitted an ungated command")
			}
			if !strings.Contains(message, "locking is not supported") {
				t.Fatalf("unhelpful refusal: %v, %s", err, message)
			}
			if workspaceGateHandle != nil {
				t.Fatal("failed acquisition retained a handle")
			}
		})
	}
}
