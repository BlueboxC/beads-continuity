package main

// End-to-end cover for `bd create --file` on the DIRECT route, which is where
// this command's behaviour changed when it moved onto issueops.BatchCreator.
// They run the real bd binary against an isolated embedded-Dolt workspace and
// reuse create_deps_atomic_test.go's hermetic environment and output helpers.

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

// writeMarkdownPlan writes a plan file and returns its path.
func writeMarkdownPlan(t *testing.T, dir, name, body string) string {
	t.Helper()
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write markdown plan: %v", err)
	}
	return path
}

func TestCreateFromMarkdownFile(t *testing.T) {
	bd := buildBDForInitTests(t)
	dir := t.TempDir()
	runCreateDepsBD(t, bd, dir, "init", "--backend", "dolt", "--prefix", "test",
		"--quiet", "--non-interactive", "--skip-hooks", "--skip-agents")

	t.Run("creates_every_template_in_the_file", func(t *testing.T) {
		plan := writeMarkdownPlan(t, dir, "plan.md", `## First from file

Opening body.

### Priority
1

### Type
bug

### Labels
api, batch

## Second from file

### Description
Second body.
`)
		out := runCreateDepsBD(t, bd, dir, "create", "--file", plan)
		if !strings.Contains(out, "Created 2 issues from") {
			t.Errorf("create --file output = %q, want the two-issue summary", out)
		}
		titles := createDepsIssueTitles(t, bd, dir)
		for _, title := range []string{"First from file", "Second from file"} {
			if !titles[title] {
				t.Errorf("issue %q was not created", title)
			}
		}
	})

	// The direct route used to hand the whole slice to the store's batch create
	// with no create-only guard and no per-item content validation. A file the
	// workspace refuses now refuses WHOLE, so a caller can fix the file and
	// re-run it without working out which half landed.
	t.Run("a_refused_template_leaves_nothing_behind", func(t *testing.T) {
		plan := writeMarkdownPlan(t, dir, "half-bad.md", `## Lands before the refusal

### Description
Fine.

## Names a dependency that does not exist

### Dependencies
test-nosuchissue
`)
		out, err := runCreateDepsBDRaw(bd, dir, "create", "--file", plan)
		if err == nil {
			t.Fatalf("create --file with an unresolvable dependency exited 0; output:\n%s", out)
		}
		titles := createDepsIssueTitles(t, bd, dir)
		if titles["Lands before the refusal"] {
			t.Error(`issue "Lands before the refusal" persisted despite the file being refused: ` +
				"a --file create is all or nothing")
		}
		if titles["Names a dependency that does not exist"] {
			t.Error("the refusing template itself persisted")
		}
	})

	// --ephemeral is not in singleIssueOnlyFlags, so this route has always
	// ACCEPTED it while building the issues without reading it: a caller who
	// asked for scratch work got durable rows and no warning.
	t.Run("ephemeral_is_honoured_rather_than_accepted_and_ignored", func(t *testing.T) {
		plan := writeMarkdownPlan(t, dir, "scratch.md", `## Scratch from file

### Description
Ephemeral body.
`)
		out := runCreateDepsBD(t, bd, dir, "create", "--file", plan, "--ephemeral", "--json")
		if !strings.Contains(out, `"ephemeral": true`) && !strings.Contains(out, `"ephemeral":true`) {
			t.Errorf("create --file --ephemeral --json does not report an ephemeral row:\n%s", out)
		}
		// And the durable listing does not carry it, which is the half a
		// caller asking for scratch work actually depends on.
		if createDepsIssueTitles(t, bd, dir)["Scratch from file"] {
			t.Error(`"Scratch from file" is in the durable listing; --file --ephemeral must create a wisp`)
		}
	})

	// The edges the file declares are written with the issues, in the same
	// transaction — including an edge onto an issue created EARLIER in the file.
	t.Run("writes_the_edges_the_file_declares", func(t *testing.T) {
		blocker := strings.TrimSpace(runCreateDepsBD(t, bd, dir, "create", "file edge blocker", "--silent"))
		if blocker == "" {
			t.Fatal("blocker create returned empty ID")
		}
		plan := writeMarkdownPlan(t, dir, "edges.md", `## Blocked by an existing issue

### Dependencies
`+blocker+`
`)
		runCreateDepsBD(t, bd, dir, "create", "--file", plan)
		// --direction=up is what the blocker BLOCKS, which is where the edge
		// the file declared shows up.
		out := runCreateDepsBD(t, bd, dir, "dep", "tree", blocker, "--direction=up", "--json")
		if !strings.Contains(out, "Blocked by an existing issue") {
			t.Errorf("the declared edge was not written; dependents of %s:\n%s", blocker, out)
		}
	})
}

func TestCreateFromMarkdownDependencies(t *testing.T) {
	bd := buildBDForInitTests(t)
	dir := t.TempDir()
	runCreateDepsBD(t, bd, dir, "init", "--backend", "dolt", "--prefix", "md",
		"--quiet", "--non-interactive", "--skip-hooks", "--skip-agents")
	checkMarkdownDependencyContract(t, dir, func(args ...string) (string, error) {
		cmd := exec.Command(bd, args...)
		cmd.Dir, cmd.Env = dir, createDepsTestEnv(dir)
		out, err := cmd.Output()
		if exit, ok := err.(*exec.ExitError); ok {
			out = append(out, exit.Stderr...)
		}
		return string(out), err
	})
}

