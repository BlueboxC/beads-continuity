package main

import (
	"context"
	"errors"
	"fmt"
	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage"

	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"
)

// clonePortTestEnv clears every variable that names a Dolt server endpoint,
// its credentials or the mode, isolates HOME (the shared-server directory,
// user config and credentials file live there) and resets the config package,
// so only what a test writes chooses the endpoint.
func clonePortTestEnv(t *testing.T) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("HOME", home)
	t.Setenv("USERPROFILE", home)
	t.Setenv("XDG_CONFIG_HOME", filepath.Join(home, ".config"))
	t.Setenv("BEADS_SHARED_SERVER_DIR", filepath.Join(home, "shared-server"))
	for _, name := range []string{
		"BEADS_DOLT_SERVER_MODE", "BEADS_DOLT_SHARED_SERVER", "BEADS_DOLT_SERVER_HOST",
		"BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_PORT", "BEADS_DOLT_SERVER_SOCKET",
		"BEADS_DOLT_DATA_DIR", "BEADS_DOLT_SERVER_DATABASE", "BEADS_DOLT_SERVER_USER",
		"BEADS_DOLT_SERVER_TLS", "BEADS_DOLT_PASSWORD", "BEADS_CREDENTIALS_FILE",
		// Config reports these as dolt.port, dolt.shared-server, dolt.host and
		// dolt.mode.
		"BD_DOLT_PORT", "BD_DOLT_SHARED_SERVER", "BD_DOLT_HOST", "BD_DOLT_MODE",
	} {
		t.Setenv(name, "")
	}
	config.ResetForTesting()
	t.Cleanup(config.ResetForTesting)
}

// loadClonePortConfig makes beadsDir's config.yaml the one the store reads
// dolt.port from.
func loadClonePortConfig(t *testing.T, beadsDir string) {
	t.Helper()
	t.Setenv("BEADS_DIR", beadsDir)
	config.ResetForTesting()
	if err := config.Initialize(); err != nil {
		t.Fatalf("config.Initialize: %v", err)
	}
}

func writeClonePortFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
}

// writeClonePortCredentials points BEADS_CREDENTIALS_FILE at a credentials
// file with a section for every host and port pair, whose password is the
// pair itself ("host:port"), so a password names the server it is for.
func writeClonePortCredentials(t *testing.T, hosts []string, ports []int) {
	t.Helper()
	var body strings.Builder
	for _, host := range hosts {
		for _, port := range ports {
			fmt.Fprintf(&body, "[%s:%d]\npassword=%s:%d\n\n", host, port, host, port)
		}
	}
	path := filepath.Join(t.TempDir(), "credentials")
	writeClonePortFile(t, path, body.String())
	t.Setenv("BEADS_CREDENTIALS_FILE", path)
}

