//go:build cgo && unix

package main

import (
	"context"
	"fmt"
	"github.com/steveyegge/beads/internal/storage/uow"
	"os"
	"path/filepath"
	"reflect"
	"testing"
	"time"
)

// A successful read must not merely hide write commands: the actual provider
// must reject SQL writes and cannot bootstrap files or a missing database.
func TestStrictReadonlyProxiedExistingOnly(t *testing.T) {
	requireManagedLocalProxiedEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdManagedLocalInit(t, bd, "readonlyprobe", 5*time.Minute)
	assertStrictReadonlyProxiedExistingOnly(t, bd, p)
}

func TestStrictReadonlyProxiedExternal(t *testing.T) {
	requireManagedLocalProxiedEnv(t)
	bd := buildEmbeddedBD(t)
	upstream := bdManagedLocalInit(t, bd, "readonly_upstream", 5*time.Minute)
	backend := readManagedBackendPidFile(t, upstream)
	if backend == nil {
		t.Fatal("owned upstream is absent")
	}
	p := bdProxiedInit(t, bd, "readonly_external", "--proxied-server-external-host", "127.0.0.1", "--proxied-server-external-port", fmt.Sprint(backend.Port))
	assertStrictReadonlyProxiedExistingOnly(t, bd, p)
}

func assertStrictReadonlyProxiedExistingOnly(t *testing.T, bd string, p proxiedProject) {
	t.Helper()
	issue := bdProxiedCreate(t, bd, p.dir, "readable task")
	db := openProxiedDB(t, p)
	snapshot := func() map[string]string {
		t.Helper()
		out := map[string]string{}
		entries, err := os.ReadDir(p.beadsDir)
		if err != nil {
			t.Fatal(err)
		}
		names := []string{filepath.Join(p.dir, ".beads.gate.lock")}
		for _, entry := range entries {
			if !entry.IsDir() {
				names = append(names, filepath.Join(p.beadsDir, entry.Name()))
			}
		}
		for _, name := range names {
			data, err := os.ReadFile(name)
			if err != nil {
				t.Fatal(err)
			}
			info, err := os.Stat(name)
			if err != nil {
				t.Fatal(err)
			}
			out[name] = fmt.Sprintf("%o:%d:%x", info.Mode(), info.ModTime().UnixNano(), data)
		}
		return out
	}
	countHistory := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM dolt_log").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := snapshot()
	history := countHistory()
	for _, args := range [][]string{{"--readonly", "list", "--json"}, {"--readonly", "show", issue.ID, "--json"}, {"--readonly", "ready", "--json"}, {"--readonly", "memories", "--json"}} {
		out, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, args...)
		if err != nil {
			t.Fatalf("%v: %v: %s%s", args, err, out, stderr)
		}
	}
	out, stderr, err := bdProxiedRunBuffersWithEnv(t, bd, p.dir, []string{"BD_READONLY=1"}, "list", "--json")
	if err != nil {
		t.Fatalf("env readonly: %v %s%s", err, out, stderr)
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatalf("readonly changed workspace files: before=%v after=%v", before, after)
	}
	if countHistory() != history {
		t.Fatal("readonly read minted history")
	}
	for _, args := range [][]string{{"create", "forbidden"}, {"update", issue.ID, "--notes", "forbidden"}, {"--database", "absent_readonly_database", "list", "--json"}} {
		out, stderr, err := bdProxiedRunBuffersWithEnv(t, bd, p.dir, []string{"BD_READONLY=1"}, args...)
		if err == nil {
			t.Fatalf("readonly mutation/absent database accepted: %v %s%s", args, out, stderr)
		}
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatal("refusal changed workspace files")
	}
	if countHistory() != history {
		t.Fatal("refusal committed history")
	}
	// Existing shared gates are retained; a missing gate fails without creating one.
	for _, path := range []string{filepath.Join(p.beadsDir, "dolt.gate.lock"), filepath.Join(p.proxyRoot, "proxy.pid")} {
		saved := path + ".readonly-fixture"
		if err := os.Rename(path, saved); err != nil {
			t.Fatal(err)
		}
		out, stderr, runErr := bdProxiedRunBuffers(t, bd, p.dir, "--readonly", "list", "--json")
		_, statErr := os.Stat(path)
		restoreErr := os.Rename(saved, path)
		if restoreErr != nil {
			t.Fatal(restoreErr)
		}
		if runErr == nil || !os.IsNotExist(statErr) {
			t.Fatalf("missing %s was repaired/accepted: %v %s%s", path, runErr, out, stderr)
		}
	}
	if after := snapshot(); !reflect.DeepEqual(before, after) {
		t.Fatal("existing-only refusals changed files")
	}
	// Discovery of legacy config must not migrate it before readonly admission.
	metadata := filepath.Join(p.beadsDir, "metadata.json")
	legacy := filepath.Join(p.beadsDir, "config.json")
	if err := os.Rename(metadata, legacy); err != nil {
		t.Fatal(err)
	}
	legacyBefore := snapshot()
	out, stderr, err = bdProxiedRunBuffers(t, bd, p.dir, "--readonly", "list", "--json")
	if err != nil {
		t.Fatalf("legacy readonly: %v %s%s", err, out, stderr)
	}
	if after := snapshot(); !reflect.DeepEqual(legacyBefore, after) {
		t.Fatal("readonly migrated legacy metadata")
	}
	if err := os.Rename(legacy, metadata); err != nil {
		t.Fatal(err)
	}
}

func TestStrictReadonlyProxiedSQLGuard(t *testing.T) {
	requireManagedLocalProxiedEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdManagedLocalInit(t, bd, "readonlysql", 5*time.Minute)
	issue := bdProxiedCreate(t, bd, p.dir, "unchanged task")
	ctx := context.Background()
	provider, err := uow.NewReadOnlyProxyUOWProvider(ctx, p.proxyRoot, p.database, "root", "", nil, "")
	if err != nil {
		t.Fatal(err)
	}
	defer provider.Close(ctx)
	for _, query := range []string{"UPDATE issues SET title='forbidden' WHERE id='" + issue.ID + "'", "CREATE TABLE forbidden_readonly_table (id INT)", "CALL DOLT_ADD('.')", "SELECT 1 UNION SELECT 2 INTO OUTFILE '/tmp/beads-readonly-forbidden'", "SELECT 1; DELETE FROM issues"} {
		uw, err := provider.NewUOW(ctx)
		if err != nil {
			t.Fatal(err)
		}
		_, writeErr := uw.RawSQLUseCase().Exec(ctx, query)
		_, queryErr := uw.RawSQLUseCase().Query(ctx, query)
		if queryErr == nil {
			t.Errorf("readonly query accepted: %s", query)
		}
		commitErr := uw.Commit(ctx, "forbidden readonly commit")
		uw.Close(ctx)
		if writeErr == nil {
			t.Errorf("readonly SQL accepted: %s", query)
		}
		if commitErr == nil {
			t.Errorf("readonly commit accepted: %s", query)
		}
	}
}
