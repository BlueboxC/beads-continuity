//go:build cgo

package main

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

// The conversion-only admission is meaningful only if a native plane larger
// than the final graph bound can be pruned without changing the source.
func TestGraphConversionOversizedCodePruneIsCandidateOnly(t *testing.T) {
	bd := buildBDUnderTest(t)
	work, home, _ := conversionCLIFixture(t, bd, true)
	beadsDir, err := filepath.EvalSymlinks(filepath.Join(work, ".beads"))
	if err != nil {
		t.Fatal(err)
	}
	cfg, err := configfile.LoadForDiscovery(beadsDir)
	if err != nil {
		t.Fatal(err)
	}
	db, cleanup, err := embeddeddolt.OpenSQL(t.Context(), filepath.Join(beadsDir, "embeddeddolt"), cfg.GetDoltDatabase(), "main")
	if err != nil {
		t.Fatal(err)
	}
	var rows []string
	var args []any
	for i := 0; i < 1500; i++ {
		value := fmt.Sprintf("code-v1:obsolete-%d:", i) + strings.Repeat("x", 49100)
		digest := sha256.Sum256([]byte(value))
		key := "kv.memory.@knowledge/code/blob/" + hex.EncodeToString(digest[:])
		rows = append(rows, "(?,?)")
		args = append(args, key, value)
	}
	_, writeErr := db.ExecContext(t.Context(), "INSERT INTO config (`key`,value) VALUES "+strings.Join(rows, ","), args...)
	closeErr := cleanup()
	if writeErr != nil || closeErr != nil {
		t.Fatalf("oversized native fixture: %v %v", writeErr, closeErr)
	}
	root := t.TempDir()
	planFile := filepath.Join(root, "plan.json")
	rehearsal := filepath.Join(root, "rehearsal")
	out, _, exit := graphGenerationRun(t, bd, work, home, "graph-convert", "plan", "--workspace", beadsDir, "--scope-url", "https://example.invalid/oversized-converter/", "--out", planFile)
	if exit == 0 || out != "" {
		t.Fatal("ordinary plan admitted an oversized plane")
	}
	if _, err := os.Lstat(planFile); !os.IsNotExist(err) {
		t.Fatal("refused plan published an artifact")
	}
	run := func(args ...string) string {
		t.Helper()
		out, stderr, exit := graphGenerationRun(t, bd, work, home, args...)
		if exit != 0 {
			t.Fatalf("%v: %s", args, stderr)
		}
		return out
	}
	var plan graphConversionPlan
	if err := json.Unmarshal([]byte(run("graph-convert", "plan", "--workspace", beadsDir, "--scope-url", "https://example.invalid/oversized-converter/", "--out", planFile, "--prune-obsolete-code")), &plan); err != nil {
		t.Fatal(err)
	}
	var receipt graphConversionReceipt
	if err := json.Unmarshal([]byte(run("graph-convert", "prepare", "--plan", planFile, "--out", rehearsal, "--actor", "owner", "--writers-stopped")), &receipt); err != nil {
		t.Fatal(err)
	}
	if len(receipt.PrunedCode) < 1500 || receipt.AdoptionPlan == nil {
		t.Fatal("obsolete code was not pruned on the candidate")
	}
	current, err := graphstore.PlanConversionForCodePrune(t.Context(), graphConversionOptions(plan))
	if err != nil || current.State.Tables["config"] != plan.Store.State.Tables["config"] {
		t.Fatalf("prune changed source: %v", err)
	}
	run("graph-convert", "verify", "--receipt", filepath.Join(rehearsal, "receipt.json"))
	if _, err := os.Lstat(filepath.Join(beadsDir, graphPreviewMarker)); !os.IsNotExist(err) {
		t.Fatal("prune converted the original")
	}
}
