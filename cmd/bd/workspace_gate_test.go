package main

import (
	"context"
	"errors"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/uow"
	"os"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/workspacegate"
)

// resetGateTestEnv pins every env var the physical-root resolver consults so
// a developer machine's beads setup (shared-server mode, custom data dirs,
// central config) cannot change which gates these tests acquire.
func resetGateTestEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"BEADS_DOLT_SERVER_MODE",
		"BEADS_DOLT_SHARED_SERVER",
		"BEADS_DOLT_DATA_DIR",
		"BEADS_DOLT_SERVER_HOST",
		"BEADS_PROXIED_SERVER_ROOT_PATH",
		"BEADS_SHARED_SERVER_DIR",
	} {
		t.Setenv(k, "")
	}
	t.Setenv("BEADS_CENTRAL_CONFIG", filepath.Join(t.TempDir(), "no-central.json"))
}

func newGateTestWorkspace(t *testing.T) string {
	t.Helper()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(beadsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"backend":"dolt","database":"beads.db","dolt_mode":"embedded"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	return beadsDir
}

func TestCommandNeedsExclusiveGate(t *testing.T) {
	root := &cobra.Command{Use: "bd"}
	backup := &cobra.Command{Use: "backup"}
	backupRestore := &cobra.Command{Use: "restore [path]"}
	backup.AddCommand(backupRestore)
	root.AddCommand(backup)
	// Top-level `bd restore <issue-id>` is an ISSUE restore
	// (cmd/bd/restore.go), not a database restore: it must stay SHARED.
	issueRestore := &cobra.Command{Use: "restore [issue-id]"}
	root.AddCommand(issueRestore)
	list := &cobra.Command{Use: "list"}
	root.AddCommand(list)

	compact := &cobra.Command{Use: "compact"}
	compact.Flags().Bool("dry-run", false, "")
	root.AddCommand(compact)
	preview := &cobra.Command{Use: "compact"}
	preview.Flags().Bool("dry-run", true, "")
	root.AddCommand(preview)
	admin := &cobra.Command{Use: "admin"}
	semantic := &cobra.Command{Use: "compact"}
	admin.AddCommand(semantic)
	root.AddCommand(admin)

	cases := []struct {
		name string
		cmd  *cobra.Command
		want bool
	}{
		{"backup restore is exclusive", backupRestore, true},
		{"history compact is exclusive", compact, true},
		{"history preview is shared", preview, false},
		{"semantic compaction is shared", semantic, false},
		{"top-level issue restore is not", issueRestore, false},
		{"list is not", list, false},
		{"backup parent is not", backup, false},
		{"root is not", root, false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := commandNeedsExclusiveGate(tc.cmd); got != tc.want {
				t.Errorf("commandNeedsExclusiveGate(%s) = %v, want %v", tc.cmd.Name(), got, tc.want)
			}
		})
	}
}

func TestAcquireCommandWorkspaceGatesAbsentWorkspace(t *testing.T) {
	resetGateTestEnv(t)
	t.Cleanup(releaseWorkspaceGates)

	list := &cobra.Command{Use: "list"}
	missing := filepath.Join(t.TempDir(), "nope", ".beads")
	if err := acquireCommandWorkspaceGates(context.Background(), list, missing); err != nil {
		t.Fatalf("absent beadsDir must be silently ungated, got %v", err)
	}
	if workspaceGateHandle != nil {
		t.Error("absent beadsDir must leave no gate handle")
	}
}

