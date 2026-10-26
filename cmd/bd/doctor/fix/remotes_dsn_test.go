package fix

// Repair and scan DSNs retain runtime-port credentials and transport policy.

import (
	"context"
	"database/sql"
	"net"
	"os"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/go-sql-driver/mysql"
	"github.com/steveyegge/beads/internal/configfile"
)

// isolateDoltEnv clears every env var that changes fixDBDSN's port, host,
// shared-mode, or password resolution, so results don't depend on the host
// running the tests. Every reader treats "" as unset.
func isolateDoltEnv(t *testing.T) {
	t.Helper()
	for _, k := range []string{
		"BEADS_DOLT_SERVER_PORT",
		"BEADS_DOLT_PORT",
		"BEADS_DOLT_SHARED_SERVER",
		"BEADS_DOLT_SERVER_HOST",
		"BEADS_DOLT_PASSWORD",
		"BEADS_DOLT_SERVER_SOCKET",
	} {
		t.Setenv(k, "")
	}
}

func TestForkFixDBUsesLegacyPortAndMatchingCredentials(t *testing.T) {
	isolateDoltEnv(t)
	beadsDir := writeDSNFixture(t, "", map[string]string{"127.0.0.1:43123": "legacy-secret"})
	t.Setenv("BEADS_DOLT_PORT", "43123")
	cfg := &configfile.Config{DoltServerHost: "127.0.0.1", DoltServerUser: "reader", DoltDatabase: "beads"}
	got := parseDSN(t, fixDBDSN(beadsDir, cfg))
	if got.Addr != "127.0.0.1:43123" || got.Passwd != "legacy-secret" {
		t.Fatalf("endpoint/credentials = %s / password match %v", got.Addr, got.Passwd == "legacy-secret")
	}
	// The legacy environment port outranks a remembered port file.
	if err := os.WriteFile(filepath.Join(beadsDir, "dolt-server.port"), []byte("43124\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := parseDSN(t, fixDBDSN(beadsDir, cfg)); got.Addr != "127.0.0.1:43123" {
		t.Fatalf("legacy precedence lost: %s", got.Addr)
	}
	cfg.DoltServerPort = 43125
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	if err := os.Remove(filepath.Join(beadsDir, "dolt-server.port")); err != nil {
		t.Fatal(err)
	}
	if got := parseDSN(t, fixDBDSN(beadsDir, cfg)); got.Addr != "127.0.0.1:43125" {
		t.Fatalf("authoritative metadata port lost: %s", got.Addr)
	}
}

func TestForkFixRefusesConfiguredSocket(t *testing.T) {
	isolateDoltEnv(t)
	socket := filepath.Join(t.TempDir(), "missing.sock")
	t.Setenv("BEADS_DOLT_SERVER_SOCKET", socket)
	cfg := &configfile.Config{}
	db, err := openFixDB(t.TempDir(), cfg)
	if db != nil {
		_ = db.Close()
	}
	if err == nil || !strings.Contains(err.Error(), "socket") {
		t.Fatalf("socket repair must refuse before SQL open, got %v", err)
	}
}

func TestForkScanDSNRetainsSocketTLSAndCredentials(t *testing.T) {
	isolateDoltEnv(t)
	beadsDir := writeDSNFixture(t, "43123", map[string]string{"127.0.0.1:43123": "socket-secret"})
	cfg := &configfile.Config{DoltServerHost: "127.0.0.1", DoltDatabase: "beads", DoltServerTLS: true}
	got := parseDSN(t, workspaceDBDSN(beadsDir, cfg, "/missing/socket"))
	if got.Net != "unix" || got.Addr != "/missing/socket" || got.Passwd != "socket-secret" || got.TLSConfig != "true" {
		t.Fatalf("scan endpoint lost transport, TLS or credentials")
	}
}

// writeDSNFixture creates a beads dir whose port file names runtimePort and
// points BEADS_CREDENTIALS_FILE at a credentials file holding the given
// [host:port] -> password sections.
func writeDSNFixture(t *testing.T, runtimePort string, sections map[string]string) (beadsDir string) {
	t.Helper()
	beadsDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(beadsDir, "dolt-server.port"), []byte(runtimePort+"\n"), 0o600); err != nil {
		t.Fatalf("write port file: %v", err)
	}
	var creds string
	for key, pass := range sections {
		creds += "[" + key + "]\npassword = " + pass + "\n"
	}
	credFile := filepath.Join(t.TempDir(), "credentials")
	if err := os.WriteFile(credFile, []byte(creds), 0o600); err != nil {
		t.Fatalf("write credentials file: %v", err)
	}
	t.Setenv("BEADS_CREDENTIALS_FILE", credFile)
	return beadsDir
}