// TestServerClonePortIsTheStorePort pins the port bd bootstrap clones
// through on an external Dolt server to the one the store dials in that
// workspace once metadata.json holds the config bootstrap writes, so the clone
// lands where every later command looks: DialConfig's rule, for a server on
// another machine. A fresh clone writes metadata.json only after the clone,
// so its config's dolt_server_port must still take the file's rank.
func TestServerClonePortIsTheStorePort(t *testing.T) {
	const legacy, serverEnv, configured, metadata = 13307, 13400, 15555, 14444
	doltPort := func(t *testing.T, beadsDir string) {
		writeClonePortFile(t, filepath.Join(beadsDir, "config.yaml"), fmt.Sprintf("dolt.port: %d\n", configured))
	}
	listenerPort := func(t *testing.T, beadsDir string) {
		writeClonePortFile(t, filepath.Join(beadsDir, "dolt", "config.yaml"), fmt.Sprintf("listener:\n  port: %d\n", configured))
	}
	localPort := func(t *testing.T, beadsDir string) {
		writeClonePortFile(t, filepath.Join(beadsDir, "config.local.yaml"), fmt.Sprintf("dolt.port: %d\n", configured))
	}
	for _, tc := range []struct {
		name       string
		serverPort int // BEADS_DOLT_SERVER_PORT
		legacyPort int // BEADS_DOLT_PORT
		metaPort   int // dolt_server_port in the config bootstrap writes
		fresh      bool
		shared     bool // BEADS_DOLT_SHARED_SERVER=1
		setup      func(t *testing.T, beadsDir string)
		want       int
	}{
		{name: "config.yaml dolt.port over BEADS_DOLT_PORT", legacyPort: legacy, setup: doltPort, want: configured},
		{name: "listener.port over BEADS_DOLT_PORT", legacyPort: legacy, setup: listenerPort, want: configured},
		{name: "config.local.yaml dolt.port over BEADS_DOLT_PORT", legacyPort: legacy, setup: localPort, want: configured},
		{name: "config.local.yaml dolt.port alone", setup: localPort, want: configured},
		{name: "BEADS_DOLT_SERVER_PORT over dolt_server_port", serverPort: serverEnv, metaPort: metadata, want: serverEnv},
		{name: "dolt_server_port over BEADS_DOLT_PORT", legacyPort: legacy, metaPort: metadata, want: metadata},
		{name: "config.yaml dolt.port over dolt_server_port", metaPort: metadata, setup: doltPort, want: configured},
		{name: "BEADS_DOLT_PORT alone", legacyPort: legacy, want: legacy},
		{name: "nothing named", want: configfile.DefaultDoltServerPort},
		{name: "fresh clone/inherited dolt_server_port over BEADS_DOLT_PORT", fresh: true, legacyPort: legacy, metaPort: metadata, want: metadata},
		{name: "fresh clone/inherited dolt_server_port alone", fresh: true, metaPort: metadata, want: metadata},
		{name: "fresh clone/BEADS_DOLT_SERVER_PORT over inherited dolt_server_port", fresh: true, serverPort: serverEnv, metaPort: metadata, want: serverEnv},
		{name: "fresh clone/config.yaml dolt.port over inherited dolt_server_port", fresh: true, metaPort: metadata, setup: doltPort, want: configured},
		{name: "shared server/dolt_server_port not read", shared: true, metaPort: metadata, want: doltserver.DefaultSharedServerPort},
		{name: "shared server/fresh clone/inherited dolt_server_port not read", shared: true, fresh: true, metaPort: metadata, want: doltserver.DefaultSharedServerPort},
		{name: "shared server/BEADS_DOLT_PORT over the shared default", shared: true, legacyPort: legacy, metaPort: metadata, want: legacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clonePortTestEnv(t)
			if tc.shared {
				t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")
			}
			if tc.serverPort > 0 {
				t.Setenv("BEADS_DOLT_SERVER_PORT", strconv.Itoa(tc.serverPort))
			}
			if tc.legacyPort > 0 {
				t.Setenv("BEADS_DOLT_PORT", strconv.Itoa(tc.legacyPort))
			}
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			writeClonePortFile(t, filepath.Join(beadsDir, "config.yaml"), "# Beads Config\n")
			if tc.setup != nil {
				tc.setup(t, beadsDir)
			}
			loadClonePortConfig(t, beadsDir)
			cfg := &configfile.Config{
				Backend:        configfile.BackendDolt,
				DoltMode:       configfile.DoltModeServer,
				DoltServerHost: "db.example.com",
				DoltServerPort: tc.metaPort,
				DoltDatabase:   "clone_port",
			}
			if !tc.fresh {
				if err := cfg.Save(beadsDir); err != nil {
					t.Fatal(err)
				}
			}

			got := serverClonePort(beadsDir, cfg)

			if tc.fresh {
				// What finalizeSyncedBootstrap writes before the store opens.
				if err := cfg.Save(beadsDir); err != nil {
					t.Fatal(err)
				}
			}
			dial := doltserver.DialConfig(beadsDir)
			if got != tc.want {
				t.Errorf("serverClonePort = %d, want %d", got, tc.want)
			}
			if tc.want != configfile.DefaultDoltServerPort && got != dial.Port {
				t.Errorf("serverClonePort = %d, but the store dials %d (%s)", got, dial.Port, dial.PortSource)
			}
		})
	}
}

