//go:build cgo

package main

import (
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/graphstore"
)

func TestGraphPreviewNativeDependencyCLI(t *testing.T) {
	bd := buildBDUnderTest(t)
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			work, home := t.TempDir(), t.TempDir()
			const scope = "https://example.invalid/native-cli/"
			args := []string{"init", "--graph-mode", "link", "--scope-url", scope, "--skip-hooks", "--skip-agents", "--non-interactive"}
			if engine == "server" {
				port := os.Getenv("BEADS_GRAPH_TEST_SERVER_PORT")
				if port == "" {
					t.Skip("set BEADS_GRAPH_TEST_SERVER_PORT for isolated SQL-server CLI qualification")
				}
				args = append(args, "--server", "--external", "--server-host", "127.0.0.1", "--server-port", port, "--server-user", "root")
			}
			call := func(args ...string) string {
				t.Helper()
				return graphPolicyCLI(t, bd, work, home, nil, "", append(args, "--json")...)
			}
			call(args...)
			call("create", "Source", "--id", "beads/source")
			types := []string{"blocks", "parent-child", "discovered-from", "related"}
			urls := make([]string, len(types))
			for i, typ := range types {
				var err error
				urls[i], err = graphstore.NativeDependencyTypeURL(scope, typ)
				if err != nil {
					t.Fatal(err)
				}
			}
			for i, typ := range types {
				target := fmt.Sprintf("beads/target-%d", i)
				call("create", typ, "--id", target)
				command := []string{"dep", "add", "beads/source", target, "--type", typ}
				if i == 3 {
					command = []string{"link", "beads/source", target, "--link-type", urls[i], "--unconditional-source"}
				}
				command = append(command, "--id", fmt.Sprintf("links/edge-%d", i), "--metadata", `{"evidence":"fixture","nested":{"value":9007199254740991}}`, "--thread", "thread-α")
				result := graphMixedResult[graphstore.DependencyResult](t, call(command...))
				if result.Link.Type != urls[i] || result.Link.Properties["threadId"] != "thread-α" || result.Link.Properties["nativeId"] == "" || !strings.Contains(string(result.Link.Metadata), `"evidence":"fixture"`) || !strings.Contains(string(result.Link.Metadata), `9007199254740991`) {
					t.Fatalf("%s lost native fields: %+v", typ, result.Link)
				}
				current := graphMixedResult[graphstore.LinkRecord](t, call("show", fmt.Sprintf("links/edge-%d", i)))
				if !reflect.DeepEqual(current, result.Link) {
					t.Fatalf("%s process reopen differs", typ)
				}
				if duplicate := graphMixedResult[graphstore.DependencyResult](t, call(command...)); duplicate.Changed || !reflect.DeepEqual(duplicate.Link, result.Link) {
					t.Fatalf("%s duplicate minted history", typ)
				}
			}
			source := graphMixedResult[graphstore.IssueRecord](t, call("show", "beads/source"))
			links := graphMixedResult[[]graphstore.LinkRecord](t, call("links", "beads/source"))
			if len(source.Owned) != 4 || len(links) != 4 {
				t.Fatalf("owned=%d incident=%d", len(source.Owned), len(links))
			}
			graphPolicyCLI(t, bd, work, home, nil, "invalid_properties", "update", "links/edge-3", "--properties", `{"threadId":"changed"}`, "--json")
			if after := graphMixedResult[graphstore.IssueRecord](t, call("show", "beads/source")); !reflect.DeepEqual(after, source) {
				t.Fatal("unsupported native-field edit changed source")
			}
			graphPolicyCLI(t, bd, work, home, nil, "invalid_properties", "dep", "add", "beads/source", "beads/target-0", "--metadata", `{"unsafe":9007199254740993}`, "--json")
			if after := graphMixedResult[graphstore.IssueRecord](t, call("show", "beads/source")); !reflect.DeepEqual(after, source) {
				t.Fatal("unrepresentable metadata changed source")
			}
			call("graph", "--project")
		})
	}
}