func parseDSN(t *testing.T, dsn string) *mysql.Config {
	t.Helper()
	parsed, err := mysql.ParseDSN(dsn)
	if err != nil {
		t.Fatalf("fixDBDSN produced an unparseable DSN %q: %v", dsn, err)
	}
	return parsed
}

// TestFixDBDSN_PasswordMatchesRuntimePort: metadata.json carries a config port
// (3307) that differs from the runtime port (3308). Credentials exist only
// under the runtime port.
func TestFixDBDSN_PasswordMatchesRuntimePort(t *testing.T) {
	isolateDoltEnv(t)
	beadsDir := writeDSNFixture(t, "3308", map[string]string{"127.0.0.1:3308": "SECRET-runtime"})

	cfg := &configfile.Config{
		Database:       "dolt",
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltServerUser: "skillrx",
		DoltServerPort: 3307, // config port DIFFERS from runtime port 3308
		DoltDatabase:   "hendrix",
	}

	parsed := parseDSN(t, fixDBDSN(beadsDir, cfg))
	if parsed.Addr != "127.0.0.1:3308" {
		t.Errorf("DSN addr = %q, want runtime port 127.0.0.1:3308", parsed.Addr)
	}
	// Pre-fix behavior: lookup keyed [127.0.0.1:3307] -> section miss -> "".
	if parsed.Passwd != "SECRET-runtime" {
		t.Errorf("DSN password = %q, want SECRET-runtime (credentials lookup must use the runtime port)", parsed.Passwd)
	}
	if parsed.User != "skillrx" || parsed.DBName != "hendrix" {
		t.Errorf("DSN user/db = %q/%q, want skillrx/hendrix", parsed.User, parsed.DBName)
	}
}

// TestFixDBDSN_PasswordResolvedWhenMetadataOmitsPort is the shape that
// reproduced in the field: metadata.json has no dolt_server_port at all, so
// the deprecated getter falls back to DefaultDoltServerPort (3307) while the
// shared server listens on 3308 and the credentials file only has a
// [127.0.0.1:3308] section.
func TestFixDBDSN_PasswordResolvedWhenMetadataOmitsPort(t *testing.T) {
	isolateDoltEnv(t)
	beadsDir := writeDSNFixture(t, "3308", map[string]string{"127.0.0.1:3308": "s3cret"})

	cfg := &configfile.Config{
		Database:       "dolt",
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltServerUser: "skillrx",
		DoltDatabase:   "beads",
		// DoltServerPort deliberately omitted.
	}

	// Precondition: the deprecated getter does miss; this is the bug.
	if got := cfg.GetDoltServerPassword(); got != "" {
		t.Fatalf("precondition broken: deprecated GetDoltServerPassword() = %q, want empty (3307 miss)", got)
	}

	parsed := parseDSN(t, fixDBDSN(beadsDir, cfg))
	if parsed.Addr != "127.0.0.1:3308" {
		t.Errorf("DSN addr = %q, want 127.0.0.1:3308", parsed.Addr)
	}
	if parsed.Passwd != "s3cret" {
		t.Errorf("DSN password = %q, want s3cret from the [127.0.0.1:3308] section", parsed.Passwd)
	}
}

