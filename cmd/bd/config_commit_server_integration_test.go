//go:build cgo && integration

package main

import (
	"fmt"
	"os"
	"strconv"
	"strings"
	"testing"
)

// TestCLI_ServerModeConfigWritesCommit_E2E is the user-level pin for GH#4078:
// on the direct SQL-server route, every config-table write verb must leave no
// config dirt in the working set. Before the fix each of them committed the SQL
// transaction only, so the rows (and the custom_statuses / custom_types lookup
// tables SetConfig projects from status.custom and types.custom) sat
// uncommitted: never pushed, and a dirty internal key refused the next pull.
//
// The last step pins the kv.* screen on memory writes: a dirty internal config
// key that the memory write did not make is refused, named, and left alone.
func TestCLI_ServerModeConfigWritesCommit_E2E(t *testing.T) {
	port := testDoltServerPort
	if raw := os.Getenv("BEADS_GRAPH_TEST_SERVER_PORT"); raw != "" {
		var err error
		port, err = strconv.Atoi(raw)
		if err != nil || port < 1 || port > 65535 {
			t.Fatal("invalid disposable SQL-server port")
		}
	} else {
		skipIfNoDolt(t)
	}

	tmpDir := t.TempDir()
	env := os.Environ()
	database := uniqueTestDBName(t)
	t.Cleanup(func() {
		dropTestDatabase(database, port)
	})

	bd := func(args ...string) (string, error) {
		return runBDExecAllowErrorWithEnv(t, tmpDir, env, args...)
	}
	mustBD := func(args ...string) string {
		t.Helper()
		out, err := bd(args...)
		if err != nil {
			t.Fatalf("bd %v failed: %v\n%s", args, err, out)
		}
		return out
	}
	// dirtyConfigTables reports which of config and its lookup tables are dirty,
	// comma-joined in name order ("" when all are clean).
	dirtyConfigTables := func() string {
		t.Helper()
		out := mustBD("sql", "--csv",
			"SELECT table_name FROM dolt_status WHERE table_name IN ('config', 'custom_statuses', 'custom_types') ORDER BY table_name")
		var dirty []string
		for _, line := range strings.Split(out, "\n") {
			switch name := strings.TrimSpace(line); name {
			case "config", "custom_statuses", "custom_types":
				dirty = append(dirty, name)
			}
		}
		return strings.Join(dirty, ",")
	}

	mustBD("init", "--backend", "dolt", "--server", "--external",
		"--server-host", "127.0.0.1",
		"--server-port", fmt.Sprintf("%d", port),
		"--database", database,
		"--prefix", "cfgc", "--quiet")
	if got := dirtyConfigTables(); got != "" {
		t.Fatalf("precondition: %s dirty right after bd init", got)
	}

	for _, step := range [][]string{
		{"remember", "--key", "gh4078", "server-mode memory"},
		{"config", "set", "status.custom", "gh4078review:active"},
		{"config", "set-many", "types.custom=gh4078kind", "status.custom=gh4078review:active,gh4078parked:frozen"},
		{"config", "unset", "types.custom"},
		{"forget", "gh4078"},
		{"kv", "set", "gh7523", "user kv"},
		{"kv", "clear", "gh7523"},
	} {
		mustBD(step...)
		if got := dirtyConfigTables(); got != "" {
			t.Fatalf("after bd %s: %s left uncommitted in the working set (GH#4078)", strings.Join(step, " "), got)
		}
	}

	// Server auto-commit is off by default in Dolt: the CLI owns version commits.
	if out := mustBD("sql", "--csv", "SELECT @@dolt_transaction_commit"); !strings.Contains(out, "0") {
		t.Fatalf("fixture must use dolt_transaction_commit=0: %s", out)
	}
	for _, mode := range []string{"off", "batch"} {
		before := mustBD("sql", "--csv", "SELECT commit_hash FROM dolt_log LIMIT 1")
		for _, args := range [][]string{{"kv", "set", "deferred-key", mode}, {"remember", "deferred memory", "--key", "deferred-memory"}, {"config", "set", "custom.deferred", mode}} {
			mustBD(append([]string{"--dolt-auto-commit", mode}, args...)...)
		}
		if got := dirtyConfigTables(); got != "config" {
			t.Fatalf("%s: config dirty = %q", mode, got)
		}
		if after := mustBD("sql", "--csv", "SELECT commit_hash FROM dolt_log LIMIT 1"); after != before {
			t.Fatalf("%s committed despite deferral", mode)
		}
		mustBD("dolt", "commit", "-m", "explicit deferred config flush")
		if got := dirtyConfigTables(); got != "" {
			t.Fatalf("explicit flush left %s dirty", got)
		}
	}

	mustBD("sql", "INSERT INTO config (`key`, value) VALUES ('gh4078_internal', 'another writer')")
	out, err := bd("remember", "--key", "gh4078-screened", "screened memory")
	if err == nil {
		t.Fatalf("bd remember committed over a dirty internal config key:\n%s", out)
	}
	if !strings.Contains(out, "gh4078_internal") {
		t.Fatalf("refusal does not name the internal key:\n%s", out)
	}
	if got := dirtyConfigTables(); got != "config" {
		t.Fatalf("after the refused memory commit: dirty = %q, want config left for bd dolt commit", got)
	}
}
