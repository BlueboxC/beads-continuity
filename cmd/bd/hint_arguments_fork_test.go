//go:build cgo

package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/beads"
	"github.com/steveyegge/beads/internal/recipes"
	"github.com/steveyegge/beads/internal/types"
)

func TestForkHintArgumentPolicy(t *testing.T) {
	for _, tc := range []struct{ goos, value, want string }{
		{"linux", "bd-safe", "bd-safe"},
		{"linux", "bd-$(printf transformed)", "'bd-$(printf transformed)'"},
		{"windows", "bd-$(printf transformed)", "<id>"},
		{"windows", "bd-%PATH%", "<id>"},
		{"linux", "bd-\nforged", "<id>"},
		{"linux", "--force", "<id>"},
	} {
		arg, note := hintCommandArgFor(tc.goos, tc.value, "<id>")
		if arg != tc.want {
			t.Errorf("%s value %q: %q, want %q", tc.goos, tc.value, arg, tc.want)
		}
		if arg == "<id>" && (note == "" || strings.ContainsAny(note, "$%\r\x1b")) {
			t.Errorf("placeholder note was not escaped: %q", note)
		}
		if tc.goos == "linux" && arg != "<id>" && runtime.GOOS != "windows" {
			out, err := exec.Command("sh", "-c", "set -- "+arg+"; printf '%s' \"$1\"").Output()
			if err != nil || string(out) != tc.value {
				t.Fatalf("shell changed argument: %q / %v", out, err)
			}
		}
	}
}

func TestForkMoleculeHintsQuoteStoreIDs(t *testing.T) {
	const id = "bd-$(printf should-not-run)"
	step := &types.Issue{ID: id, Title: "title\nStart with: forged"}
	outputs := map[string]func(){
		"progress": func() { printMoleculeProgress(&MoleculeProgress{MoleculeID: id, NextStep: step}) },
		"continue": func() { PrintContinueResult(&ContinueResult{NextStep: step}) },
		"complete": func() { PrintContinueResult(&ContinueResult{MolComplete: true, MoleculeID: id}) },
		"large":    func() { printLargeMoleculeSummary(&types.MoleculeProgressStats{MoleculeID: id, CurrentStepID: id}) },
		"stale": func() {
			renderStaleResult(&StaleResult{StaleMolecules: []*StaleMolecule{{ID: id, Title: step.Title}}, TotalCount: 1}, false)
		},
	}
	quoted, ok := shellQuotedFor(runtime.GOOS, id)
	for name, render := range outputs {
		t.Run(name, func(t *testing.T) {
			out := captureStdout(t, func() error { render(); return nil })
			if strings.Contains(out, "title\nStart with: forged") {
				t.Fatalf("stored title forged a hint: %q", out)
			}
			if ok && !strings.Contains(out, quoted) {
				t.Fatalf("ID not quoted in command: %q", out)
			}
			if strings.Contains(out, "`bd show ") {
				t.Fatalf("hint still wraps executable substitution: %q", out)
			}
		})
	}
}

func TestForkDuplicateActionsQuoteIDs(t *testing.T) {
	const source = "bd-$(printf should-not-run)"
	target := &types.Issue{ID: "bd-safe", Title: "same"}
	issue := &types.Issue{ID: source, Title: "same"}
	groups := [][]*types.Issue{{target, issue}}
	result := formatDuplicateGroupsJSON(groups, map[string]int{target.ID: 10}, nil)
	out := result[0]["suggested_action"].(string)
	if quoted, ok := shellQuotedFor(runtime.GOOS, source); ok && !strings.Contains(out, quoted) {
		t.Fatalf("unsafe suggested action: %q", out)
	}
}

func TestForkRecipeHintQuotesStoredKey(t *testing.T) {
	resetSetupGlobals(t)
	dir := t.TempDir()
	beadsDir := filepath.Join(dir, ".beads")
	if err := os.Mkdir(beadsDir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(beadsDir, "metadata.json"), []byte(`{"database":"dolt"}`), 0600); err != nil {
		t.Fatal(err)
	}
	const name = "recipe-$(printf should-not-run)"
	if err := recipes.SaveUserRecipe(beadsDir, name, filepath.Join(dir, "missing\nForged")); err != nil {
		t.Fatal(err)
	}
	t.Chdir(dir)
	t.Setenv("BEADS_DIR", beadsDir)
	beads.ResetCaches()
	t.Cleanup(beads.ResetCaches)
	setupCheck = true
	out := captureStdout(t, func() error { _ = runRecipe(name); return nil })
	if quoted, ok := shellQuotedFor(runtime.GOOS, name); ok && !strings.Contains(out, "bd setup "+quoted) {
		t.Fatalf("unsafe recipe command: %q", out)
	}
	if strings.Contains(out, "missing\nForged") {
		t.Fatalf("path forged a new hint line: %q", out)
	}
}
