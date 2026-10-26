package dolt

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	sqlmock "github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/testutil"
)

// clearServerPortEnv blanks BEADS_DOLT_SERVER_PORT and BEADS_DOLT_PORT for
// the rest of the test. With either set, no Dolt database in a store's CLIDir
// counts as its server's (cliDirServesServer), and in the Dolt lane the
// package's TestMain exports both for the suite's own server, so a test of
// the CLI routes for a server bd runs clears them first.
func clearServerPortEnv(t *testing.T) {
	t.Helper()
	t.Setenv("BEADS_DOLT_SERVER_PORT", "")
	t.Setenv("BEADS_DOLT_PORT", "")
}

func TestForkLeftoverRemotesRemainMigrationEvidenceOnly(t *testing.T) {
	clearPredicateEnv(t)
	s := &DoltStore{serverMode: true, dbPath: t.TempDir(), database: "active", cfg: &Config{ServerHost: "db.example.com"}}
	dir := filepath.Join(s.CLIDir(), ".dolt")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "repo_state.json"), []byte(`{"remotes":{"origin":{"name":"origin","url":"file:///unused"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if s.hasCLIDatabase() || s.HasPersistedRemote() || len(s.PersistedRemoteInfos()) != 0 {
		t.Fatal("leftover clone became authoritative for an external server")
	}
	if !s.hasPersistedCLIRemote() {
		t.Fatal("migration guard lost persisted remote evidence")
	}
}

// leftoverRemoteRows lists, as the server's dolt_remotes does, the remotes
// the CLI-route tests below route: a git-protocol default remote, a
// git-protocol federation peer, and an Azure remote for the cloud-auth route.
func leftoverRemoteRows(gitURL, azURL string) *sqlmock.Rows {
	return sqlmock.NewRows([]string{"name", "url"}).
		AddRow("origin", gitURL).
		AddRow("peer", gitURL).
		AddRow("cloud", azURL)
}

// TestCLIRoutesUseALeftoverDatabaseOnlyWhereBdRunsTheServer pins every CLI
// route of push, pull and fetch (default remote and federation peer) to the
// server bd may run. CLIDir holds a Dolt database whose remotes match the
// server's, as a clone bd init left in .beads/dolt/<db> does. That database
// is the server's only for a plain TCP server where bd runs its server
// (configfile.BdRunsServerAt) and the environment names no port, or names
// the port bd's state files show its own live server for the workspace on;
// there every route keeps taking the dolt CLI. At any other host, for a
// gateway, a TLS endpoint or a unix socket, or for another port
// BEADS_DOLT_SERVER_PORT or BEADS_DOLT_PORT names, it is at best a stale
// copy, and pushing it would report success without the server's commits,
// so every route falls back to SQL, which the server runs on its own data.
func TestCLIRoutesUseALeftoverDatabaseOnlyWhereBdRunsTheServer(t *testing.T) {
	testutil.RequireDoltBinary(t)
	clearPredicateEnv(t)
	clearCloudAuthEnv(t)
	t.Setenv("AZURE_STORAGE_ACCOUNT", "account")
	ctx := context.Background()
	creds := &remoteCredentials{username: "user", password: "pass"}
	const gitURL = "git+ssh://git@example.com/org/repo.git"
	const azURL = "az://account.blob.core.windows.net/container"

	routes := []struct {
		name  string
		route func(*DoltStore) (bool, error)
	}{
		{"git protocol", func(s *DoltStore) (bool, error) { return s.prepareCLIRouteForGitProtocol(ctx, "origin") }},
		{"credential remote", func(s *DoltStore) (bool, error) { return s.prepareCLIRouteForCredentials(ctx, "origin", creds) }},
		{"cloud auth remote", func(s *DoltStore) (bool, error) { return s.prepareCLIRouteForCloudAuth(ctx, "cloud") }},
		{"local remote", func(s *DoltStore) (bool, error) { return s.shouldUseCLIForLocalRemoteWithError(ctx, "origin") }},
		{"peer git protocol", func(s *DoltStore) (bool, error) { return s.prepareCLIRouteForPeerGitProtocol(ctx, "peer") }},
		{"peer credential remote", func(s *DoltStore) (bool, error) { return s.prepareCLIRouteForPeerCredentials(ctx, "peer", creds) }},
	}

	for _, tc := range []struct {
		name    string
		cfg     Config
		portVar string
		// bdServerOnPort, when set, is the port bd's state files for the
		// workspace show its own live server on.
		bdServerOnPort int
		wantCLI        bool
	}{
		{name: "host=127.0.0.1", cfg: Config{ServerHost: "127.0.0.1"}, wantCLI: true},
		{name: "host=127.0.0.1/BEADS_DOLT_SERVER_PORT", cfg: Config{ServerHost: "127.0.0.1", ServerPort: 3310}, portVar: "BEADS_DOLT_SERVER_PORT"},
		{name: "host=127.0.0.1/BEADS_DOLT_PORT", cfg: Config{ServerHost: "127.0.0.1", ServerPort: 3310}, portVar: "BEADS_DOLT_PORT"},
		{name: "host=127.0.0.1/BEADS_DOLT_SERVER_PORT/bd server on that port", cfg: Config{ServerHost: "127.0.0.1", ServerPort: 3310}, portVar: "BEADS_DOLT_SERVER_PORT", bdServerOnPort: 3310, wantCLI: true},
		{name: "host=127.0.0.1/BEADS_DOLT_PORT/bd server on that port", cfg: Config{ServerHost: "127.0.0.1", ServerPort: 3310}, portVar: "BEADS_DOLT_PORT", bdServerOnPort: 3310, wantCLI: true},
		{name: "host=127.0.0.1/BEADS_DOLT_SERVER_PORT/bd server on another port", cfg: Config{ServerHost: "127.0.0.1", ServerPort: 3310}, portVar: "BEADS_DOLT_SERVER_PORT", bdServerOnPort: 3311},
		{name: "config-only port", cfg: Config{ServerHost: "127.0.0.1", ServerPort: 3310}},
		{name: "config-only owned port", cfg: Config{ServerHost: "127.0.0.1", ServerPort: 3310}, bdServerOnPort: 3310, wantCLI: true},
		{name: "config-only different port", cfg: Config{ServerHost: "127.0.0.1", ServerPort: 3310}, bdServerOnPort: 3311},
		{name: "proxied-server", cfg: Config{ServerHost: "127.0.0.1", ProxiedServer: true}},
		{name: "host=127.0.0.1/gateway", cfg: Config{ServerHost: "127.0.0.1", Gateway: true}},
		{name: "host=127.0.0.1/TLS", cfg: Config{ServerHost: "127.0.0.1", ServerTLS: true}},
		{name: "host=127.0.0.1/unix socket", cfg: Config{ServerHost: "127.0.0.1", ServerSocket: "/tmp/dolt.sock"}},
		{name: "host=::1", cfg: Config{ServerHost: "::1"}},
		{name: "host=127.0.0.2", cfg: Config{ServerHost: "127.0.0.2"}},
		{name: "host=db.example.com", cfg: Config{ServerHost: "db.example.com"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.portVar != "" {
				t.Setenv(tc.portVar, "3310")
			}
			beadsDir := unmanagedWorkspace(t)
			if tc.bdServerOnPort > 0 {
				beadsDir = managedWorkspace(t, tc.bdServerOnPort)
			}
			dbPath := filepath.Join(beadsDir, "dolt")
			cliDir := filepath.Join(dbPath, "beads")
			initLocalDoltRepoForRemote(t, cliDir)
			for name, url := range map[string]string{"origin": gitURL, "peer": gitURL, "cloud": azURL} {
				if err := doltutil.AddCLIRemote(cliDir, name, url); err != nil {
					t.Fatalf("AddCLIRemote(%s): %v", name, err)
				}
			}

			for _, tt := range routes {
				t.Run(tt.name, func(t *testing.T) {
					db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
					if err != nil {
						t.Fatalf("sqlmock: %v", err)
					}
					t.Cleanup(func() { _ = db.Close() })
					mock.MatchExpectationsInOrder(false)
					for i := 0; i < 3; i++ {
						mock.ExpectQuery("SELECT name, url FROM dolt_remotes").
							WillReturnRows(leftoverRemoteRows(gitURL, azURL))
					}
					cfg := tc.cfg
					store := &DoltStore{
						serverMode: true,
						beadsDir:   beadsDir,
						dbPath:     dbPath,
						database:   "beads",
						remote:     "origin",
						db:         db,
						cfg:        &cfg,
					}

					useCLI, err := tt.route(store)
					if err != nil {
						t.Fatalf("route returned error: %v", err)
					}
					if useCLI != tc.wantCLI {
						t.Fatalf("route through the dolt CLI in %s = %v for %s, want %v (only bd's own server serves that directory)", cliDir, useCLI, tc.name, tc.wantCLI)
					}
				})
			}
		})
	}
}

// fakeDoltOnPath puts a dolt on PATH that appends "<dir>|<args>|<the
// DOLT_REMOTE_PASSWORD it got>" to the returned log for every call, and
// answers `dolt remote -v` with origin at remoteURL.
func fakeDoltOnPath(t *testing.T, remoteURL string) string {
	t.Helper()
	binDir := t.TempDir()
	callLog := filepath.Join(binDir, "calls.log")
	script := fmt.Sprintf("#!/bin/sh\necho \"$(pwd -P)|$*|DOLT_REMOTE_PASSWORD=${DOLT_REMOTE_PASSWORD:-}\" >> '%s'\nif [ \"$1 $2\" = \"remote -v\" ]; then echo 'origin %s'; fi\nexit 0\n", callLog, remoteURL)
	if err := os.WriteFile(filepath.Join(binDir, "dolt"), []byte(script), 0o755); err != nil { // #nosec G306 -- test fixture must be executable
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	return callLog
}

// TestPushAtAHostBdDoesNotRunRunsNoDoltInTheLeftoverDatabase is the push end
// of that gate, with a dolt on PATH that records every call. A server-mode
// store at the IPv6 loopback whose .beads/dolt/<db> holds a Dolt database
// with the git-protocol remote runs no dolt there (no fsck, no push, no
// remote listing), forced or not, and pushes with CALL DOLT_PUSH on the
// server instead. So does the same store at 127.0.0.1 for a gateway, or when
// BEADS_DOLT_SERVER_PORT or BEADS_DOLT_PORT names its port, unless that is
// the port bd's state files show its own live server for the workspace on.
// Otherwise the store at 127.0.0.1 keeps fsck-ing and pushing that directory
// through the dolt CLI.
func TestPushAtAHostBdDoesNotRunRunsNoDoltInTheLeftoverDatabase(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake")
	}
	clearPredicateEnv(t)
	const gitURL = "git+ssh://git@example.com/org/repo.git"
	callLog := fakeDoltOnPath(t, gitURL)

	for _, tc := range []struct {
		host           string
		portVar        string
		gateway        bool
		bdServerOnPort bool
		force          bool
		wantCLI        bool
	}{
		{host: "127.0.0.1", wantCLI: true},
		{host: "127.0.0.1", force: true, wantCLI: true},
		{host: "127.0.0.1", portVar: "BEADS_DOLT_SERVER_PORT"},
		{host: "127.0.0.1", portVar: "BEADS_DOLT_PORT", force: true},
		{host: "127.0.0.1", portVar: "BEADS_DOLT_SERVER_PORT", bdServerOnPort: true, wantCLI: true},
		{host: "127.0.0.1", gateway: true},
		{host: "::1"},
		{host: "::1", force: true},
	} {
		name := fmt.Sprintf("host=%s/force=%v", tc.host, tc.force)
		if tc.portVar != "" {
			name += "/" + tc.portVar
		}
		if tc.bdServerOnPort || tc.wantCLI {
			name += "/bd server on that port"
		}
		if tc.gateway {
			name += "/gateway"
		}
		t.Run(name, func(t *testing.T) {
			if tc.portVar != "" {
				t.Setenv(tc.portVar, "3310")
			}
			beadsDir := unmanagedWorkspace(t)
			if tc.bdServerOnPort || tc.wantCLI {
				beadsDir = managedWorkspace(t, 3310)
			}
			_ = os.Remove(callLog)
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			mock.MatchExpectationsInOrder(false)
			for i := 0; i < 8; i++ {
				mock.ExpectQuery("SELECT name, url FROM dolt_remotes").
					WillReturnRows(sqlmock.NewRows([]string{"name", "url"}).AddRow("origin", gitURL))
			}
			store := &DoltStore{
				serverMode: true,
				beadsDir:   beadsDir,
				dbPath:     filepath.Join(beadsDir, "dolt"),
				database:   "beads",
				remote:     "origin",
				branch:     "main",
				db:         db,
				cfg:        &Config{ServerHost: tc.host, ServerPort: 3310, Gateway: tc.gateway},
				// The SQL route's own connection fails at once, which is how
				// this test tells it was taken.
				connStr: unroutableConnStr,
			}
			if err := os.MkdirAll(filepath.Join(store.CLIDir(), ".dolt", "noms"), 0o755); err != nil {
				t.Fatal(err)
			}

			push := store.Push
			if tc.force {
				push = store.ForcePush
			}
			pushErr := push(context.Background())

			raw, err := os.ReadFile(callLog)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			calls := strings.TrimSpace(string(raw))
			if !tc.wantCLI {
				if calls != "" {
					t.Fatalf("push (%s) ran dolt in the leftover database:\n%s", name, calls)
				}
				if pushErr == nil || !strings.Contains(pushErr.Error(), "push to origin/main") {
					t.Fatalf("push (%s) = %v, want the SQL route's CALL DOLT_PUSH failure", name, pushErr)
				}
				return
			}
			if pushErr != nil {
				t.Fatalf("push (%s): %v", name, pushErr)
			}
			cliDir, err := filepath.EvalSymlinks(store.CLIDir())
			if err != nil {
				t.Fatal(err)
			}
			want := cliDir + "|push origin main"
			if tc.force {
				want = cliDir + "|push --force origin main"
			}
			for _, w := range []string{cliDir + "|fsck --quiet", want} {
				if !strings.Contains(calls, w) {
					t.Fatalf("push (%s): dolt calls %q lack %q", name, calls, w)
				}
			}
		})
	}
}

// TestCredentialPushOnAnEnvNamedPortKeepsTheCLIOnlyForBdsOwnServer pins the
// credential route of a push to a Hosted Dolt remote whose user and
// DOLT_REMOTE_PASSWORD only this bd holds, from a store at 127.0.0.1 whose
// port BEADS_DOLT_SERVER_PORT names. Where bd's state files show its own live
// server for the workspace on that port, .beads/dolt/<db> is that server's
// database: the push stays on the dolt CLI there, and the dolt child gets the
// password. Otherwise the port may be any server's, so the push runs CALL
// DOLT_PUSH on it, which takes the password from the server process's own
// environment, and its failure says so.
func TestCredentialPushOnAnEnvNamedPortKeepsTheCLIOnlyForBdsOwnServer(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX shell fake")
	}
	clearPredicateEnv(t)
	clearCloudAuthEnv(t)
	const hostedURL = "https://doltremoteapi.dolthub.com/org/repo"
	callLog := fakeDoltOnPath(t, hostedURL)
	t.Setenv("BEADS_DOLT_SERVER_PORT", "3310")

	for _, bdServerOnPort := range []bool{true, false} {
		t.Run(fmt.Sprintf("bd server on that port=%v", bdServerOnPort), func(t *testing.T) {
			beadsDir := unmanagedWorkspace(t)
			if bdServerOnPort {
				beadsDir = managedWorkspace(t, 3310)
			}
			_ = os.Remove(callLog)
			db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
			if err != nil {
				t.Fatalf("sqlmock: %v", err)
			}
			t.Cleanup(func() { _ = db.Close() })
			mock.MatchExpectationsInOrder(false)
			for i := 0; i < 8; i++ {
				mock.ExpectQuery("SELECT name, url FROM dolt_remotes").
					WillReturnRows(sqlmock.NewRows([]string{"name", "url"}).AddRow("origin", hostedURL))
			}
			store := &DoltStore{
				serverMode:     true,
				beadsDir:       beadsDir,
				dbPath:         filepath.Join(beadsDir, "dolt"),
				database:       "beads",
				remote:         "origin",
				branch:         "main",
				remoteUser:     "user",
				remotePassword: "client-secret",
				db:             db,
				cfg:            &Config{ServerHost: "127.0.0.1", ServerPort: 3310, BeadsDir: beadsDir},
				connStr:        unroutableConnStr,
			}
			if err := os.MkdirAll(filepath.Join(store.CLIDir(), ".dolt", "noms"), 0o755); err != nil {
				t.Fatal(err)
			}

			pushErr := store.Push(context.Background())

			raw, err := os.ReadFile(callLog)
			if err != nil && !os.IsNotExist(err) {
				t.Fatal(err)
			}
			calls := strings.TrimSpace(string(raw))
			if !bdServerOnPort {
				if calls != "" {
					t.Fatalf("push to a server the variable names ran dolt in the leftover database:\n%s", calls)
				}
				const note = "CALL DOLT_PUSH runs in the Dolt server's process, which takes DOLT_REMOTE_PASSWORD from its own environment, not from bd's"
				if pushErr == nil || !strings.Contains(pushErr.Error(), "push to origin/main") || !strings.Contains(pushErr.Error(), note) {
					t.Fatalf("push = %v, want the SQL route's CALL DOLT_PUSH failure naming where the server takes the password from (%q)", pushErr, note)
				}
				return
			}
			if pushErr != nil {
				t.Fatalf("push to bd's own server: %v", pushErr)
			}
			cliDir, err := filepath.EvalSymlinks(store.CLIDir())
			if err != nil {
				t.Fatal(err)
			}
			if want := cliDir + "|push origin main|DOLT_REMOTE_PASSWORD=client-secret"; !strings.Contains(calls, want) {
				t.Fatalf("dolt calls %q lack the credential push %q", calls, want)
			}
		})
	}
}
