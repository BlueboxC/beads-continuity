//go:build cgo

package main

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"
	"time"

	"github.com/spf13/cobra"
	"github.com/steveyegge/beads/internal/storage/graphstore"
	"github.com/steveyegge/beads/internal/workspacegate"
)

func conversionCLIFixture(t *testing.T, bd string, code bool) (work, home, issue string) {
	t.Helper()
	work, home = t.TempDir(), t.TempDir()
	run := func(args ...string) string {
		t.Helper()
		out, errOut, exit := graphGenerationRun(t, bd, work, home, args...)
		if exit != 0 {
			t.Fatalf("%v exit %d: %s", args, exit, errOut)
		}
		return out
	}
	run("init", "--prefix", "convert", "--skip-hooks", "--skip-agents")
	var created struct {
		ID string `json:"id"`
	}
	if err := json.Unmarshal([]byte(run("create", "Historical solved module", "--json")), &created); err != nil || created.ID == "" {
		t.Fatalf("create: %v", err)
	}
	issue = created.ID
	run("close", issue, "--reason", "Historical verification")
	run("remember", "Exact saved solution — 記憶", "--key", "prior-solution")
	if code {
		dox := "# DOX.md\n\n## Purpose\n\nDisposable converter fixture.\n\n## Ownership\n\nOnly this test.\n\n## Local Contracts\n\nNo project execution.\n\n## Work Guidance\n\n## Verification\n\n## Child DOX Index\n"
		if err := os.WriteFile(filepath.Join(work, "DOX.md"), []byte(dox), 0600); err != nil {
			t.Fatal(err)
		}
		for _, value := range []string{"one", "two"} {
			if err := os.WriteFile(filepath.Join(work, "module.go"), []byte("package fixture\nfunc Solved() string { return \""+value+"\" }\n"), 0600); err != nil {
				t.Fatal(err)
			}
			run("code", "scan", "module.go", "--languages", "go", "--json")
		}
		run("knowledge", "scan", "DOX.md", "--json")
	}
	return
}

func conversionFixtureCommand(values map[string]string, bools ...string) *cobra.Command {
	cmd := &cobra.Command{}
	for key, value := range values {
		cmd.Flags().String(key, value, "")
	}
	for _, key := range bools {
		cmd.Flags().Bool(key, true, "")
	}
	return cmd
}