// TestCloneFromRemoteServerPortFollowsTheCaller pins which port each caller's
// server clone dials when config.yaml dolt.port and the legacy BEADS_DOLT_PORT
// disagree. bd bootstrap clones into the workspace, whose later commands
// connect on dolt.port. bd init --server --external opens its own store with
// BEADS_DOLT_PORT first, so its clone stays there.
func TestCloneFromRemoteServerPortFollowsTheCaller(t *testing.T) {
	clonePortTestEnv(t)
	configured := freeLoopbackPort(t)
	legacy := freeLoopbackPort(t)
	for legacy == configured {
		legacy = freeLoopbackPort(t)
	}
	t.Setenv("BEADS_DOLT_PORT", strconv.Itoa(legacy))
	beadsDir := filepath.Join(t.TempDir(), ".beads")
	writeClonePortFile(t, filepath.Join(beadsDir, "config.yaml"), fmt.Sprintf("dolt.port: %d\n", configured))
	loadClonePortConfig(t, beadsDir)
	cfg := &configfile.Config{
		Backend:        configfile.BackendDolt,
		DoltMode:       configfile.DoltModeServer,
		DoltServerHost: "127.0.0.1",
		DoltDatabase:   "clone_port",
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	const remote = "file:///nonexistent/remote"

	for _, tc := range []struct {
		name  string
		clone func() error
		want  int
	}{
		{name: "bd bootstrap", want: configured, clone: func() error {
			return cloneFromRemote(t.Context(), beadsDir, remote, "clone_port", cfg)
		}},
		{name: "bd init --server --external", want: legacy, clone: func() error {
			initCfg := initTimeCloneConfig(true, "", 0, "", "", "clone_port")
			return cloneFromRemoteWithMode(t.Context(), beadsDir, remote, "clone_port", initCfg, remoteCloneExternalServer)
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.clone()
			if err == nil {
				t.Fatal("clone succeeded with no server running")
			}
			want := "dolt server unreachable at " + net.JoinHostPort("127.0.0.1", strconv.Itoa(tc.want))
			if !strings.Contains(err.Error(), want) {
				t.Errorf("clone error = %v, want it to name %s", err, want)
			}
		})
	}
}

// TestServerClonePortIgnoresAnAncestorDoltPort covers a fresh rig with no
// .beads yet, inside a town but bounded by its own git repository (GH#3029,
// GH#2792): config loads the town's config.yaml, so DialConfig sees the
// town's dolt.port or dolt.shared-server. finalizeSyncedBootstrap then writes
// the rig's own config.yaml, which names neither, and later commands dial by
// the rig's own sources: that file, the user config files (the legacy
// ~/.beads/config.yaml among them) and the BD_* variables. The clone must
// land there too.
func TestServerClonePortIgnoresAnAncestorDoltPort(t *testing.T) {
	const legacy, townPort, metadata, bdPort, userPort, sharedPort = 13307, 15555, 14444, 16666, 15566, 17777
	userDoltPort := fmt.Sprintf("dolt.port: %d\n", userPort)
	userShared := "dolt.shared-server: true\n"
	for _, tc := range []struct {
		name       string
		committed  bool   // the rig commits metadata.json (defensive: the CLI pins BEADS_DIR to it)
		townShared bool   // the town's config.yaml sets dolt.shared-server instead of dolt.port
		plainTown  bool   // the town's config.yaml names no dolt key
		noMetaPort bool   // the rig's config names no dolt_server_port
		legacyPort int    // BEADS_DOLT_PORT
		bdDoltPort int    // BD_DOLT_PORT
		bdShared   bool   // BD_DOLT_SHARED_SERVER=1
		userLegacy string // the legacy ~/.beads/config.yaml
		sharedFile bool   // a running shared server's port file names sharedPort
		want       int
	}{
		{name: "fresh clone", want: metadata},
		{name: "committed metadata.json (defensive)", committed: true, want: metadata},
		{name: "fresh clone/inherited dolt_server_port over BEADS_DOLT_PORT", legacyPort: legacy, want: metadata},
		{name: "fresh clone/BEADS_DOLT_PORT without dolt_server_port", noMetaPort: true, legacyPort: legacy, want: legacy},
		{name: "fresh clone/BD_DOLT_PORT", bdDoltPort: bdPort, want: bdPort},
		{name: "fresh clone/BD_DOLT_PORT under a town shared server", townShared: true, sharedFile: true, bdDoltPort: bdPort, want: bdPort},
		{name: "fresh clone/town shared-server", townShared: true, want: metadata},
		{name: "fresh clone/BD_DOLT_SHARED_SERVER", bdShared: true, want: doltserver.DefaultSharedServerPort},
		{name: "fresh clone/legacy user dolt.port", plainTown: true, noMetaPort: true, userLegacy: userDoltPort, want: userPort},
		{name: "fresh clone/legacy user dolt.port over inherited dolt_server_port", plainTown: true, userLegacy: userDoltPort, want: userPort},
		{name: "fresh clone/legacy user dolt.port over the town's", userLegacy: userDoltPort, want: userPort},
		{name: "fresh clone/legacy user dolt.shared-server", plainTown: true, noMetaPort: true, userLegacy: userShared, want: doltserver.DefaultSharedServerPort},
		{name: "fresh clone/legacy user dolt.shared-server over inherited dolt_server_port", plainTown: true, userLegacy: userShared, want: doltserver.DefaultSharedServerPort},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clonePortTestEnv(t)
			t.Setenv("BEADS_CEILING_DIRECTORIES", "")
			if tc.userLegacy != "" {
				writeClonePortFile(t, filepath.Join(os.Getenv("HOME"), ".beads", "config.yaml"), tc.userLegacy)
			}
			if tc.sharedFile {
				writeClonePortFile(t, filepath.Join(os.Getenv("BEADS_SHARED_SERVER_DIR"), doltserver.PortFileName), strconv.Itoa(sharedPort))
			}
			if tc.bdShared {
				t.Setenv("BD_DOLT_SHARED_SERVER", "1")
			}
			if tc.legacyPort > 0 {
				t.Setenv("BEADS_DOLT_PORT", strconv.Itoa(tc.legacyPort))
			}
			if tc.bdDoltPort > 0 {
				t.Setenv("BD_DOLT_PORT", strconv.Itoa(tc.bdDoltPort))
			}
			town := t.TempDir()
			townConfig := fmt.Sprintf("dolt.port: %d\n", townPort)
			switch {
			case tc.townShared:
				townConfig = "dolt.shared-server: true\n"
			case tc.plainTown:
				townConfig = "issue-prefix: town\n"
			}
			writeClonePortFile(t, filepath.Join(town, ".beads", "config.yaml"), townConfig)
			rig := filepath.Join(town, "rig")
			beadsDir := filepath.Join(rig, ".beads")
			if err := os.MkdirAll(rig, 0o700); err != nil {
				t.Fatal(err)
			}
			cfg := &configfile.Config{
				Backend:        configfile.BackendDolt,
				DoltMode:       configfile.DoltModeServer,
				DoltServerHost: "db.example.com",
				DoltServerPort: metadata,
				DoltDatabase:   "rig_clone_port",
			}
			if tc.noMetaPort {
				cfg.DoltServerPort = 0
			}
			if tc.committed {
				if err := os.MkdirAll(beadsDir, 0o700); err != nil {
					t.Fatal(err)
				}
				if err := cfg.Save(beadsDir); err != nil {
					t.Fatal(err)
				}
			}
			t.Chdir(rig)
			t.Setenv("BEADS_DIR", "")
			config.ResetForTesting()
			if err := config.Initialize(); err != nil {
				t.Fatalf("config.Initialize: %v", err)
			}
			before := doltserver.DialConfig(beadsDir)
			switch {
			case tc.townShared && !before.PortSharedServer:
				t.Fatalf("precondition: DialConfig before bootstrap is not in shared-server mode (port %d, %s)", before.Port, before.PortSource)
			case !tc.townShared && !tc.plainTown && !tc.bdShared && tc.bdDoltPort == 0 && before.Port != townPort:
				t.Fatalf("precondition: DialConfig before bootstrap = %d (%s), want the town's dolt.port %d", before.Port, before.PortSource, townPort)
			}

			got := serverClonePort(beadsDir, cfg)

			// What finalizeSyncedBootstrap writes before the next command.
			if err := os.MkdirAll(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Save(beadsDir); err != nil {
				t.Fatal(err)
			}
			if err := createConfigYaml(beadsDir, false, ""); err != nil {
				t.Fatal(err)
			}
			config.ResetForTesting()
			if err := config.Initialize(); err != nil {
				t.Fatalf("config.Initialize: %v", err)
			}
			dial := doltserver.DialConfig(beadsDir)
			if got != tc.want {
				t.Errorf("serverClonePort = %d, want %d", got, tc.want)
			}
			if got != dial.Port {
				t.Errorf("serverClonePort = %d, but later commands in the rig dial %d (%s)", got, dial.Port, dial.PortSource)
			}
		})
	}
}