// TestFixDBDSN_EnvPasswordStillWins locks BEADS_DOLT_PASSWORD precedence over
// the credentials file; the fix must not change that contract.
func TestFixDBDSN_EnvPasswordStillWins(t *testing.T) {
	isolateDoltEnv(t)
	beadsDir := writeDSNFixture(t, "3308", map[string]string{"127.0.0.1:3308": "from-file"})
	t.Setenv("BEADS_DOLT_PASSWORD", "from-env")

	cfg := &configfile.Config{
		Database:       "dolt",
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltServerUser: "u",
		DoltServerPort: 3307,
		DoltDatabase:   "db",
	}
	parsed := parseDSN(t, fixDBDSN(beadsDir, cfg))
	if parsed.Passwd != "from-env" {
		t.Errorf("DSN password = %q, want from-env (env precedence preserved)", parsed.Passwd)
	}
}

func TestForkSocketScansAndRepairAdmission(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("unix socket fixture")
	}
	isolateDoltEnv(t)
	dir, err := os.MkdirTemp("", "bds")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	socket := filepath.Join(dir, "dolt.sock")
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = ln.Close() })
	var accepted atomic.Int32
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			accepted.Add(1)
			_ = c.Close()
		}
	}()
	tcp, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = tcp.Close() })
	t.Setenv("BEADS_DOLT_SERVER_PORT", strconv.Itoa(tcp.Addr().(*net.TCPAddr).Port))
	t.Setenv("BEADS_DOLT_SERVER_SOCKET", socket)
	cfg := &configfile.Config{DoltServerHost: "127.0.0.1"}
	beadsDir := t.TempDir()
	for _, open := range []func(string, *configfile.Config) (*sql.DB, error){openFixDB, openServerCatalogDB} {
		db, err := open(beadsDir, cfg)
		if db != nil {
			_ = db.Close()
		}
		if err == nil {
			t.Fatal("repair/catalog accepted TCP beside preferred live socket")
		}
	}
	before := accepted.Load()
	db, err := openScanDB(beadsDir, cfg)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	if err := db.PingContext(ctx); err == nil {
		t.Fatal("fixture does not implement SQL")
	}
	if accepted.Load()-before < 2 {
		t.Fatal("scan probed the socket but did not perform SQL on it")
	}
	// With the socket removed, a live TCP endpoint becomes the selected transport.
	if err := ln.Close(); err != nil {
		t.Fatal(err)
	}
	db2, err := openFixDB(beadsDir, cfg)
	if err != nil {
		t.Fatalf("TCP fallback refused: %v", err)
	}
	_ = db2.Close()
}

func TestForkMetadataReconcileWithoutEndpoint(t *testing.T) {
	isolateDoltEnv(t)
	t.Setenv("BEADS_DOLT_AUTO_START", "true")
	t.Setenv("BEADS_DOLT_SERVER_MODE", "")
	beadsDir := t.TempDir()
	cfg := &configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeServer}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	if rows, err := inspectServerMetadataDatabases(beadsDir, cfg); err != nil || len(rows) != 0 {
		t.Fatalf("fresh owned workspace should await initialization: %v", err)
	}
	if err := os.MkdirAll(cfg.DatabasePath(beadsDir), 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := inspectServerMetadataDatabases(beadsDir, cfg); err == nil || !strings.Contains(err.Error(), "not running") {
		t.Fatalf("stopped owned workspace was not withheld: %v", err)
	}
	t.Setenv("BEADS_DOLT_SERVER_HOST", "::1")
	if _, err := openServerCatalogDB(beadsDir, cfg); err == nil || strings.Contains(err.Error(), "[::1]:0") || !strings.Contains(err.Error(), "no Dolt server port") {
		t.Fatalf("unmanaged unresolved endpoint dialed: %v", err)
	}
}
