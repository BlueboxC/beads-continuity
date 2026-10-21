package doltserver

import (
	"errors"
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"

	"github.com/steveyegge/beads/internal/configfile"
)

// ServerMode describes who owns and manages the dolt sql-server lifecycle.
type ServerMode int

const (
	// ServerModeOwned means beads auto-starts and manages the server.
	// This is the default for standalone users with no explicit port config.
	ServerModeOwned ServerMode = iota

	// ServerModeExternal means the user manages the server lifecycle
	// (e.g., systemd, Docker, Hosted Dolt, VPS). Beads never starts or
	// stops the server. Determined when metadata.json has an explicit
	// dolt_server_port or BEADS_DOLT_SHARED_SERVER is set.
	ServerModeExternal

	// ServerModeEmbedded is the legacy in-process embedded dolt path.
	// Determined when metadata.json dolt_mode is "embedded".
	ServerModeEmbedded
)

// String returns a human-readable label for the server mode.
func (m ServerMode) String() string {
	switch m {
	case ServerModeOwned:
		return "owned"
	case ServerModeExternal:
		return "external"
	case ServerModeEmbedded:
		return "embedded"
	default:
		return fmt.Sprintf("ServerMode(%d)", int(m))
	}
}

// ResolveServerMode determines the server mode from the given beadsDir.
// This is the single source of truth for how the server lifecycle is managed.
//
// Decision logic (checked in order):
//  1. BEADS_DOLT_SERVER_MODE=1 env var             -> ServerModeExternal
//  2. BEADS_DOLT_SHARED_SERVER env var is set       -> ServerModeExternal
//     2b. host-based inference (HostImpliesServerMode) -> ServerModeExternal
//  3. metadata.json dolt_mode == "embedded"         -> ServerModeEmbedded
//  4. metadata.json has explicit dolt_server_port   -> ServerModeExternal
//  5. default                                       -> ServerModeOwned
//
// Runtime env vars (1, 2) take precedence over persisted metadata.json
// to prevent stale dolt_mode=embedded from silently overriding an active
// shared-server or server-mode configuration (GH#2949).
//
// The function loads metadata.json only if the file exists, to avoid
// triggering the legacy config.json -> metadata.json migration side effect.
func ResolveServerMode(beadsDir string) ServerMode {
	// 1. BEADS_DOLT_SERVER_MODE=1 env var -> external (explicit server mode)
	if os.Getenv("BEADS_DOLT_SERVER_MODE") == "1" {
		return ServerModeExternal
	}

	// 2. Shared server mode (env var or config.yaml) -> external.
	// Must be checked before metadata.json so that a stale
	// dolt_mode=embedded cannot override active shared-server intent.
	if IsSharedServerMode() {
		return ServerModeExternal
	}

	var fileCfg *configfile.Config

	// Only load config if metadata.json exists (avoids legacy migration side effect)
	metadataPath := configfile.ConfigPath(beadsDir)
	if _, err := os.Stat(metadataPath); err == nil {
		if cfg, loadErr := configfile.Load(beadsDir); loadErr == nil && cfg != nil {
			fileCfg = cfg
		}
	}

	// 2b. Host-based inference (GH#3545) -> external. Uses the same
	// centralized rule as IsDoltServerMode (explicit-mode gates, env
	// suppression, effective-host precedence, proxied exemption) so the
	// storage-mode and lifecycle resolvers cannot disagree: a server
	// that lives on another machine cannot have a bd-owned lifecycle.
	// Runs before the embedded-metadata check because a runtime env
	// host beats stale metadata (GH#2949).
	hostCfg := fileCfg
	if hostCfg == nil {
		hostCfg = &configfile.Config{}
	}
	if hostCfg.HostImpliesServerMode() {
		return ServerModeExternal
	}

	// Explicit runtime ports name an externally managed endpoint. This changes
	// lifecycle ownership, not the embedded/proxy storage selector.
	if !strings.EqualFold(hostCfg.DoltMode, configfile.DoltModeProxiedServer) {
		for _, name := range []string{"BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_PORT"} {
			if port, err := strconv.Atoi(os.Getenv(name)); err == nil && port > 0 && !ManagesLiveServerOnPort(beadsDir, port) {
				return ServerModeExternal
			}
		}
	}

	// 3. Explicit embedded mode in metadata.json
	if fileCfg != nil && strings.ToLower(fileCfg.DoltMode) == configfile.DoltModeEmbedded &&
		fileCfg.DoltMode != "" { // empty defaults to embedded in GetDoltMode, but we treat empty as "unset"
		return ServerModeEmbedded
	}

	// 4. Explicit server port in metadata.json -> external
	if fileCfg != nil && fileCfg.DoltServerPort > 0 {
		return ServerModeExternal
	}

	// 5. Default: beads owns the server
	return ServerModeOwned
}

// ErrExternallyManaged matches EnsureRunning's refusal to start a server for a
// workspace whose server is externally managed (ServerModeExternal). The
// refusal is not a failed start: bd never tried, so a caller that reports it
// as one ("auto-start failed") misdescribes it.
var ErrExternallyManaged = errors.New("dolt server is externally managed; bd does not start it")

// externallyManagedError is EnsureRunning's refusal: its own text, matching
// ErrExternallyManaged.
type externallyManagedError struct{ msg string }

func (e *externallyManagedError) Error() string        { return e.msg }
func (e *externallyManagedError) Is(target error) bool { return target == ErrExternallyManaged }