// Exercise the same persisted-edge and scheduler contract on both CLI routes.
func checkMarkdownDependencyContract(t *testing.T, dir string, run func(...string) (string, error)) {
	t.Helper()
	ok := func(args ...string) string {
		t.Helper()
		out, err := run(args...)
		if err != nil {
			t.Fatalf("bd %v: %v\n%s", args, err, out)
		}
		return out
	}
	ids := func(verb string) map[string]bool {
		t.Helper()
		args := []string{verb, "--json"}
		if verb == "ready" {
			args = append(args, "--limit", "0")
		}
		out := ok(args...)
		if strings.Index(out, "[") < 0 {
			t.Fatalf("missing %s JSON: %s", verb, out)
		}
		var rows []types.Issue
		if err := json.Unmarshal([]byte(out[strings.Index(out, "["):]), &rows); err != nil {
			t.Fatalf("decode %s: %v\n%s", verb, err, out)
		}
		result := make(map[string]bool, len(rows))
		for _, row := range rows {
			result[row.ID] = true
		}
		return result
	}
	cases := []struct {
		prefix, kind string
		reverse      bool
	}{
		{"", "blocks", false},
		{"depends-on:", "blocks", false},
		{"blocked-by:", "blocks", false},
		{"blocks:", "blocks", true},
		{"discovered-from:", "discovered-from", false},
		{"my-custom-type:", "my-custom-type", false},
	}
	targets := make([]string, len(cases))
	var body strings.Builder
	for i, tc := range cases {
		targets[i] = strings.TrimSpace(ok("create", fmt.Sprintf("Target %d", i), "--silent"))
		fmt.Fprintf(&body, "## Markdown %d\n\n### Deps\n%s%s\n\n", i, tc.prefix, targets[i])
	}
	plan := writeMarkdownPlan(t, dir, "dependencies.md", body.String())
	out := ok("create", "--file", plan, "--json")
	if strings.Index(out, "[") < 0 {
		t.Fatalf("missing create JSON: %s", out)
	}
	var created []types.Issue
	if err := json.Unmarshal([]byte(out[strings.Index(out, "["):]), &created); err != nil {
		t.Fatalf("decode markdown create: %v\n%s", err, out)
	}
	if len(created) != len(cases) {
		t.Fatalf("created %d issues, want %d", len(created), len(cases))
	}
	ready, blocked := ids("ready"), ids("blocked")
	for i, tc := range cases {
		source, target := created[i].ID, targets[i]
		if tc.reverse {
			source, target = target, source
		}
		out := ok("show", source, "--json")
		if strings.Index(out, "[") < 0 {
			t.Fatalf("missing show JSON: %s", out)
		}
		var details []types.IssueDetails
		if err := json.Unmarshal([]byte(out[strings.Index(out, "["):]), &details); err != nil {
			t.Fatalf("decode show: %v\n%s", err, out)
		}
		if len(details) != 1 || len(details[0].Dependencies) != 1 {
			t.Errorf("%q: want exactly one persisted dependency: %s", tc.prefix, out)
		} else if dep := details[0].Dependencies[0]; dep.ID != target || string(dep.DependencyType) != tc.kind {
			t.Errorf("%q: persisted dependency = %s (%s), want %s (%s)", tc.prefix, dep.ID, dep.DependencyType, target, tc.kind)
		}
		wantBlocked := tc.kind == "blocks"
		if blocked[source] != wantBlocked || ready[source] == wantBlocked || !ready[target] || blocked[target] {
			t.Errorf("%q: ready/blocked disagree with persisted direction: ready=%v blocked=%v", tc.prefix, ready, blocked)
		}
		if wantBlocked {
			ok("close", target)
		}
	}
	ready, blocked = ids("ready"), ids("blocked")
	for i, tc := range cases {
		source := created[i].ID
		if tc.reverse {
			source = targets[i]
		}
		if !ready[source] || blocked[source] {
			t.Errorf("%q: source %s did not become ready after its blocker closed", tc.prefix, source)
		}
	}
	before := ok("list", "--all", "--limit", "0", "--json")
	for _, tc := range []struct{ name, deps string }{
		{"duplicates", "depends-on:%s\ndepends-on:%s"},
		{"duplicate-aliases", "depends-on:%s\nblocked-by:%s"},
		{"cycle", "blocks:%s\ndepends-on:%s"},
		{"empty-target", "depends-on:"},
		{"empty-type", ":%s"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			deps := strings.ReplaceAll(tc.deps, "%s", created[0].ID)
			plan := writeMarkdownPlan(t, dir, tc.name+".md", "## Innocent\n\n## Refused\n\n### Deps\n"+deps+"\n")
			out, err := run("create", "--file", plan, "--json")
			if err == nil {
				t.Fatalf("invalid batch succeeded: %s", out)
			}
			if after := ok("list", "--all", "--limit", "0", "--json"); after != before {
				t.Errorf("refused batch changed persisted issues\nbefore: %s\nafter: %s", before, after)
			}
		})
	}
}
