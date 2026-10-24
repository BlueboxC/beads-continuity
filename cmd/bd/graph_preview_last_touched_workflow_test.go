//go:build cgo

package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/graphstore"
	"github.com/steveyegge/beads/internal/types"
)

// The ordinary interactive last-touched workflow must use the graph Issue's
// canonical identity. Its native backing ID is not a graph selector, and a
// Memory read must not redirect a later no-ID Issue write.
func TestGraphPreviewLastTouchedIssueWorkflow(t *testing.T) {
	bd := buildBDUnderTest(t)
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			work, home := t.TempDir(), t.TempDir()
			const scope = "https://example.invalid/last-touched/"
			initArgs := []string{"init", "--graph-mode", "link", "--scope-url", scope,
				"--skip-hooks", "--skip-agents", "--non-interactive"}
			if engine == "server" {
				port := os.Getenv("BEADS_GRAPH_TEST_SERVER_PORT")
				if port == "" {
					t.Skip("set BEADS_GRAPH_TEST_SERVER_PORT for ordinary shared-server qualification")
				}
				initArgs = append(initArgs, "--server", "--external", "--server-host", "127.0.0.1",
					"--server-port", port, "--server-user", "root")
			}
			identities := map[string]string{}
			call := func(args ...string) string {
				t.Helper()
				output := graphPolicyCLI(t, bd, work, home, nil, "", append(args, "--json")...)
				graphCaptureWorkflowIdentity(t, args, output, identities)
				return output
			}
			fallback := func(args ...string) string {
				t.Helper()
				output := graphPolicyCLI(t, bd, work, home, []string{"BD_LAST_TOUCHED_FALLBACK=1"}, "", append(args, "--json")...)
				graphCaptureWorkflowIdentity(t, args, output, identities)
				return output
			}
			marker := func(want string) {
				t.Helper()
				data, err := os.ReadFile(filepath.Join(work, ".beads", "last-touched"))
				if err != nil || strings.TrimSpace(string(data)) != want {
					t.Fatalf("last-touched marker = %q, err %v; want %q", data, err, want)
				}
			}
			call(initArgs...)
			graphPolicyCLI(t, bd, work, home, []string{"BD_LAST_TOUCHED_FALLBACK=1"}, "invalid_selector", "close", "--json")
			graphPolicyCLI(t, bd, work, home, []string{"BD_LAST_TOUCHED_FALLBACK=1"}, "invalid_selector",
				"update", "--priority", "0", "--json")
			call("create", "First", "--id", "first", "--priority", "2")
			marker("beads/first")
			call("create", "Second", "--id", "second", "--priority", "1")
			marker("beads/second")
			call("remember", "A note", "--id", "memory")
			call("show", "memory")
			marker("beads/second")
			graphPolicyCLI(t, bd, work, home, []string{"BD_LAST_TOUCHED_FALLBACK=1"}, "invalid_selector",
				"update", "--properties", `{"title":"Wrong","body":"Target"}`, "--json")
			marker("beads/second")
			call("show", "first")
			marker("beads/first")
			updated := graphMixedResult[graphstore.IssueMutationResult](t, fallback("update", "--priority", "0"))
			if !updated.Changed || updated.Issue.ID != graphWorkflowIdentity(identities, scope, "beads/first") || updated.Issue.Properties.Priority != 0 {
				t.Fatalf("no-ID update missed the last shown Issue: %+v", updated)
			}
			marker("beads/first")
			call("show", "second")
			closed := graphMixedResult[graphstore.IssueMutationResult](t, fallback("close", "--reason", "done"))
			if !closed.Changed || closed.Issue.ID != graphWorkflowIdentity(identities, scope, "beads/second") || closed.Issue.Properties.Status != types.StatusClosed {
				t.Fatalf("no-ID close missed the last shown Issue: %+v", closed)
			}
			marker("beads/second")
			claimed := graphMixedResult[[]graphstore.IssueRecord](t, call("ready", "--claim", "--actor", "worker"))
			if len(claimed) != 1 || claimed[0].ID != graphWorkflowIdentity(identities, scope, "beads/first") {
				t.Fatalf("ready claim selected unexpected work: %+v", claimed)
			}
			marker(graphWorkflowIdentity(identities, scope, "beads/first"))
			closed = graphMixedResult[graphstore.IssueMutationResult](t, fallback("close", "--reason", "claimed work done", "--actor", "worker"))
			if !closed.Changed || closed.Issue.ID != graphWorkflowIdentity(identities, scope, "beads/first") {
				t.Fatalf("no-ID close missed the claimed Issue: %+v", closed)
			}
			marker("beads/first")
		})
	}
}