// ExternalPortDeclaration is the setting that declares a workspace's Dolt
// server externally managed (ServerModeExternal) by naming a port for it, as
// found by ExternalPortSetting. The zero value means no such setting does.
type ExternalPortDeclaration struct {
	// Setting names the declaring setting for the operator:
	// "BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_PORT", or
	// "dolt_server_port in <metadata.json path>".
	Setting string
	// NamesEndpoint reports whether Setting's value is the port bd dialed. It
	// is false when a higher-ranked source (config.yaml dolt.port, the dolt
	// data directory's listener.port, a port file with no pid file beside it)
	// chose a different port than the metadata.json dolt_server_port that
	// makes the workspace external.
	NamesEndpoint bool
}

// ExternalPortSetting finds the setting that declares beadsDir's Dolt server
// externally managed by naming a port, for a dial of port resolved from
// source; the zero ExternalPortDeclaration when no such setting does. The
// settings are BEADS_DOLT_SERVER_PORT and the legacy BEADS_DOLT_PORT
// (ResolveServerMode check 2c, #5934), each only when its value is the dialed
// port, whichever source chose that port: a config.yaml dolt.port or
// listener.port outranks the legacy BEADS_DOLT_PORT for the dial, and an env
// var naming the same port still declares the server external. Then
// metadata.json's dolt_server_port (check 4, GH#2554), which declares the
// server external whichever source chose the port, so it is reported beside a
// config.yaml or listener.port port too (`bd dolt set port N --update-config`
// writes both). Excluded: shared-server mode, since `bd dolt start` is how bd
// starts that server; a port from bd's port file with bd's pid file beside it,
// which records a server bd started; and the shared-server and remote-host
// defaults. A port file with no pid file beside it records only the port an
// earlier open reached (the store writes one after a successful local open),
// and it outranks listener.port, config.yaml and dolt_server_port for the
// dial, so its port is matched against the settings like any other. A port of
// 0 matches any named value. The env vars are parsed as the store and check
// 2c parse them, untrimmed, so a value neither of them reads is never named.
func ExternalPortSetting(beadsDir string, source PortSource, port int) ExternalPortDeclaration {
	switch source {
	case PortSourceUnset, PortSourceSharedServerDefault, PortSourceExternalHostDefault:
		return ExternalPortDeclaration{}
	case PortSourcePortFile:
		if _, err := os.Stat(pidPath(beadsDir)); err == nil {
			return ExternalPortDeclaration{}
		}
	}
	if IsSharedServerMode() || ResolveServerMode(beadsDir) != ServerModeExternal {
		return ExternalPortDeclaration{}
	}
	for _, name := range []string{"BEADS_DOLT_SERVER_PORT", "BEADS_DOLT_PORT"} {
		if p, err := strconv.Atoi(os.Getenv(name)); err == nil && p > 0 && (port == 0 || p == port) {
			return ExternalPortDeclaration{Setting: name, NamesEndpoint: true}
		}
	}
	metadataPath := configfile.ConfigPath(beadsDir)
	if _, err := os.Stat(metadataPath); err != nil {
		return ExternalPortDeclaration{}
	}
	cfg, err := configfile.Load(beadsDir)
	if err != nil || cfg == nil || cfg.DoltServerPort <= 0 {
		return ExternalPortDeclaration{}
	}
	return ExternalPortDeclaration{
		Setting:       "dolt_server_port in " + metadataPath,
		NamesEndpoint: port == 0 || cfg.DoltServerPort == port,
	}
}

// Advice is the advice for an unreachable Dolt server at endpoint that d
// declares externally managed, or "" for the zero declaration. It says bd did
// not start a server, in the words `bd init --external` uses (#7674), and
// does not suggest `bd dolt start`: that command would start a server of bd's
// own over .beads/dolt on the same port, a different database that hides the
// real one and takes the port the real server needs.
func (d ExternalPortDeclaration) Advice(endpoint string) string {
	switch {
	case d.Setting == "":
		return ""
	case d.NamesEndpoint:
		return ExternallyManagedAdvice(d.Setting, endpoint)
	default:
		return fmt.Sprintf("%s declares this workspace's Dolt server externally managed, so bd did not start one at %s.\n"+
			"Start that server, or point bd at the right endpoint. Removing dolt_server_port lets bd manage the server.",
			d.Setting, endpoint)
	}
}

// ExternallyManagedAdvice is the advice for a Dolt server at endpoint that bd
// could not reach and that setting, by naming endpoint's port, declares
// externally managed. See ExternalPortDeclaration.Advice.
func ExternallyManagedAdvice(setting, endpoint string) string {
	return fmt.Sprintf("%s declares the Dolt server at %s externally managed, so bd did not start one.\n"+
		"Start that server, or point bd at the right endpoint.", setting, endpoint)
}

// UnreachableEndpointAdvice is the advice for a TCP dial of host:port, the
// port resolved from source, that found no server: the externally managed
// advice when a setting declares the workspace's server so (see
// ExternalPortSetting), or "" when the caller's own advice stands. A host off
// this machine also gets "", so the caller's own advice stands for it too.
func UnreachableEndpointAdvice(beadsDir, host string, port int, source PortSource) string {
	if !configfile.IsLocalHostString(host) {
		return ""
	}
	return ExternalPortSetting(beadsDir, source, port).Advice(net.JoinHostPort(host, strconv.Itoa(port)))
}