// Interrupted publication must be reconstructible from disk, and a pending
// marker must fence ordinary CLI opens until the exact conversion resumes.
func TestGraphConversionInterruptedPublicationAndRollback(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("atomic graph conversion publication is deliberately unavailable on Windows")
	}
	bd := buildBDUnderTest(t)
	work, home, issue := conversionCLIFixture(t, bd, true)
	out := t.TempDir()
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	plan, err := runGraphConversionPlan(conversionFixtureCommand(map[string]string{"workspace": filepath.Join(work, ".beads"), "scope-url": "https://example.invalid/converter/", "out": filepath.Join(out, "plan.json")}, "prune-obsolete-code"))
	if err != nil {
		t.Fatal(err)
	}
	nativeGates, err := buildWorkspaceGateSet(plan.Store.Binding.WorkspaceID, plan.Store.SourceDataDir)
	if err != nil {
		t.Fatal(err)
	}
	nativeHeld, err := workspacegate.AcquireAll(ctx, workspacegate.Exclusive, workspacegate.Options{Reason: "test native continuity fence"}, nativeGates...)
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"prime"}, {"activity", "status", "--json"}, {"maintain", "once", "--json"}} {
		stdout, _, exit := graphGenerationRun(t, bd, work, home, args...)
		if exit == 0 || stdout != "" {
			t.Fatalf("native scoped command bypassed gate: %v %s", args, stdout)
		}
	}
	if err := nativeHeld.Release(); err != nil {
		t.Fatal(err)
	}
	receipt, err := runGraphConversionPrepare(conversionFixtureCommand(map[string]string{"plan": filepath.Join(out, "plan.json"), "out": filepath.Join(out, "rehearsal"), "actor": "conversion-owner"}, "writers-stopped"))
	if err != nil {
		t.Fatal(err)
	}
	if len(receipt.PrunedCode) == 0 || receipt.AdoptionPlan == nil {
		t.Fatal("candidate-only prune was not recorded")
	}
	original, err := os.ReadFile(filepath.Join(work, ".beads", "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("publication interrupted")
	for _, step := range []string{"intent", "candidate-staged", "fenced", "native-retained", "graph-installed", "selector-published", "format-published"} {
		err := runGraphConversionActivate(ctx, receipt, func(got string) error {
			if got == step {
				return fault
			}
			return nil
		})
		if !errors.Is(err, fault) {
			t.Fatalf("activation %s: %v", step, err)
		}
		if step == "fenced" || step == "native-retained" || step == "graph-installed" || step == "selector-published" {
			stdout, _, exit := graphGenerationRun(t, bd, work, home, "--readonly", "show", issue, "--json")
			if exit == 0 || stdout != "" {
				t.Fatalf("incomplete publication admitted show at %s: %s", step, stdout)
			}
		}
	}
	if err := runGraphConversionActivate(ctx, receipt, nil); err != nil {
		t.Fatal(err)
	}
	if err := runGraphConversionActivate(ctx, receipt, nil); err != nil {
		t.Fatalf("repeat activation: %v", err)
	}
	before, err := graphstore.InspectConversionState(ctx, graphConversionOptions(plan))
	if err != nil {
		t.Fatal(err)
	}
	for _, args := range [][]string{{"status", "--graph", "--json"}, {"show", issue, "--json"}, {"ready", "--json"}, {"code", "status", "--json"}, {"knowledge", "list", "--json"}, {"prime"}, {"graph", "--project", "--json"}} {
		_, stderr, exit := graphGenerationRun(t, bd, work, home, args...)
		if exit != 0 {
			t.Fatalf("graph smoke %v: %s", args, stderr)
		}
	}
	after, err := graphstore.InspectConversionState(ctx, graphConversionOptions(plan))
	if err != nil || !reflect.DeepEqual(before, after) {
		t.Fatalf("read-only graph smoke changed state: %v", err)
	}
	gates, err := buildWorkspaceGateSet(plan.Store.Binding.WorkspaceID, plan.Store.SourceDataDir)
	if err != nil {
		t.Fatal(err)
	}
	held, err := workspacegate.AcquireAll(ctx, workspacegate.Exclusive, workspacegate.Options{Reason: "test conversion fence"}, gates...)
	if err != nil {
		t.Fatal(err)
	}
	stdout, _, exit := graphGenerationRun(t, bd, work, home, "status", "--graph", "--json")
	releaseErr := held.Release()
	if exit == 0 || stdout != "" || releaseErr != nil {
		t.Fatalf("Graph Preview bypassed exclusive gate: exit=%d stdout=%s release=%v", exit, stdout, releaseErr)
	}
	for _, step := range []string{"rollback-intent", "graph-retained", "native-restored", "native-selector-restored", "native-format-restored"} {
		err := runGraphConversionRollback(ctx, receipt, func(got string) error {
			if got == step {
				return fault
			}
			return nil
		})
		if !errors.Is(err, fault) {
			t.Fatalf("rollback %s: %v", step, err)
		}
	}
	if err := runGraphConversionRollback(ctx, receipt, nil); err != nil {
		t.Fatal(err)
	}
	if err := runGraphConversionRollback(ctx, receipt, nil); err != nil {
		t.Fatalf("repeat rollback: %v", err)
	}
	restored, err := os.ReadFile(filepath.Join(work, ".beads", "metadata.json"))
	if err != nil || !reflect.DeepEqual(original, restored) {
		t.Fatalf("selector was not restored exactly: %v", err)
	}
	current, err := graphstore.PlanConversionForCodePrune(ctx, graphConversionOptions(plan))
	if err != nil || !reflect.DeepEqual(current, plan.Store) {
		t.Fatalf("native state was not restored exactly: %v", err)
	}
	if _, err := os.Lstat(filepath.Join(work, ".beads", graphPreviewMarker)); !os.IsNotExist(err) {
		t.Fatalf("rollback left format marker: %v", err)
	}
}

// The public process boundary binds its executable and refuses rollback over
// newly accepted work, even when the caller supplies the old valid receipt.
func TestGraphConversionCLIRefusesRollbackOverLaterWrites(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("atomic graph conversion publication is deliberately unavailable on Windows")
	}
	bd := buildBDUnderTest(t)
	work, home, issue := conversionCLIFixture(t, bd, false)
	root := t.TempDir()
	planFile := filepath.Join(root, "plan.json")
	rehearsal := filepath.Join(root, "rehearsal")
	run := func(args ...string) string {
		t.Helper()
		out, stderr, exit := graphGenerationRun(t, bd, work, home, args...)
		if exit != 0 {
			t.Fatalf("%v exit %d: %s", args, exit, stderr)
		}
		return out
	}
	selector := filepath.Join(work, ".beads", "metadata.json")
	withSymlinkSelector := func(args ...string) {
		t.Helper()
		target := filepath.Join(root, "symlink-selector-target.json")
		if err := os.Rename(selector, target); err != nil {
			t.Fatal(err)
		}
		defer func() {
			if err := os.Remove(selector); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(target, selector); err != nil {
				t.Fatal(err)
			}
		}()
		if err := os.Symlink(target, selector); err != nil {
			t.Fatal(err)
		}
		out, stderr, exit := graphGenerationRun(t, bd, work, home, args...)
		if exit == 0 || out != "" || !strings.Contains(stderr, "regular file") && !strings.Contains(stderr, "selector changed") {
			t.Fatalf("symbolic selector admitted: %s %s", out, stderr)
		}
		if _, err := os.Lstat(filepath.Join(work, ".beads", graphPreviewMarker)); !os.IsNotExist(err) {
			t.Fatal("symbolic selector changed the live format")
		}
	}
	withSymlinkSelector("graph-convert", "plan", "--workspace", filepath.Join(work, ".beads"), "--scope-url", "https://example.invalid/process-converter/", "--out", planFile)
	if _, err := os.Lstat(planFile); !os.IsNotExist(err) {
		t.Fatal("symbolic selector published a plan")
	}
	run("graph-convert", "plan", "--workspace", filepath.Join(work, ".beads"), "--scope-url", "https://example.invalid/process-converter/", "--out", planFile)
	run("graph-convert", "prepare", "--plan", planFile, "--out", rehearsal, "--actor", "conversion-owner", "--writers-stopped")
	receiptFile := filepath.Join(rehearsal, "receipt.json")
	withSymlinkSelector("graph-convert", "activate", "--receipt", receiptFile, "--writers-stopped")
	for _, args := range [][]string{{"--readonly", "graph-convert", "activate", "--receipt", receiptFile, "--writers-stopped"}, {"graph-convert", "activate", "--receipt", receiptFile}, {"--db", "different", "graph-convert", "verify", "--receipt", receiptFile}} {
		out, _, exit := graphGenerationRun(t, bd, work, home, args...)
		if exit == 0 || out != "" {
			t.Fatalf("unsafe flags admitted: %v %s", args, out)
		}
	}
	repeatOut, _, repeatExit := graphGenerationRun(t, bd, work, home, "graph-convert", "prepare", "--plan", planFile, "--out", rehearsal, "--actor", "owner", "--writers-stopped")
	if repeatExit == 0 || repeatOut != "" {
		t.Fatal("repeat preparation was admitted")
	}
	var sealed graphConversionReceipt
	if err := graphConversionReadJSON(receiptFile, &sealed); err != nil {
		t.Fatal(err)
	}
	var backupFile string
	if err := filepath.WalkDir(filepath.Join(rehearsal, "backup"), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if !entry.IsDir() && backupFile == "" {
			backupFile = path
		}
		return nil
	}); err != nil || backupFile == "" {
		t.Fatalf("backup seal fixture: %v", err)
	}
	backupRaw, err := os.ReadFile(backupFile)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(backupFile, append(append([]byte{}, backupRaw...), byte('x')), 0600); err != nil {
		t.Fatal(err)
	}
	sealOut, _, sealExit := graphGenerationRun(t, bd, work, home, "graph-convert", "activate", "--receipt", receiptFile, "--writers-stopped")
	if sealExit == 0 || sealOut != "" {
		t.Fatal("altered backup admitted")
	}
	if _, err := os.Lstat(filepath.Join(work, ".beads", graphPreviewMarker)); !os.IsNotExist(err) {
		t.Fatal("altered backup changed live format")
	}
	if err := os.WriteFile(backupFile, backupRaw, 0600); err != nil {
		t.Fatal(err)
	}
	run("graph-convert", "activate", "--receipt", receiptFile, "--writers-stopped")
	run("graph-convert", "verify", "--receipt", receiptFile)
	run("remember", "Later work must survive", "--id", "beads/later", "--title", "Later work")
	metadata, err := os.ReadFile(filepath.Join(work, ".beads", "metadata.json"))
	if err != nil {
		t.Fatal(err)
	}
	out, stderr, exit := graphGenerationRun(t, bd, work, home, "graph-convert", "rollback", "--receipt", receiptFile, "--writers-stopped")
	if exit == 0 || out != "" || !strings.Contains(stderr, "changed") {
		t.Fatalf("rollback overwrote new work: %s %s", out, stderr)
	}
	if got := run("recall", "beads/later"); !strings.Contains(got, "Later work must survive") {
		t.Fatal("new memory lost")
	}
	current, err := os.ReadFile(filepath.Join(work, ".beads", "metadata.json"))
	if err != nil || !reflect.DeepEqual(metadata, current) {
		t.Fatal("refused rollback changed selector")
	}
	run("show", issue, "--json")
	if _, err := os.Lstat(filepath.Join(rehearsal, "rollback.json")); !os.IsNotExist(err) {
		t.Fatalf("refused rollback published intent: %v", err)
	}
}