// TestServerClonePortReadsTheUserLayers covers a fresh clone with no .beads
// and no enclosing workspace, whose port or shared-server mode comes from a
// layer every later command reads through config: BD_DOLT_SHARED_SERVER, the
// documented user config, or the legacy ~/.beads/config.yaml (which the
// directory walk also loads as the project file for a repository under
// HOME). None of these is an enclosing workspace's config, so the clone port
// must stay the one the store dials.
func TestServerClonePortReadsTheUserLayers(t *testing.T) {
	const port, metadata = 15555, 14444
	userFile := func(rel, body string) func(t *testing.T, home string) {
		return func(t *testing.T, home string) {
			writeClonePortFile(t, filepath.Join(home, rel), body)
		}
	}
	legacyPort := userFile(filepath.Join(".beads", "config.yaml"), fmt.Sprintf("dolt.port: %d\n", port))
	legacyShared := userFile(filepath.Join(".beads", "config.yaml"), "dolt.shared-server: true\n")
	documentedPort := userFile(filepath.Join(".config", "bd", "config.yaml"), fmt.Sprintf("dolt.port: %d\n", port))
	documentedShared := userFile(filepath.Join(".config", "bd", "config.yaml"), "dolt.shared-server: true\n")
	bdShared := func(value string) func(t *testing.T, home string) {
		return func(t *testing.T, home string) { t.Setenv("BD_DOLT_SHARED_SERVER", value) }
	}
	shared := doltserver.DefaultSharedServerPort
	for _, tc := range []struct {
		name      string
		setup     []func(t *testing.T, home string)
		underHome bool // the repository lives under HOME
		localHost bool // 127.0.0.1 instead of a remote host
		metaPort  int  // dolt_server_port in the config bootstrap writes
		want      int
	}{
		{name: "BD_DOLT_SHARED_SERVER", setup: []func(*testing.T, string){bdShared("1")}, want: shared},
		{name: "BD_DOLT_SHARED_SERVER/local host", setup: []func(*testing.T, string){bdShared("1")}, localHost: true, want: shared},
		{name: "BD_DOLT_SHARED_SERVER=false over the user config", setup: []func(*testing.T, string){documentedShared, bdShared("false")}, want: configfile.DefaultDoltServerPort},
		{name: "legacy dolt.port", setup: []func(*testing.T, string){legacyPort}, want: port},
		{name: "legacy dolt.port over dolt_server_port", setup: []func(*testing.T, string){legacyPort}, metaPort: metadata, want: port},
		{name: "legacy dolt.shared-server", setup: []func(*testing.T, string){legacyShared}, want: shared},
		{name: "legacy dolt.shared-server/local host", setup: []func(*testing.T, string){legacyShared}, localHost: true, want: shared},
		{name: "repo under HOME/legacy dolt.port", setup: []func(*testing.T, string){legacyPort}, underHome: true, want: port},
		{name: "repo under HOME/legacy dolt.shared-server", setup: []func(*testing.T, string){legacyShared}, underHome: true, want: shared},
		{name: "user config dolt.port", setup: []func(*testing.T, string){documentedPort}, want: port},
		{name: "user config dolt.shared-server", setup: []func(*testing.T, string){documentedShared}, want: shared},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clonePortTestEnv(t)
			t.Setenv("BEADS_CEILING_DIRECTORIES", "")
			home := os.Getenv("HOME")
			for _, setup := range tc.setup {
				setup(t, home)
			}
			repo := filepath.Join(t.TempDir(), "repo")
			if tc.underHome {
				repo = filepath.Join(home, "code", "repo")
			}
			if err := os.MkdirAll(repo, 0o700); err != nil {
				t.Fatal(err)
			}
			beadsDir := filepath.Join(repo, ".beads")
			host := "db.example.com"
			if tc.localHost {
				host = "127.0.0.1"
			}
			cfg := &configfile.Config{
				Backend:        configfile.BackendDolt,
				DoltMode:       configfile.DoltModeServer,
				DoltServerHost: host,
				DoltServerPort: tc.metaPort,
				DoltDatabase:   "user_clone_port",
			}
			t.Chdir(repo)
			t.Setenv("BEADS_DIR", "")
			config.ResetForTesting()
			if err := config.Initialize(); err != nil {
				t.Fatalf("config.Initialize: %v", err)
			}

			got := serverClonePort(beadsDir, cfg)

			// What finalizeSyncedBootstrap writes before the next command,
			// which the CLI runs with BEADS_DIR pinned to it.
			if err := os.MkdirAll(beadsDir, 0o700); err != nil {
				t.Fatal(err)
			}
			if err := cfg.Save(beadsDir); err != nil {
				t.Fatal(err)
			}
			if err := createConfigYaml(beadsDir, false, ""); err != nil {
				t.Fatal(err)
			}
			loadClonePortConfig(t, beadsDir)
			dial := doltserver.DialConfig(beadsDir)
			if got != tc.want {
				t.Errorf("serverClonePort = %d, want %d", got, tc.want)
			}
			if tc.want != configfile.DefaultDoltServerPort && got != dial.Port {
				t.Errorf("serverClonePort = %d, but later commands dial %d (%s)", got, dial.Port, dial.PortSource)
			}
		})
	}
}