func TestHistoryCompactionGateRefusesActiveWriter(t *testing.T) {
	resetGateTestEnv(t)
	t.Cleanup(releaseWorkspaceGates)
	beadsDir := newGateTestWorkspace(t)
	gate, err := workspacegate.ForWorkspace(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := gate.Acquire(t.Context(), workspacegate.Shared, workspacegate.Options{Reason: "active writer"})
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Release()
	root := &cobra.Command{Use: "bd"}
	compact := &cobra.Command{Use: "compact"}
	compact.Flags().Bool("dry-run", false, "")
	root.AddCommand(compact)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	if err := acquireCommandWorkspaceGates(ctx, compact, beadsDir); err == nil {
		t.Fatal("history rewrite admitted while a writer holds the shared gate")
	}
	if workspaceGateHandle != nil {
		t.Fatal("failed acquisition retained a gate")
	}
}

func TestAcquireCommandWorkspaceGatesBlockedByExclusiveHolder(t *testing.T) {
	resetGateTestEnv(t)
	t.Cleanup(releaseWorkspaceGates)
	beadsDir := newGateTestWorkspace(t)

	gate, err := workspacegate.ForWorkspace(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := gate.Acquire(context.Background(), workspacegate.Exclusive,
		workspacegate.Options{Reason: "test maintenance"})
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = holder.Release() }()

	list := &cobra.Command{Use: "list"}
	if err := acquireCommandWorkspaceGates(context.Background(), list, beadsDir); err == nil {
		t.Fatal("SHARED acquisition under a foreign exclusive holder must abort, got nil error")
	}
	if workspaceGateHandle != nil {
		t.Error("failed acquisition must leave no gate handle")
	}
}

func TestAcquireInitMutationGateKeepsReplacementExclusiveDuringPreflight(t *testing.T) {
	resetGateTestEnv(t)
	beadsDir := newGateTestWorkspace(t)
	physicalRoot := filepath.Join(filepath.Dir(beadsDir), "dolt-data")

	oldOnWait := exclusiveGateOnWait
	secondWaited := make(chan struct{}, 1)
	exclusiveGateOnWait = func(string) {
		select {
		case secondWaited <- struct{}{}:
		default:
		}
	}
	t.Cleanup(func() { exclusiveGateOnWait = oldOnWait })

	firstPreflightEntered := make(chan struct{})
	allowFirstPreflight := make(chan struct{})
	type result struct {
		h   *workspacegate.MultiHandle
		err error
	}
	firstResult := make(chan result, 1)
	go func() {
		h, err := acquireInitMutationGate(context.Background(), beadsDir, physicalRoot, func() error {
			close(firstPreflightEntered)
			<-allowFirstPreflight
			return nil
		})
		firstResult <- result{h: h, err: err}
	}()
	<-firstPreflightEntered

	secondPreflightEntered := make(chan struct{})
	secondResult := make(chan result, 1)
	go func() {
		h, err := acquireInitMutationGate(context.Background(), beadsDir, physicalRoot, func() error {
			close(secondPreflightEntered)
			return nil
		})
		secondResult <- result{h: h, err: err}
	}()

	select {
	case <-secondWaited:
	case <-secondPreflightEntered:
		t.Fatal("second replacement entered preflight while first held the mutation gates")
	}

	close(allowFirstPreflight)
	first := <-firstResult
	if first.err != nil {
		t.Fatalf("first init mutation gate: %v", first.err)
	}
	if err := first.h.Release(); err != nil {
		t.Fatalf("release first init mutation gate: %v", err)
	}

	<-secondPreflightEntered
	second := <-secondResult
	if second.err != nil {
		t.Fatalf("second init mutation gate: %v", second.err)
	}
	if err := second.h.Release(); err != nil {
		t.Fatalf("release second init mutation gate: %v", err)
	}
}

func TestAcquireInitMutationGateReleasesOnPreflightError(t *testing.T) {
	resetGateTestEnv(t)
	beadsDir := newGateTestWorkspace(t)
	physicalRoot := filepath.Join(filepath.Dir(beadsDir), "dolt-data")
	refusal := errors.New("destroy token rejected")

	_, err := acquireInitMutationGate(context.Background(), beadsDir, physicalRoot, func() error {
		return refusal
	})
	if !errors.Is(err, refusal) {
		t.Fatalf("init mutation gate error = %v, want preflight refusal", err)
	}

	h, err := acquireInitMutationGate(context.Background(), beadsDir, physicalRoot, nil)
	if err != nil {
		t.Fatalf("init mutation gate remained held after refusal: %v", err)
	}
	if err := h.Release(); err != nil {
		t.Fatalf("release init mutation gate: %v", err)
	}
}

func TestReleaseWorkspaceGatesIdempotent(t *testing.T) {
	resetGateTestEnv(t)
	beadsDir := newGateTestWorkspace(t)

	list := &cobra.Command{Use: "list"}
	if err := acquireCommandWorkspaceGates(context.Background(), list, beadsDir); err != nil {
		t.Fatal(err)
	}
	if workspaceGateHandle == nil {
		t.Fatal("expected a held gate handle")
	}
	releaseWorkspaceGates()
	if workspaceGateHandle != nil {
		t.Error("handle must be cleared on release")
	}
	// Second release must be a no-op, not a panic or double-unlock.
	releaseWorkspaceGates()

	// And the gate must actually be free again: an exclusive acquisition
	// succeeds after release.
	gate, err := workspacegate.ForWorkspace(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	h, err := gate.Acquire(context.Background(), workspacegate.Exclusive, workspacegate.Options{})
	if err != nil {
		t.Fatalf("gate still held after releaseWorkspaceGates: %v", err)
	}
	_ = h.Release()
}

// The cross-wiring guarantee: a chokepoint SHARED hold (a normal command
// mid-flight) excludes acquireMigrateGates' EXCLUSIVE acquisition on the
// same workspace. Also exercises the nil-rootCtx path inside
// acquireMigrateGates (tests have no process signal context), which used to
// panic before the nil-context normalization.
func TestChokepointSharedExcludesMigrateExclusive(t *testing.T) {
	resetGateTestEnv(t)
	t.Cleanup(releaseWorkspaceGates)
	beadsDir := newGateTestWorkspace(t)

	// rootCtx is a package global that production sets via
	// setupGracefulShutdown() in PersistentPreRunE and cancels via
	// rootCancel() in PersistentPostRunE WITHOUT resetting the var to nil —
	// harmless in production (the process exits), but any earlier in-process
	// test that exercises the full command path (Execute()) leaves rootCtx
	// pointing at an already-canceled context for whatever test runs next in
	// the same binary. acquireMigrateGates now threads rootCtx through to
	// acquireExclusiveWorkspaceGates, so this test is sensitive to that
	// leak: pin it to nil (the documented "no process signal context yet"
	// case this test exercises) regardless of what ran before it.
	oldRootCtx := rootCtx
	rootCtx = nil
	t.Cleanup(func() { rootCtx = oldRootCtx })

	oldWait := exclusiveGateWait
	exclusiveGateWait = 10 * time.Millisecond
	t.Cleanup(func() { exclusiveGateWait = oldWait })

	list := &cobra.Command{Use: "list"}
	if err := acquireCommandWorkspaceGates(context.Background(), list, beadsDir); err != nil {
		t.Fatal(err)
	}
	if workspaceGateHandle == nil {
		t.Fatal("expected a held shared gate handle")
	}

	release, err := acquireMigrateGates(beadsDir, false, "test migrate")
	if err == nil {
		release()
		t.Fatal("migrate EXCLUSIVE acquisition must fail while the chokepoint holds SHARED")
	}

	// After the shared holder releases, the migration proceeds.
	releaseWorkspaceGates()
	release, err = acquireMigrateGates(beadsDir, false, "test migrate")
	if err != nil {
		t.Fatalf("migrate acquisition after shared release: %v", err)
	}
	release()
}

func TestAcquireCommandWorkspaceGatesReleasesThePreviousCommandsHold(t *testing.T) {
	resetGateTestEnv(t)
	t.Cleanup(releaseWorkspaceGates)
	list := &cobra.Command{Use: "list"}
	ctx := context.Background()

	first := newGateTestWorkspace(t)
	if err := acquireCommandWorkspaceGates(ctx, list, first); err != nil || workspaceGateHandle == nil {
		t.Fatalf("first command must hold a gate: %v", err)
	}
	second := newGateTestWorkspace(t)
	if err := acquireCommandWorkspaceGates(ctx, list, second); err != nil || workspaceGateHandle == nil {
		t.Fatalf("second command must hold a gate: %v", err)
	}

	gate, err := workspacegate.ForWorkspace(first)
	if err != nil {
		t.Fatal(err)
	}
	holder, err := gate.Acquire(ctx, workspacegate.Exclusive, workspacegate.Options{Reason: "test maintenance"})
	if err != nil {
		t.Fatalf("exclusive acquire on the first command's workspace once the second command holds its own gates: %v, want success", err)
	}
	_ = holder.Release()
}

type gateCloseProbeStore struct {
	storage.DoltStorage
	onClose func()
}

type gateCloseProbeProvider struct {
	uow.UnitOfWorkProvider
	onClose func()
}

func TestAcquireCommandWorkspaceGatesClosesThePreviousCommandsStoreFirst(t *testing.T) {
	resetGateTestEnv(t)
	t.Cleanup(releaseWorkspaceGates)
	originalStore, originalProvider := store, uowProvider
	t.Cleanup(func() { store, uowProvider = originalStore, originalProvider })
	list := &cobra.Command{Use: "list"}
	ctx := context.Background()

	first := newGateTestWorkspace(t)
	if err := acquireCommandWorkspaceGates(ctx, list, first); err != nil || workspaceGateHandle == nil {
		t.Fatalf("first command must hold a gate: %v", err)
	}
	gate, err := workspacegate.ForWorkspace(first)
	if err != nil {
		t.Fatal(err)
	}
	// Each close records whether the first command's gate was still held.
	var closes []string
	probe := func(what string) func() {
		return func() {
			h, err := gate.Acquire(ctx, workspacegate.Exclusive, workspacegate.Options{})
			switch {
			case errors.Is(err, workspacegate.ErrBusy):
				closes = append(closes, what+" closed under the gate")
			case err == nil:
				_ = h.Release()
				closes = append(closes, what+" closed after the gate dropped")
			default:
				t.Errorf("probing the first command's gate when its %s closed: %v", what, err)
			}
		}
	}
	store = &gateCloseProbeStore{onClose: probe("store")}
	uowProvider = &gateCloseProbeProvider{onClose: probe("provider")}

	if err := acquireCommandWorkspaceGates(ctx, list, newGateTestWorkspace(t)); err != nil {
		t.Fatalf("second command: %v", err)
	}
	if want := []string{"provider closed under the gate", "store closed under the gate"}; !slices.Equal(closes, want) {
		t.Fatalf("the previous command's storage: %v, want %v", closes, want)
	}
	if store != nil || uowProvider != nil {
		t.Fatal("the previous command's store and provider must not survive into the next command")
	}
}

func setReadonlyMode(t *testing.T, on bool) {
	t.Helper()
	original := readonlyMode
	readonlyMode = on
	t.Cleanup(func() { readonlyMode = original })
}

func newGateTestServerWorkspace(t *testing.T) string {
	t.Helper()
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	if err := os.MkdirAll(filepath.Join(beadsDir, "dolt"), 0o755); err != nil {
		t.Fatal(err)
	}
	meta := `{"backend":"dolt","database":"beads.db","dolt_mode":"server"}`
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(meta), 0o600); err != nil {
		t.Fatal(err)
	}
	return beadsDir
}

func dirEntryNames(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

func TestAcquireCommandWorkspaceGatesStrictReadonlyCreatesNoGateFiles(t *testing.T) {
	layouts := []struct {
		name  string
		build func(*testing.T) string
	}{
		{"embedded", newGateTestWorkspace},
		{"local server", newGateTestServerWorkspace},
	}
	for _, tc := range layouts {
		t.Run(tc.name, func(t *testing.T) {
			resetGateTestEnv(t)
			t.Cleanup(releaseWorkspaceGates)
			setReadonlyMode(t, true)
			beadsDir := tc.build(t)
			projectDir := filepath.Dir(beadsDir)
			projectBefore, beadsBefore := dirEntryNames(t, projectDir), dirEntryNames(t, beadsDir)

			list := &cobra.Command{Use: "list"}
			if err := acquireCommandWorkspaceGates(context.Background(), list, beadsDir); err != nil {
				t.Fatalf("strict readonly acquisition on a never-gated workspace: %v", err)
			}
			// SHARED fail-open also returns nil and creates nothing; it is
			// told apart by running with no handle at all.
			if workspaceGateHandle == nil {
				t.Fatal("strict readonly acquisition on a never-gated workspace ran ungated, want a handle that holds nothing")
			}
			if !workspaceGateHandle.Empty() {
				t.Fatal("strict readonly acquisition on a never-gated workspace locked a gate file")
			}
			if got := dirEntryNames(t, projectDir); !slices.Equal(got, projectBefore) {
				t.Errorf("strict readonly changed %s\n before: %v\n after:  %v", projectDir, projectBefore, got)
			}
			if got := dirEntryNames(t, beadsDir); !slices.Equal(got, beadsBefore) {
				t.Errorf("strict readonly changed %s\n before: %v\n after:  %v", beadsDir, beadsBefore, got)
			}
		})
	}
}

func (s *gateCloseProbeStore) Close() error {
	s.onClose()
	return nil
}

func (p *gateCloseProbeProvider) Close(context.Context) error {
	p.onClose()
	return nil
}
