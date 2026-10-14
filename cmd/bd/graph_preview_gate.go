package main

import (
	"errors"
	"os"
	"path/filepath"
	"reflect"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/workspacegate"
)

// Graph commands return before the ordinary store-opening gate. Keep a shared
// hold through their own RunE, including errors and long-running viewers.
func gateGraphPreviewCommand(cmd *cobra.Command) {
	if cmd == codexHookCmd {
		return
	} // Its child prime holds the gate; keep hook failure/fallback bookkeeping live.
	original := cmd.RunE
	if original == nil {
		return
	}
	cmd.RunE = func(cmd *cobra.Command, args []string) (err error) {
		defer func() { cmd.RunE = original }()
		if !graphPreviewActive || graphPreviewConfig == nil {
			return original(cmd, args)
		}
		gates, err := buildWorkspaceGateSet(graphPreviewDir, graphOptions(graphPreviewConfig).DataDir)
		if err != nil {
			return err
		}
		handle, err := workspacegate.AcquireAll(getRootContext(), workspacegate.Shared, workspacegate.Options{Reason: "Graph Preview command"}, gates...)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, handle.Release()) }()
		cfg, err := configfile.LoadForDiscovery(graphPreviewDir)
		if err != nil {
			return err
		}
		if cfg != nil && cfg.DoltMode == configfile.DoltModeServer {
			cfg.DoltServerUser = cfg.GetDoltServerUser()
		}
		marker, markerErr := os.ReadFile(filepath.Join(graphPreviewDir, graphPreviewMarker)) // #nosec G304 -- fixed format marker in the selected workspace, revalidated under its gate
		if markerErr != nil || cfg == nil || !reflect.DeepEqual(cfg, graphPreviewConfig) || !graphPreviewGenerationSchema(marker, cfg.GraphSchemaVersion) {
			return errors.New("Graph Preview selector changed during admission; retry after conversion completes")
		}
		return original(cmd, args)
	}
}

// Activity, prime and maintenance bypass the ordinary store-opening chokepoint.
func gateNativeContinuityCommand(cmd *cobra.Command) {
	selected := cmd == primeCmd || cmd == codexActivityCmd
	for c := cmd; c != nil; c = c.Parent() {
		if c == activityCmd || c == maintainCmd {
			selected = true
		}
	}
	if !selected || cmd.RunE == nil {
		return
	}
	original := cmd.RunE
	cmd.RunE = func(cmd *cobra.Command, args []string) (err error) {
		defer func() { cmd.RunE = original }()
		dir := beads.FindBeadsDir()
		if dir == "" {
			return original(cmd, args)
		}
		gates, err := buildWorkspaceGateSet(dir)
		if err != nil {
			return err
		}
		handle, err := workspacegate.AcquireAll(getRootContext(), workspacegate.Shared, workspacegate.Options{Reason: "native scoped continuity"}, gates...)
		if err != nil {
			return err
		}
		defer func() { err = errors.Join(err, handle.Release()) }()
		if _, err := os.Lstat(filepath.Join(dir, graphPreviewMarker)); !os.IsNotExist(err) {
			return errors.New("workspace format changed during native admission; retry after conversion completes")
		}
		cfg, err := configfile.LoadForDiscovery(dir)
		if err != nil {
			return err
		}
		mode, err := cfg.GetGraphMode()
		if err != nil {
			return err
		}
		if mode == "link" {
			return errors.New("workspace converted during native admission; retry with the graph route")
		}
		return original(cmd, args)
	}
}