// TestDetectBootstrapActionProbesTheClonePort pins the existence probe that
// decides whether bd bootstrap clones at all, and the endpoint its "already
// exists" reason and its refusal name, to bootstrapServerEndpoint: the
// endpoint the clone dials, on the port every later command dials. The probe
// used to resolve its own port order, without listener.port or the
// shared-server BEADS_DOLT_PORT rule, so a workspace with a populated data
// directory could settle "already exists" against a server later commands
// never dial. It also took its password for another port and left out the
// socket, so with a per-port credentials entry it could not authenticate on
// the server the clone dials.
func TestDetectBootstrapActionProbesTheClonePort(t *testing.T) {
	const listener, metadata, legacy = 15555, 14444, 13307
	const socket = "/run/dolt/probe_port.sock"
	for _, tc := range []struct {
		name   string
		shared bool // BEADS_DOLT_SHARED_SERVER=1 with BEADS_DOLT_PORT, instead of listener.port
		exists bool // the probe finds the database
		want   int
	}{
		{name: "listener.port over dolt_server_port/exists", exists: true, want: listener},
		{name: "listener.port over dolt_server_port/refused", want: listener},
		{name: "shared server/BEADS_DOLT_PORT over the shared default/exists", shared: true, exists: true, want: legacy},
		{name: "shared server/BEADS_DOLT_PORT over the shared default/refused", shared: true, want: legacy},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clonePortTestEnv(t)
			repo := t.TempDir()
			t.Chdir(repo)
			beadsDir := filepath.Join(repo, ".beads")
			writeClonePortFile(t, filepath.Join(beadsDir, "config.yaml"), "# Beads Config\n")
			cfg := &configfile.Config{
				Backend:          configfile.BackendDolt,
				DoltMode:         configfile.DoltModeServer,
				DoltServerHost:   "127.0.0.1",
				DoltServerPort:   metadata,
				DoltServerSocket: socket,
				DoltDatabase:     "probe_port",
				ProjectID:        "probe-port-project",
			}
			if err := cfg.Save(beadsDir); err != nil {
				t.Fatal(err)
			}
			writeClonePortCredentials(t, []string{"127.0.0.1"}, []int{listener, metadata, legacy})
			// Each shape also populates the data directory the probe takes as
			// evidence that the database lives here.
			if tc.shared {
				t.Setenv("BEADS_DOLT_SHARED_SERVER", "1")
				t.Setenv("BEADS_DOLT_PORT", strconv.Itoa(legacy))
				writeClonePortFile(t, filepath.Join(os.Getenv("BEADS_SHARED_SERVER_DIR"), "dolt", "probe_port", "marker"), "")
			} else {
				writeClonePortFile(t, filepath.Join(beadsDir, "dolt", "config.yaml"), fmt.Sprintf("listener:\n  port: %d\n", listener))
			}
			loadClonePortConfig(t, beadsDir)
			var probed []bootstrapServerProbeConfig
			origCheck := checkBootstrapServerDB
			checkBootstrapServerDB = func(probeCfg bootstrapServerProbeConfig) bootstrapServerDBCheck {
				probed = append(probed, probeCfg)
				return bootstrapServerDBCheck{Exists: tc.exists, Reachable: true}
			}
			t.Cleanup(func() { checkBootstrapServerDB = origCheck })
			origDelay := bootstrapRetryDelay
			bootstrapRetryDelay = func(time.Duration) {}
			t.Cleanup(func() { bootstrapRetryDelay = origDelay })

			plan := detectBootstrapAction(beadsDir, cfg)

			if len(probed) == 0 {
				t.Fatalf("the server was never probed (plan %q: %s)", plan.Action, plan.Reason)
			}
			clone := bootstrapServerEndpoint(beadsDir, cfg)
			for _, ep := range probed {
				if ep.port != tc.want {
					t.Errorf("probe dialed port %d, want %d", ep.port, tc.want)
				}
				if want := fmt.Sprintf("127.0.0.1:%d", tc.want); ep.pass != want {
					t.Errorf("probe password = %q, want the credentials entry for the port it dials, %q", ep.pass, want)
				}
				if ep.socket != socket {
					t.Errorf("probe socket = %q, want the configured %q", ep.socket, socket)
				}
				if ep != clone {
					t.Errorf("probe dialed %+v, but the clone dials %+v", ep, clone)
				}
			}
			wantAction := "none"
			if !tc.exists && !plan.Blocked {
				t.Error("missing existing project database must be blocked")
			}
			if tc.exists {
				wantAction = "none"
			}
			if plan.Action != wantAction {
				t.Fatalf("action = %q (%s), want %q", plan.Action, plan.Reason, wantAction)
			}
			if endpoint := net.JoinHostPort("127.0.0.1", strconv.Itoa(tc.want)); !strings.Contains(plan.Reason, endpoint) {
				t.Errorf("reason = %q, want it to name %s", plan.Reason, endpoint)
			}
		})
	}
}

