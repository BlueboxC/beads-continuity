//go:build cgo

package main

import (
	"path/filepath"
	"strings"
	"testing"
)

// The Issue query says when it, and not the Bead list, answered, and a
// configured directory label never refuses a Memory Type. The label comes from
// a workspace config.yaml, which initialization does not write.
func TestGraphPreviewListNoticeWorkflow(t *testing.T) {
	bd := buildBDUnderTest(t)
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			const scope = "https://example.invalid/notice/"
			work, home := graphListWorkspace(t, bd, engine, scope)
			identities := map[string]string{}
			call := func(args ...string) string {
				t.Helper()
				output := graphPolicyCLI(t, bd, work, home, nil, "", append(args, "--json")...)
				graphCaptureWorkflowIdentity(t, args, output, identities)
				return output
			}
			human := func(extra ...string) []string {
				t.Helper()
				out := graphPolicyCLI(t, bd, work, home, nil, "", append([]string{"list"}, extra...)...)
				return strings.Split(strings.TrimSuffix(out, "\n"), "\n")
			}
			structured := func(extra ...string) string {
				t.Helper()
				return graphPolicyCLI(t, bd, work, home, nil, "", append([]string{"list", "--format", "records-json"}, extra...)...)
			}
			issueQuery := func(selectedBy string) string {
				return "Memories are not listed (Issue query selected by: " + selectedBy + ")."
			}
			expect := func(what string, got []string, want ...string) {
				t.Helper()
				if strings.Join(got, "\n") != strings.Join(want, "\n") {
					t.Fatalf("%s:\n got=%q\nwant=%q", what, got, want)
				}
			}
			var workRow, otherRow string

			call("remember", "plan body", "--id", "beads/plan", "--title", "Plan")
			call("create", "Frontend work", "--id", "beads/work", "--priority", "1", "--labels", "frontend")
			call("create", "Other work", "--id", "beads/other", "--priority", "2")
			workRow = `"` + identities["beads/work"] + `" "open" P1 "Frontend work"`
			otherRow = `"` + identities["beads/other"] + `" "open" P2 "Other work"`

			// A typed Issue option selects the Issue query and says so on the line
			// under the header, naming the options in canonical order.
			lines := human("--status", "open")
			if len(lines) != 4 || lines[0] != "Issues (2; more: false; graph preview)" || lines[1] != issueQuery("--status") {
				t.Fatalf("--status open: %q", lines)
			}
			for _, args := range [][]string{{"--status", "open", "--sort", "priority"}, {"--sort", "priority", "--status", "open"}} {
				if got := human(args...); len(got) < 2 || got[1] != issueQuery("--status, --sort") {
					t.Fatalf("%v must name its options in canonical order: %q", args, got)
				}
			}
			// Structured output carries no notice: it is byte-identical to the
			// Issue Type route, which never prints one, and --quiet prints nothing.
			typed := structured("--status", "open")
			if narrowed := structured("--bead-type", "types/issue", "--status", "open"); typed != narrowed || strings.Contains(typed, "not listed") {
				t.Fatalf("structured output changed:\n got=%s\nwant=%s", typed, narrowed)
			}
			if quiet := graphPolicyCLI(t, bd, work, home, nil, "", "list", "--status", "open", "--quiet"); quiet != "" {
				t.Fatalf("--quiet printed %q", quiet)
			}
			// Narrowing to the Issue Type already told the caller it is Issues only.
			expect("Issue Type with a typed option", human("--bead-type", "types/issue", "--status", "open"),
				"Issues (2; more: false; graph preview)", workRow, otherRow)

			// A configured directory label for this directory selects the Issue
			// query too. config.yaml is read from the workspace, and the label
			// pattern is matched against the working directory.
			graphListConfig(t, work, "directory:\n  labels:\n    \""+filepath.Base(work)+"\": frontend\n")
			label := `directory.labels "frontend"`
			expect("configured label alone", human(), "Issues (1; more: false; graph preview)", issueQuery(label), workRow)
			expect("configured label with the Issue Type", human("--bead-type", "types/issue"), "Issues (1; more: false; graph preview)", workRow)
			// A Memory has no labels, so the label is not applied to one and nothing is refused.
			expect("configured label with the Memory Type", human("--bead-type", "types/memory"),
				"Beads (1; more: false; graph preview)", label+" is not applied to this Bead Type.", "  beads/plan  Memory  Plan")
			// A typed label replaces the configured one, and a typed option is named next to it.
			expect("typed label", human("--label", "frontend"), "Issues (1; more: false; graph preview)", issueQuery("--label"), workRow)
			expect("typed option and configured label", human("--status", "open"), "Issues (1; more: false; graph preview)", issueQuery("--status, "+label), workRow)
			if out := structured(); strings.Contains(out, "not listed") || strings.Contains(out, "not applied") || !strings.Contains(out, graphWorkflowIdentity(identities, scope, "beads/work")) || strings.Contains(out, graphWorkflowIdentity(identities, scope, "beads/plan")) {
				t.Fatalf("structured list with the configured label: %s", out)
			}
			// A typed Issue option with a Memory Type is still refused.
			graphPolicyCLI(t, bd, work, home, nil, "capability_unavailable", "list", "--bead-type", "types/memory", "--status", "open", "--format", "records-json")
		})
	}
}

// A typed Issue option selects the Issue query whatever its value, and with a
// Memory Type it is refused. The list command lowers the Changed state of its
// --status, --state, --type and --assignee filters once it has read them, so
// each must be seen before that, an empty value included.
func TestGraphPreviewListTypedOptionsSelectIssueQuery(t *testing.T) {
	bd := buildBDUnderTest(t)
	work, home := graphListWorkspace(t, bd, "embedded", "https://example.invalid/typed/")
	for _, args := range [][]string{{"remember", "plan body", "--id", "beads/plan", "--title", "Plan"}, {"create", "Frontend work", "--id", "beads/work"}} {
		graphPolicyCLI(t, bd, work, home, nil, "", append(args, "--json")...)
	}
	for _, tc := range []struct{ option, value string }{
		{"status", ""}, {"state", "open"}, {"state", ""}, {"type", "task"}, {"type", ""}, {"assignee", ""},
	} {
		arg := "--" + tc.option + "=" + tc.value
		out := graphPolicyCLI(t, bd, work, home, nil, "", "list", arg)
		lines := strings.Split(strings.TrimSuffix(out, "\n"), "\n")
		if len(lines) < 2 || !strings.HasPrefix(lines[0], "Issues (") || lines[1] != "Memories are not listed (Issue query selected by: --"+tc.option+")." {
			t.Errorf("%s did not select the Issue query: %q", arg, lines)
		}
	}
	for _, option := range []string{"status", "state", "type", "assignee"} {
		graphPolicyCLI(t, bd, work, home, nil, "capability_unavailable", "list", "--bead-type", "types/memory", "--"+option+"=", "--format", "records-json")
	}
}