// TestInitServerClonePortIsInitsStorePort pins the port bd init --server
// --external clones through to the port of the store init opens right after
// the clone: --server-port, or initStoreServerPort's port, with the store's
// rule (dolt.ResolveServerPort) applied. In shared-server mode that store
// reads no config.yaml dolt.port, so neither does the clone.
func TestInitServerClonePortIsInitsStorePort(t *testing.T) {
	const configured, legacy, serverEnv, portFile, flag = 15555, 13307, 13400, 16000, 13500
	for _, tc := range []struct {
		name          string
		shared        bool // config.yaml dolt.shared-server
		doltPort      bool // config.yaml dolt.port
		serverPort    int  // --server-port
		serverEnvPort int  // BEADS_DOLT_SERVER_PORT
		legacyPort    int  // BEADS_DOLT_PORT
		portFile      bool // the workspace's port file
		want          int
	}{
		{name: "shared server/dolt.port not read", shared: true, doltPort: true, want: doltserver.DefaultSharedServerPort},
		{name: "shared server/BEADS_DOLT_PORT over the shared default", shared: true, doltPort: true, legacyPort: legacy, want: legacy},
		{name: "shared server/port file", shared: true, doltPort: true, portFile: true, want: portFile},
		{name: "shared server/BEADS_DOLT_SERVER_PORT", shared: true, doltPort: true, serverEnvPort: serverEnv, want: serverEnv},
		{name: "--server-port over BEADS_DOLT_SERVER_PORT", shared: true, serverPort: flag, serverEnvPort: serverEnv, want: flag},
		{name: "BEADS_DOLT_PORT over dolt.port", doltPort: true, legacyPort: legacy, want: legacy},
		{name: "BEADS_DOLT_PORT over the port file", portFile: true, legacyPort: legacy, want: legacy},
		{name: "dolt.port when nothing else names one", doltPort: true, want: configured},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clonePortTestEnv(t)
			if tc.serverEnvPort > 0 {
				t.Setenv("BEADS_DOLT_SERVER_PORT", strconv.Itoa(tc.serverEnvPort))
			}
			if tc.legacyPort > 0 {
				t.Setenv("BEADS_DOLT_PORT", strconv.Itoa(tc.legacyPort))
			}
			beadsDir := filepath.Join(t.TempDir(), ".beads")
			body := "# Beads Config\n"
			if tc.shared {
				body += "dolt.shared-server: true\n"
			}
			if tc.doltPort {
				body += fmt.Sprintf("dolt.port: %d\n", configured)
			}
			writeClonePortFile(t, filepath.Join(beadsDir, "config.yaml"), body)
			if tc.portFile {
				writeClonePortFile(t, filepath.Join(beadsDir, doltserver.PortFileName), strconv.Itoa(portFile))
			}
			loadClonePortConfig(t, beadsDir)

			got := initServerClonePort(beadsDir, initTimeCloneConfig(true, "", tc.serverPort, "", "", "init_clone_port"))

			if got != tc.want {
				t.Errorf("initServerClonePort = %d, want %d", got, tc.want)
			}
		})
	}
}

// TestWarmUpSyncedStoreOpensWhereTheCloneLanded covers the open that follows a
// fresh rig's clone, in the same process, still with the town's config.yaml
// loaded: it must dial the host and port the clone dialed, which later
// commands in the rig dial too, not the town's dolt.host and dolt.port. A
// warmup on the town's server misses the wisp tables and could open a
// same-named database there.
func TestWarmUpSyncedStoreOpensWhereTheCloneLanded(t *testing.T) {
	const townHost, townPort, metadata = "db.town.example", 15555, 14444
	clonePortTestEnv(t)
	t.Setenv("BEADS_CEILING_DIRECTORIES", "")
	town := t.TempDir()
	writeClonePortFile(t, filepath.Join(town, ".beads", "config.yaml"), fmt.Sprintf("dolt.host: %s\ndolt.port: %d\n", townHost, townPort))
	rig := filepath.Join(town, "rig")
	if err := os.MkdirAll(rig, 0o700); err != nil {
		t.Fatal(err)
	}
	beadsDir := filepath.Join(rig, ".beads")
	cfg := &configfile.Config{
		Backend:        configfile.BackendDolt,
		DoltMode:       configfile.DoltModeServer,
		DoltServerPort: metadata,
		DoltDatabase:   "rig_warmup",
	}
	t.Chdir(rig)
	t.Setenv("BEADS_DIR", "")
	config.ResetForTesting()
	if err := config.Initialize(); err != nil {
		t.Fatalf("config.Initialize: %v", err)
	}
	clone := bootstrapServerEndpoint(beadsDir, cfg)
	if clone.host != "127.0.0.1" || clone.port != metadata {
		t.Fatalf("precondition: the clone dials %s:%d, want the rig's 127.0.0.1 and the inherited dolt_server_port %d", clone.host, clone.port, metadata)
	}
	// What finalizeSyncedBootstrap writes before the warmup.
	if err := os.MkdirAll(beadsDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := cfg.Save(beadsDir); err != nil {
		t.Fatal(err)
	}
	if err := createConfigYaml(beadsDir, false, ""); err != nil {
		t.Fatal(err)
	}
	warmupHost, warmupPort := "", 0
	errStub := errors.New("stub store")
	origOpen := openSyncedWarmupStore
	openSyncedWarmupStore = func(_ context.Context, dir string) (storage.DoltStorage, error) {
		warmupHost, warmupPort = warmupStoreEndpoint(t, dir)
		return nil, errStub
	}
	t.Cleanup(func() { openSyncedWarmupStore = origOpen })

	if err := warmUpSyncedStore(t.Context(), BootstrapPlan{BeadsDir: beadsDir}); !errors.Is(err, errStub) {
		t.Fatalf("warmUpSyncedStore = %v, want the store open's error", err)
	}

	if warmupHost != clone.host || warmupPort != clone.port {
		t.Errorf("warmup dials %s:%d, but the clone dialed %s:%d", warmupHost, warmupPort, clone.host, clone.port)
	}
	if got := config.GetYamlConfig("dolt.port"); got != strconv.Itoa(townPort) {
		t.Errorf("after the warmup, the loaded dolt.port = %q, want the town's %d back", got, townPort)
	}
	if got := config.GetYamlConfig("dolt.host"); got != townHost {
		t.Errorf("after the warmup, the loaded dolt.host = %q, want the town's %q back", got, townHost)
	}
}

// warmupStoreEndpoint is the host and port of the store opened in dir under
// the configuration loaded now: metadata.json's host, as the store takes it
// (GetDoltServerHost), and DialConfig's port.
func warmupStoreEndpoint(t *testing.T, dir string) (string, int) {
	t.Helper()
	fileCfg, err := configfile.Load(dir)
	if err != nil || fileCfg == nil {
		t.Fatalf("no metadata.json in %s for the warmup to open: %v", dir, err)
	}
	return fileCfg.GetDoltServerHost(), doltserver.DialConfig(dir).Port
}

// TestSyncActionClonesWhereTheRigsCommandsLook runs bd bootstrap's sync action
// for a fresh rig with no .beads yet, inside a town but bounded by its own git
// repository (GH#3029, GH#2792), whose config.yaml names a Dolt server or
// mode. Config loads the town's config.yaml, but the rig's later commands read
// only the rig's own configuration and the user config files. Whether the
// clone goes to a server or the embedded engine, the server it dials and the
// password it presents there, the dolt_mode finalizeSyncedBootstrap records,
// and the server the warmup opens must all be the ones those commands use, and
// the existence probe (bootstrapServerEndpoint) must look where the clone
// lands.
func TestSyncActionClonesWhereTheRigsCommandsLook(t *testing.T) {
	const townHost, userHost, townPort, metadata = "db.town.example", "db.user.example", 15555, 14444
	townShared := "dolt.shared-server: true\n"
	townServer := fmt.Sprintf("dolt.host: %s\ndolt.port: %d\n", townHost, townPort)
	server := func(port int) *configfile.Config {
		return &configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeServer, DoltServerPort: port}
	}
	embedded := &configfile.Config{Backend: configfile.BackendDolt, DoltMode: configfile.DoltModeEmbedded}
	for _, tc := range []struct {
		name     string
		town     string // the town's config.yaml
		townKey  string // a key of it that config loads before the clone
		userHost string // dolt.host in the user config
		cfg      *configfile.Config
		// wantHost and wantPort name the server the clone dials; empty for
		// a clone into the embedded engine.
		wantHost string
		wantPort int
	}{
		{name: "town shared-server/default config", town: townShared, townKey: "dolt.shared-server", cfg: configfile.DefaultConfig()},
		{name: "town shared-server/embedded config", town: townShared, townKey: "dolt.shared-server", cfg: embedded},
		// Nothing the rig reads names a port, so the clone dials the default,
		// as for any workspace whose settings name none, and not the town's
		// shared server (3308).
		{name: "town shared-server/server config naming no port", town: townShared, townKey: "dolt.shared-server", cfg: server(0), wantHost: "127.0.0.1", wantPort: configfile.DefaultDoltServerPort},
		{name: "town dolt.mode server/default config", town: "dolt.mode: server\n", townKey: "dolt.mode", cfg: configfile.DefaultConfig()},
		{name: "town dolt.host/server config naming no host or port", town: townServer, townKey: "dolt.host", cfg: server(0), wantHost: "127.0.0.1", wantPort: configfile.DefaultDoltServerPort},
		{name: "town dolt.host/inherited dolt_server_port", town: townServer, townKey: "dolt.host", cfg: server(metadata), wantHost: "127.0.0.1", wantPort: metadata},
		{name: "town dolt.host/user dolt.host", town: townServer, townKey: "dolt.host", userHost: userHost, cfg: server(metadata), wantHost: userHost, wantPort: metadata},
	} {
		t.Run(tc.name, func(t *testing.T) {
			clonePortTestEnv(t)
			t.Setenv("BEADS_CEILING_DIRECTORIES", "")
			if tc.userHost != "" {
				writeClonePortFile(t, filepath.Join(os.Getenv("HOME"), ".config", "bd", "config.yaml"), "dolt.host: "+tc.userHost+"\n")
			}
			writeClonePortCredentials(t, []string{"127.0.0.1", townHost, userHost}, []int{metadata, townPort, configfile.DefaultDoltServerPort})
			town := t.TempDir()
			writeClonePortFile(t, filepath.Join(town, ".beads", "config.yaml"), tc.town)
			rig := filepath.Join(town, "rig")
			if err := os.MkdirAll(rig, 0o700); err != nil {
				t.Fatal(err)
			}
			beadsDir := filepath.Join(rig, ".beads")
			cfg := *tc.cfg
			cfg.DoltDatabase = "rig_sync"
			t.Chdir(rig)
			t.Setenv("BEADS_DIR", "")
			config.ResetForTesting()
			if err := config.Initialize(); err != nil {
				t.Fatalf("config.Initialize: %v", err)
			}
			if config.GetYamlConfig(tc.townKey) == "" {
				t.Fatalf("precondition: bootstrap starts without the town's %s loaded", tc.townKey)
			}
			probe := bootstrapServerEndpoint(beadsDir, &cfg)

			var cloned *bootstrapServerProbeConfig
			clonedEmbedded := false
			origServer, origEmbedded, origOpen := remoteCloneViaServer, remoteCloneViaEmbedded, openSyncedWarmupStore
			t.Cleanup(func() {
				remoteCloneViaServer, remoteCloneViaEmbedded, openSyncedWarmupStore = origServer, origEmbedded, origOpen
			})
			remoteCloneViaServer = func(_ context.Context, _, _ string, ep bootstrapServerProbeConfig) error {
				cloned = &ep
				return nil
			}
			remoteCloneViaEmbedded = func(context.Context, string, string, string) error {
				clonedEmbedded = true
				return nil
			}
			warmupHost, warmupPort := "", 0
			openSyncedWarmupStore = func(_ context.Context, dir string) (storage.DoltStorage, error) {
				warmupHost, warmupPort = warmupStoreEndpoint(t, dir)
				return nil, errors.New("stub store")
			}

			plan := BootstrapPlan{Action: "sync", BeadsDir: beadsDir, Database: "rig_sync", SyncRemote: "file:///nonexistent/remote"}
			if err := executeSyncAction(t.Context(), plan, &cfg); err != nil {
				t.Fatalf("executeSyncAction: %v", err)
			}

			// The rig's next command, which the CLI runs with BEADS_DIR pinned
			// to it.
			loadClonePortConfig(t, beadsDir)
			later, err := configfile.Load(beadsDir)
			if err != nil || later == nil {
				t.Fatalf("finalize wrote no metadata.json: %v", err)
			}
			wantServer := tc.wantHost != ""
			if laterServer := (resolveRemoteCloneMode(beadsDir, later, remoteCloneAuto) == remoteCloneExternalServer); laterServer != wantServer {
				t.Errorf("later commands use a server: %v (dolt_mode %q), want %v", laterServer, later.DoltMode, wantServer)
			}
			if !wantServer {
				if cloned != nil {
					t.Errorf("cloned through a server at %s:%d, want a clone into the embedded engine", cloned.host, cloned.port)
				}
				if !clonedEmbedded {
					t.Error("no clone into the embedded engine")
				}
				return
			}
			if cloned == nil {
				t.Fatalf("no server clone (embedded: %v), want one through %s:%d", clonedEmbedded, tc.wantHost, tc.wantPort)
			}
			laterHost, laterPort := later.GetDoltServerHost(), doltserver.DialConfig(beadsDir).Port
			if cloned.host != tc.wantHost || cloned.port != tc.wantPort {
				t.Errorf("clone dialed %s:%d, want %s:%d", cloned.host, cloned.port, tc.wantHost, tc.wantPort)
			}
			if cloned.host != laterHost || (laterPort > 0 && cloned.port != laterPort) {
				t.Errorf("clone dialed %s:%d, but later commands dial %s:%d", cloned.host, cloned.port, laterHost, laterPort)
			}
			if want := later.GetDoltServerPasswordForPort(cloned.port); want == "" || cloned.pass != want {
				t.Errorf("clone password = %q, want %q, the one later commands present there", cloned.pass, want)
			}
			if *cloned != probe {
				t.Errorf("the existence probe dials %+v, but the clone dialed %+v", probe, *cloned)
			}
			if warmupHost != laterHost || warmupPort != laterPort {
				t.Errorf("warmup dials %s:%d, but later commands dial %s:%d", warmupHost, warmupPort, laterHost, laterPort)
			}
		})
	}
}
