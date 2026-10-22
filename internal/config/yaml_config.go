package config

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// YamlOnlyKeys are configuration keys that must be stored in config.yaml
// rather than the database. These are "startup" settings that are
// read before the database is opened.
//
// This fixes GH#536: users were confused when `bd config set no-db true`
// appeared to succeed but had no effect (because no-db is read from yaml
// at startup, not from the database).
var YamlOnlyKeys = map[string]bool{
	// Bootstrap flags (affect how bd starts)
	"no-db": true,
	"json":  true,
	// Events journal: read through viper during root pre-run, before the store
	// is open, so a DB-backed write would be silently unread — the GH#536 class
	// this map exists to prevent. Without these four entries
	// `bd config set events-journal true` reports success and changes nothing.
	"events-journal":             true,
	"events-journal-retain-days": true,
	"events-journal-retain-rows": true,
	"events-journal-auto-prune":  true,

	// Database and identity
	"db":       true,
	"actor":    true,
	"identity": true,
	// Replica identity: config.NodeID() reads this through viper (yaml/env)
	// only, so a DB-backed write would be silently unread — exactly the
	// GH#536 class this map exists to prevent.
	"node_id": true,

	// Git settings
	"git.author":      true,
	"git.no-gpg-sign": true,
	"no-push":         true,
	"no-git-ops":      true, // Disable git ops in bd prime session close protocol (GH#593)
	"agent.profile":   true, // Explicit policy profile for bd prime's close protocol (GH#3423)

	// Sync settings
	"sync.remote":     true, // Primary: any Dolt-compatible remote URL
	"sync.git-remote": true, // Deprecated: falls back from sync.remote
	"sync.require_confirmation_on_mass_delete": true,

	// Routing settings
	"routing.mode":        true,
	"routing.default":     true,
	"routing.maintainer":  true,
	"routing.contributor": true,

	// Create command settings
	"create.require-description": true,

	// Prime memory-injection caps (read at session start, possibly before
	// the database is reachable, so they must live in yaml)
	"prime.max-memories":     true,
	"prime.max-memory-chars": true,

	// Validation settings (bd-t7jq)
	// Values: "warn" | "error" | "none"
	"validation.on-create": true,
	"validation.on-close":  true,
	"validation.on-sync":   true,

	// Hierarchy settings (GH#995)
	"hierarchy.max-depth": true,

	// Backup settings (must be in yaml so GetValueSource can detect overrides)
	"backup.enabled":  true,
	"backup.interval": true,
	"backup.git-push": true,
	"backup.git-repo": true,

	// Import settings
	"import.auto": true,
	"import.path": true,

	// Dolt server settings
	"dolt.shared-server":      true, // Shared Dolt server at ~/.beads/shared-server/ (GH#2377)
	"dolt.max-conns":          true, // Connection pool size override (default 10, GH#3140)
	"dolt.pool-read-timeout":  true, // Pool per-I/O read deadline override (default 10s, bd-vz0y9)
	"dolt.pool-write-timeout": true, // Pool per-I/O write deadline override (default 10s, bd-vz0y9)
	"dolt.debug":              true, // Debug-mode dolt sql-server: --loglevel=debug + --prof cpu

	// Secrets: tokens and API keys must NOT be stored in the Dolt database
	// because that data is pushed to remotes, triggering secret-scanning
	// blocks on GitHub. Store them in local config.yaml instead.
	"github.token":               true,
	"linear.api_key":             true,
	"linear.oauth_client_id":     true,
	"linear.oauth_client_secret": true,
	"jira.api_token":             true,
	"gitlab.token":               true,
	"ado.pat":                    true,
	"notion.token":               true,
}

// IsLocalEndpointKey identifies authenticated tracker destinations that must be
// chosen locally, independently of synced project settings.
func IsLocalEndpointKey(key string) bool {
	switch strings.ToLower(strings.TrimSpace(key)) {
	case "github.url", "gitlab.url", "jira.url", "ado.url", "linear.api_endpoint":
		return true
	}
	return false
}

// IsLocalCredentialKey identifies integration credentials reserved for local
// configuration. Ordinary settings and user KV keys keep their existing rules.
func IsLocalCredentialKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	return IsYamlOnlyKey(key) && (IsSecretKey(key) || key == "linear.oauth_client_id")
}

// ValidateSyncedConfigKey refuses credentials before a config-table mutation.
func ValidateSyncedConfigKey(key string) error {
	if IsLocalCredentialKey(key) {
		return fmt.Errorf("%q must be set locally in config.yaml or its environment variable, not the synced database", key)
	}
	return nil
}

// IsYamlOnlyKey returns true if the given key should be stored in config.yaml
// rather than the Dolt database.
func IsYamlOnlyKey(key string) bool {
	key = strings.ToLower(strings.TrimSpace(key))
	// Check exact match
	if YamlOnlyKeys[key] || IsLocalEndpointKey(key) {
		return true
	}

	// Check prefix matches for nested keys
	prefixes := []string{"routing.", "sync.", "git.", "directory.", "repos.", "external_projects.", "validation.", "lint.", "hierarchy.", "ai.", "backup.", "export.", "dolt.", "federation.", "metrics.", "list.", "audit.", "storage-class."}
	for _, prefix := range prefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}

	return false
}

// secretKeyPatterns are substrings that identify a key as carrying sensitive
// material. Matched anywhere in the key, so they must be long enough that a
// substring hit is never an accident.
var secretKeyPatterns = []string{
	"api_key", "api-key", "apikey", "secret", "token", "password", "passwd",
	"credential", "private_key", "privatekey", "privkey",
}

// secretKeySegments are whole segments — split on `.`, `_` and `-` — that mark
// a key as sensitive.
//
// They are matched as SEGMENTS rather than as substrings because every one of
// them is a prefix of an ordinary word: as a substring, "pat" would redact
// `issue.path` and `export.pattern`, "auth" would redact `commit.author`, and
// "key" would redact `sort.keyword`. As a segment, `github.pat` and
// `commit.author` are told apart correctly.
var secretKeySegments = map[string]bool{
	"key": true, "keys": true, "apikey": true, "pwd": true, "pat": true,
	"auth": true, "bearer": true, "cert": true, "credential": true,
	"credentials": true, "secret": true, "token": true, "password": true,
}

// IsSecretKey reports whether a config key holds sensitive material.
//
// IT IS A SECURITY CONTROL, not only a lint. Two callers depend on it: the
// `bd config set` guard that refuses to write a credential into a git-tracked
// file, and — since the settings surface went on the wire — the redaction in
// internal/httpapi that decides whether GET /v0/beads/config publishes a
// value. Redaction is the whole control there: a `bd serve` bearer is optional
// and, where configured, shared and surface-wide, so it cannot withhold one
// value from one caller — and there is no TLS either. A spelling missing from
// this predicate is a credential served in cleartext.
//
// It errs toward over-redacting for that reason: a key wrongly withheld is an
// operator asking why, and a key wrongly published cannot be recalled. The
// decision is about the KEY alone; no value is ever inspected.
func IsSecretKey(key string) bool {
	lower := strings.ToLower(key)
	for _, pattern := range secretKeyPatterns {
		if strings.Contains(lower, pattern) {
			return true
		}
	}
	for _, segment := range strings.FieldsFunc(lower, func(r rune) bool {
		return r == '.' || r == '_' || r == '-'
	}) {
		if secretKeySegments[segment] {
			return true
		}
	}
	return false
}

// isGitTracked returns true if the file at path is tracked by git
// (i.e., has been git-added). Uses `git ls-files --error-unmatch`.
func isGitTracked(path string) bool {
	cmd := exec.Command("git", "ls-files", "--error-unmatch", path)
	cmd.Dir = filepath.Dir(path)
	cmd.Stdout = nil
	cmd.Stderr = nil
	return cmd.Run() == nil
}

var secretKeyEnvVarHints = map[string]string{ //nolint:gosec // Values are environment variable names, not credentials.
	// Single var name only: this value is interpolated into an
	// `export %s="..."` shell template, where "A or B" would silently
	// assign to B alone. ANTHROPIC_API_KEY is the primary; MiniMax users
	// can export MINIMAX_API_KEY instead (same resolution chain).
	"ai.api_key":     "ANTHROPIC_API_KEY",
	"github.token":   "GITHUB_TOKEN",
	"linear.api_key": "LINEAR_API_KEY",
}

// secretKeyEnvVarHint returns a suggested environment variable name for a
// secret config key, e.g. "linear.api_key" -> "LINEAR_API_KEY".
func secretKeyEnvVarHint(key string) string {
	if envVar, ok := secretKeyEnvVarHints[key]; ok {
		return envVar
	}
	return "BD_" + strings.ToUpper(strings.ReplaceAll(strings.ReplaceAll(key, "-", "_"), ".", "_"))
}

// CheckSecretKeyGitSafety checks whether writing key to the project's
// config.yaml would expose a secret in git history. Returns a descriptive
// error with remediation steps if so; nil otherwise. Non-secret keys always
// return nil.
func CheckSecretKeyGitSafety(key string) error {
	configPath, err := findProjectConfigYaml()
	if err != nil {
		return nil // can't resolve path; let the write fail with its own error
	}
	return checkSecretGitTracked(configPath, key)
}

func checkSecretGitTracked(configPath, key string) error {
	if !IsYamlOnlyKey(key) {
		return nil
	}
	if !IsSecretKey(key) {
		return nil
	}
	if !isGitTracked(configPath) {
		return nil
	}
	envVar := secretKeyEnvVarHint(key)
	return fmt.Errorf(
		"refusing to write secret key %q to git-tracked config file %s\n\n"+
			"This would expose your secret in git history. Instead:\n"+
			"  export %s=\"your-key-here\"    # add to ~/.secrets or ~/.zshrc\n\n"+
			"Or move config.yaml out of git tracking:\n"+
			"  git rm --cached %s\n"+
			"  echo \"config.yaml\" >> %s/.gitignore\n\n"+
			"To override this check (e.g., for testing):\n"+
			"  bd config set --force-git-tracked %s \"value\"",
		key, configPath,
		envVar,
		configPath,
		filepath.Dir(configPath),
		key,
	)
}

// SetYamlConfig sets a configuration value in the project's config.yaml file.
// It handles both adding new keys and updating existing (possibly commented) keys.
func SetYamlConfig(key, value string) error {
	// Validate specific keys (GH#995)
	if err := validateYamlConfigValue(key, value); err != nil {
		return err
	}

	configPath, err := findProjectConfigYaml()
	if err != nil {
		return err
	}

	return setYamlConfigAtPath(configPath, key, value)
}

// SetYamlConfigInDir sets a configuration value in the config.yaml located in
// the provided beadsDir, bypassing CWD/worktree discovery. Use this when the
// caller has already resolved the authoritative workspace and needs to avoid
// local worktree stubs shadowing the real shared config location.
func SetYamlConfigInDir(beadsDir, key, value string) error {
	// Validate specific keys (GH#995)
	if err := validateYamlConfigValue(key, value); err != nil {
		return err
	}

	configPath := filepath.Join(beadsDir, "config.yaml")
	if _, err := os.Stat(configPath); err != nil {
		if os.IsNotExist(err) {
			return fmt.Errorf("no config.yaml found in %s (run 'bd init' first)", beadsDir)
		}
		return fmt.Errorf("failed to stat config.yaml: %w", err)
	}

	return setYamlConfigAtPath(configPath, key, value)
}

var userGlobalKeyPrefixes = []string{"metrics."}

// userGlobalExactKeys are per-MACHINE settings that must never be written to
// the project .beads/config.yaml, which is a git-TRACKED file (see
// cmd/bd/doctor/gitignore.go: nothing in .beads/.gitignore excludes it). A
// committed value propagates one machine's answer to every clone that pulls
// it, which for these keys is worse than having no value at all.
//
// node_id is the exemplar: it names the beads STORE that grants leases here,
// and the reclaim guard (issueops.ReclaimExpiredLeasesInTx) compares it
// against each lease's granted_node. Commit "node_id: mini" and every replica
// reads "mini", so every comparison matches and the guard is simultaneously
// fully ARMED and fully INERT — laptop reaps mini's leases exactly as if they
// were local, which is the precise hazard the guard exists to close, now
// happening while the operator believes they are protected. Routing the write
// to ~/.config/bd/config.yaml keeps it per-machine; viper still merges that
// file, so config.NodeID() reads it back.
var userGlobalExactKeys = map[string]bool{"node_id": true}

func IsUserGlobalKey(key string) bool {
	if userGlobalExactKeys[key] {
		return true
	}
	for _, prefix := range userGlobalKeyPrefixes {
		if strings.HasPrefix(key, prefix) {
			return true
		}
	}
	return false
}

// readUserGlobalYamlValue reads a single dotted key from the user-global
// config.yaml ONLY, never project or BEADS_DIR config. It accepts both the
// nested form (metrics:\n  disabled: true) and the flat dotted form
// (metrics.disabled: true). It returns the raw scalar string and whether the
// key was present.
//
// Consent-bearing settings (metrics enablement and endpoint) are resolved
// through this rather than merged viper so a repository's .beads/config.yaml can
// never re-enable metrics for a user who opted out, nor redirect where metrics
// are sent. See MetricsDisabledByUserConfig / UserMetricsEndpoint.
func readUserGlobalYamlValue(key string) (string, bool) {
	configPath, err := UserConfigYamlPath()
	if err != nil {
		return "", false
	}
	return readYamlValueAtPath(configPath, key)
}

// WorkspaceYamlValue reads a single dotted key out of ONE workspace's
// config.yaml, named by its .beads directory, returning ("", false) when the
// file or the key is absent.
//
// It exists for the cross-workspace opens — routed creates, remote-cache
// hydration, `bd serve` against another workspace — where the process-wide
// merged config answers for the directory bd was LAUNCHED from, not for the
// workspace about to be written. A setting that governs what gets recorded in a
// target workspace has to be read from that target.
func WorkspaceYamlValue(beadsDir, key string) (string, bool) {
	if beadsDir == "" {
		return "", false
	}
	return readYamlValueAtPath(filepath.Join(beadsDir, "config.yaml"), key)
}

// WorkspaceYamlValueStrict reads one dotted key from a workspace config.yaml
// without conflating a malformed or unreadable file with an absent key. A
// missing file or key returns present=false and no error; all other I/O and
// YAML shape errors are returned to the caller.
func WorkspaceYamlValueStrict(beadsDir, key string) (string, bool, error) {
	if beadsDir == "" {
		return "", false, nil
	}
	return workspaceYamlSettingAtPath(filepath.Join(beadsDir, "config.yaml"), key, false)
}

// WorkspaceYamlSettingWithLocal matches local-over-project settings, treating
// commented-out/null values as unset while preserving real read/shape errors.
func WorkspaceYamlSettingWithLocal(beadsDir, key string) (string, bool, error) {
	if beadsDir == "" {
		return "", false, nil
	}
	names := []string{"config.local.yaml", "config.yaml"}
	if _, err := os.Stat(filepath.Join(beadsDir, "config.yaml")); err != nil {
		names = names[1:]
	}
	for _, name := range names {
		value, present, err := workspaceYamlSettingAtPath(filepath.Join(beadsDir, name), key, true)
		if err != nil || present {
			return value, present, err
		}
	}
	return "", false, nil
}

func workspaceYamlSettingAtPath(path, key string, nullIsUnset bool) (string, bool, error) {
	data, err := os.ReadFile(path) //nolint:gosec // beadsDir is caller-resolved workspace state
	if os.IsNotExist(err) {
		return "", false, nil
	}
	if err != nil {
		return "", false, fmt.Errorf("reading workspace config.yaml: %w", err)
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return "", false, fmt.Errorf("parsing workspace config.yaml: %w", err)
	}
	if root == nil {
		return "", false, nil
	}
	if raw, ok := root[key]; ok && (raw != nil || !nullIsUnset) {
		value, err := strictYamlScalarString(raw)
		if err != nil {
			return "", true, err
		}
		return value, true, nil
	}
	var node interface{} = root
	parts := strings.Split(key, ".")
	for i, part := range parts {
		m, ok := node.(map[string]interface{})
		if !ok {
			return "", true, fmt.Errorf("workspace config key %q has a non-map parent", key)
		}
		node, ok = m[part]
		if !ok || (node == nil && nullIsUnset) {
			return "", false, nil
		}
		if i == len(parts)-1 {
			value, err := strictYamlScalarString(node)
			if err != nil {
				return "", true, err
			}
			return value, true, nil
		}
	}
	return "", false, nil
}

func strictYamlScalarString(v interface{}) (string, error) {
	if v == nil {
		return "", fmt.Errorf("workspace config value is null")
	}
	switch v.(type) {
	case map[string]interface{}, []interface{}:
		return "", fmt.Errorf("workspace config value must be a scalar")
	default:
		return fmt.Sprintf("%v", v), nil
	}
}

func readYamlValueAtPath(path, key string) (string, bool) {
	data, err := os.ReadFile(path) //nolint:gosec // path is a resolved config.yaml path, not user input
	if err != nil {
		return "", false
	}
	var root map[string]interface{}
	if err := yaml.Unmarshal(data, &root); err != nil {
		return "", false
	}
	if raw, ok := root[key]; ok { // flat dotted form
		return yamlScalarString(raw)
	}
	var node interface{} = root // nested form
	for _, part := range strings.Split(key, ".") {
		m, ok := node.(map[string]interface{})
		if !ok {
			return "", false
		}
		node, ok = m[part]
		if !ok {
			return "", false
		}
	}
	return yamlScalarString(node)
}

func yamlScalarString(v interface{}) (string, bool) {
	switch s := v.(type) {
	case nil:
		return "", false
	case string:
		return s, true
	default:
		return fmt.Sprintf("%v", s), true
	}
}

// GetUserYamlConfig reads a single dotted key from the user-global config.yaml
// ONLY, never project/BEADS_DIR config, returning "" if unset. It is the read
// counterpart of SetUserYamlConfig/UnsetUserYamlConfig and the generic form of
// the per-key consent helpers below. User-global keys (see IsUserGlobalKey —
// currently metrics.*) must be read through this so `bd config get` reports the
// value that actually governs runtime behavior, not the merged value a project's
// .beads/config.yaml could shadow.
func GetUserYamlConfig(key string) string {
	raw, _ := readUserGlobalYamlValue(key)
	return strings.TrimSpace(raw)
}

// MetricsDisabledByUserConfig reports whether the user-global config.yaml sets
// metrics.disabled: true. Project/BEADS_DIR config is intentionally ignored so a
// repository can never re-enable metrics for a user who opted out globally.
// Absent or unparseable values read as "not disabled" (the default).
func MetricsDisabledByUserConfig() bool {
	raw, ok := readUserGlobalYamlValue("metrics.disabled")
	if !ok {
		return false
	}
	disabled, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return disabled
}

// UserMetricsEndpoint returns the metrics endpoint configured in the user-global
// config.yaml, or "" if unset. Project/BEADS_DIR config is intentionally ignored
// so a repository can never redirect a user's metrics endpoint. Callers fall
// back to the built-in default when this is empty.
func UserMetricsEndpoint() string {
	raw, _ := readUserGlobalYamlValue("metrics.endpoint")
	return strings.TrimSpace(raw)
}

// MetricsNoticeShownByUserConfig reports whether the user-global config.yaml
// records that the first-run metrics disclosure was already shown. Like consent
// and endpoint, it is resolved from the user-global config ONLY: a repository's
// .beads/config.yaml must not be able to set metrics.notice_shown: true and
// suppress the one-time disclosure for a user who has never actually seen it.
// Absent or unparseable values read as "not shown" (the default).
func MetricsNoticeShownByUserConfig() bool {
	raw, ok := readUserGlobalYamlValue("metrics.notice_shown")
	if !ok {
		return false
	}
	shown, err := strconv.ParseBool(strings.TrimSpace(raw))
	if err != nil {
		return false
	}
	return shown
}

func UnsetUserYamlConfig(key string) error {
	configPath, err := UserConfigYamlPath()
	if err != nil {
		return err
	}
	content, err := os.ReadFile(configPath) //nolint:gosec // configPath is a validated absolute user config path
	if err != nil {
		if os.IsNotExist(err) {
			return nil
		}
		return fmt.Errorf("failed to read user config.yaml: %w", err)
	}

	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	newContent, err := commentOutYamlKey(normalized, key)
	if err != nil {
		return err
	}
	if newContent == normalized {
		return nil
	}
	if err := collateralUnsetRefusal(normalized, newContent, key); err != nil {
		return err
	}

	// Preserve the owner-private 0600 posture every other user-global writer
	// uses (SetUserYamlConfig, setYamlConfigAtPath, the metrics bootstrap);
	// rewriting at 0644 would relax this shared user config to world-readable.
	if err := os.WriteFile(configPath, []byte(newContent), 0o600); err != nil { //nolint:gosec // configPath is from UserConfigYamlPath
		return fmt.Errorf("failed to write user config.yaml: %w", err)
	}

	return nil
}

func SetUserYamlConfig(key, value string) error {
	if err := validateYamlConfigValue(key, value); err != nil {
		return err
	}
	configPath, err := UserConfigYamlPath()
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(configPath), 0o755); err != nil {
		return fmt.Errorf("failed to create user config directory: %w", err)
	}
	if _, err := os.Stat(configPath); os.IsNotExist(err) {
		if err := os.WriteFile(configPath, []byte{}, 0o600); err != nil {
			return fmt.Errorf("failed to create user config.yaml: %w", err)
		}
	} else if err != nil {
		return fmt.Errorf("failed to stat user config.yaml: %w", err)
	}
	return setYamlConfigAtPath(configPath, key, value)
}

func setYamlConfigAtPath(configPath, key, value string) error {
	// Read existing config
	content, err := os.ReadFile(configPath) //nolint:gosec // configPath is from findProjectConfigYaml
	if err != nil {
		return fmt.Errorf("failed to read config.yaml: %w", err)
	}

	// Update or add the key
	newContent, err := updateYamlKey(string(content), key, value)
	if err != nil {
		return err
	}

	// Write back
	if err := os.WriteFile(configPath, []byte(newContent), 0600); err != nil { //nolint:gosec // configPath is validated
		return fmt.Errorf("failed to write config.yaml: %w", err)
	}

	return nil
}

// GetYamlConfig gets a configuration value from config.yaml.
// Returns empty string if key is not found or is commented out.
func GetYamlConfig(key string) string {
	if v == nil {
		return ""
	}
	return v.GetString(key)
}

// UnsetYamlConfig removes a configuration value from the project's config.yaml file.
// The key line is commented out (prefixed with "# ") to preserve it as documentation.
func UnsetYamlConfig(key string) error {
	configPath, err := findProjectConfigYaml()
	if err != nil {
		return err
	}

	content, err := os.ReadFile(configPath) //nolint:gosec // configPath is from findProjectConfigYaml
	if err != nil {
		return fmt.Errorf("failed to read config.yaml: %w", err)
	}

	normalized := strings.ReplaceAll(string(content), "\r\n", "\n")
	newContent, err := commentOutYamlKey(normalized, key)
	if err != nil {
		return err
	}
	if newContent == normalized {
		return nil
	}
	if err := collateralUnsetRefusal(normalized, newContent, key); err != nil {
		return err
	}

	if err := os.WriteFile(configPath, []byte(newContent), 0600); err != nil { //nolint:gosec // configPath is validated
		return fmt.Errorf("failed to write config.yaml: %w", err)
	}

	return nil
}

// findProjectConfigYaml finds the active config.yaml path for YAML-only config writes.
//
// Resolution order:
//  1. BEADS_DIR/config.yaml (when BEADS_DIR is set)
//  2. Walk up from CWD to find .beads/config.yaml
//
// This keeps YAML-only config behavior aligned with runtime resolution when
// BEADS_DIR points to an external runtime directory.
func findProjectConfigYaml() (string, error) {
	return findProjectConfigYamlWithFinder(findProjectBeadsDir)
}

func findProjectConfigYamlWithFinder(findBeadsDir func() string) (string, error) {
	// Respect BEADS_DIR first when set.
	if beadsDir := os.Getenv("BEADS_DIR"); beadsDir != "" {
		configPath := filepath.Join(beadsDir, "config.yaml")
		if _, err := os.Stat(configPath); err == nil {
			return configPath, nil
		}
		return "", fmt.Errorf("no config.yaml found in BEADS_DIR (%s) (run 'bd init' first)", beadsDir)
	}

	if configPath := projectConfigPathFromLoadedState(); configPath != "" {
		return configPath, nil
	}

	if findBeadsDir != nil {
		if beadsDir := findBeadsDir(); beadsDir != "" {
			configPath := filepath.Join(beadsDir, "config.yaml")
			if _, err := os.Stat(configPath); err == nil {
				return configPath, nil
			}
		}
	}

	return "", fmt.Errorf("no .beads/config.yaml found (run 'bd init' first)")
}

func projectConfigPathFromLoadedState() string {
	configPath := ConfigFileUsed()
	if configPath == "" {
		return ""
	}
	if filepath.Base(configPath) != "config.yaml" {
		return ""
	}
	if filepath.Base(filepath.Dir(configPath)) != ".beads" {
		return ""
	}
	if _, err := os.Stat(configPath); err != nil {
		return ""
	}
	return configPath
}

func findProjectBeadsDir() string {
	cwd, err := os.Getwd()
	if err != nil {
		return ""
	}

	for dir := cwd; dir != filepath.Dir(dir); dir = filepath.Dir(dir) {
		beadsDir := filepath.Join(dir, ".beads")
		if info, err := os.Stat(beadsDir); err == nil && info.IsDir() {
			return beadsDir
		}
	}

	configPath := worktreeFallbackConfigPath(cwd)
	if configPath == "" {
		return ""
	}

	return filepath.Dir(configPath)
}

// updateYamlKey updates a key in yaml content, handling commented-out keys.
// If the key exists (commented or not), it updates it in place.
// If the key doesn't exist, it appends it at the end.
func updateYamlKey(content, key, value string) (string, error) {
	if strings.Contains(key, ".") {
		if updated, ok, err := updateNestedYamlKey(content, key, value); err != nil {
			return "", err
		} else if ok {
			return updated, nil
		}
	}

	formattedValue := formatYamlValue(value)
	newLine := fmt.Sprintf("%s: %s", key, formattedValue)

	// Build regex to match the key (commented or not)
	// Matches: "key: value" or "# key: value" with optional leading whitespace
	keyPattern := regexp.MustCompile(`^(\s*)(#\s*)?` + regexp.QuoteMeta(key) + `\s*:`)

	found := false
	var result []string

	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := scanner.Text()
		if keyPattern.MatchString(line) {
			// Found the key - replace with new value (uncommented)
			// Preserve leading whitespace
			matches := keyPattern.FindStringSubmatch(line)
			indent := ""
			if len(matches) > 1 {
				indent = matches[1]
			}
			result = append(result, indent+newLine)
			found = true
		} else {
			result = append(result, line)
		}
	}
	// A line of bufio.MaxScanTokenSize bytes or more ends the scan early;
	// returning the lines read so far would truncate config.yaml on write.
	if err := scanner.Err(); err != nil {
		return "", fmt.Errorf("cannot set %q: scanning config content: %w", key, err)
	}

	if !found {
		// Key not found - append at end
		// Add blank line before if content doesn't end with one
		if len(result) > 0 && result[len(result)-1] != "" {
			result = append(result, "")
		}
		result = append(result, newLine)
	}

	return strings.Join(result, "\n"), nil
}

func updateNestedYamlKey(content, key, value string) (string, bool, error) {
	parts := strings.Split(key, ".")
	if len(parts) < 2 {
		return "", false, nil
	}

	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil {
		return "", false, err
	}
	if len(root.Content) == 0 {
		// An empty or comment-only document parses to no nodes at all: yaml.v3
		// keeps no trace of its text, not even the comments. So there is nothing
		// to nest into AND nothing to marshal back — fabricating a mapping here
		// would emit the new key and silently delete everything else in the
		// file, and `bd init`'s default template is exactly this shape, comments
		// and nothing else. Append the rendered key to the text instead, which
		// leaves the document byte for byte intact. Falling through to the flat
		// writer is not an option either: that is what produced a key literally
		// named "dolt.host", which GetStringFromDir — splitting on the dot and
		// looking for a nested mapping — can never read back.
		appended, err := appendNestedYamlKey(content, parts, value)
		if err != nil {
			return "", false, err
		}
		return appended, true, nil
	}
	mapping := root.Content[0]
	if mapping.Kind != yaml.MappingNode {
		return "", false, fmt.Errorf("cannot set %q: the top level of this config file is not a mapping", key)
	}

	// A flat key of this exact name is the unreadable shape, whether an older
	// bd wrote it or the file arrived that way. Migrate it: drop the flat entry
	// and write the value nested, so the round trip holds from here on. Only the
	// key being written is touched — a dotted key this call does not own is
	// someone else's and stays exactly as they wrote it.
	if idx := findMappingChild(mapping, key); idx != -1 {
		mapping.Content = append(mapping.Content[:idx], mapping.Content[idx+2:]...)
	}

	leaf, err := findOrCreateNestedScalar(mapping, parts)
	if err != nil {
		return "", false, err
	}

	leaf.Kind = yaml.ScalarNode
	leaf.Tag = ""
	leaf.Style = scalarStyleFor(value)
	leaf.Value = value

	out, err := yaml.Marshal(&root)
	if err != nil {
		return "", false, err
	}
	return string(out), true, nil
}

func findOrCreateNestedScalar(mapping *yaml.Node, parts []string) (*yaml.Node, error) {
	current := mapping
	for i, part := range parts {
		idx := findMappingChild(current, part)
		isLeaf := i == len(parts)-1
		if idx == -1 {
			keyNode := &yaml.Node{Kind: yaml.ScalarNode, Tag: "!!str", Value: part}
			var valNode *yaml.Node
			if isLeaf {
				valNode = &yaml.Node{Kind: yaml.ScalarNode}
			} else {
				valNode = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map"}
			}
			current.Content = append(current.Content, keyNode, valNode)
			if isLeaf {
				return valNode, nil
			}
			current = valNode
			continue
		}
		child := current.Content[idx+1]
		if isLeaf {
			return child, nil
		}
		if child.Tag == "!!null" {
			// A section with nothing left under it — `sync:` and no more, which
			// is exactly what an unset leaves behind once it has commented the
			// last leaf out. It holds no value to lose, so treat it as the empty
			// mapping it looks like. Refusing here sent the caller to the flat
			// writer, so set -> unset -> set put the unreadable `sync.remote:`
			// spelling back into the file this whole fix exists to keep out.
			child.Kind = yaml.MappingNode
			child.Tag = "!!map"
			child.Value = ""
			child.Style = 0
		}
		if child.Kind != yaml.MappingNode {
			return nil, fmt.Errorf("cannot set %q: %q already holds a value, so there is no section to nest under it",
				strings.Join(parts, "."), strings.Join(parts[:i+1], "."))
		}
		current = child
	}
	// Unreachable: every path through the loop returns on the last part.
	return nil, fmt.Errorf("cannot set %q: no key to write", strings.Join(parts, "."))
}

// appendNestedYamlKey renders just the key being written and appends it to the
// document text. Used when the document has no nodes to walk, where the text is
// the only copy of the file's contents that exists.
func appendNestedYamlKey(content string, parts []string, value string) (string, error) {
	node := &yaml.Node{Kind: yaml.ScalarNode, Style: scalarStyleFor(value), Value: value}
	for i := len(parts) - 1; i >= 0; i-- {
		node = &yaml.Node{Kind: yaml.MappingNode, Tag: "!!map", Content: []*yaml.Node{
			{Kind: yaml.ScalarNode, Tag: "!!str", Value: parts[i]},
			node,
		}}
	}
	rendered, err := yaml.Marshal(node)
	if err != nil {
		return "", err
	}

	// Same spacing the flat writer uses: a blank line between whatever was
	// already in the file and the key being added.
	existing := strings.TrimRight(content, "\n")
	if existing == "" {
		return string(rendered), nil
	}
	return existing + "\n\n" + string(rendered), nil
}

func findMappingChild(mapping *yaml.Node, name string) int {
	for i := 0; i < len(mapping.Content); i += 2 {
		k := mapping.Content[i]
		if k.Kind == yaml.ScalarNode && k.Value == name {
			return i
		}
	}
	return -1
}

func scalarStyleFor(value string) yaml.Style {
	if value == "" {
		return yaml.DoubleQuotedStyle
	}
	if _, err := strconv.ParseBool(value); err == nil {
		return 0
	}
	if _, err := strconv.ParseFloat(value, 64); err == nil {
		return 0
	}
	switch value {
	case "null", "Null", "NULL", "~", "yes", "no", "on", "off":
		return yaml.DoubleQuotedStyle
	}
	if strings.ContainsAny(value, ":#\n\"'") || strings.HasPrefix(value, " ") || strings.HasSuffix(value, " ") {
		return yaml.DoubleQuotedStyle
	}
	return 0
}

// commentOutYamlKey adapts PR #7684 to the fork's error-only unset API.
func commentOutYamlKey(content, key string) (string, error) {
	content = strings.ReplaceAll(content, "\r\n", "\n")

	if !yamlHoldsKey(content, key) {
		return content, nil
	}

	if err := unsupportedUnsetShape(content, key); err != nil {
		return "", err
	}

	flatPattern := regexp.MustCompile(`^(\s*)` + regexp.QuoteMeta(key) + `\s*:`)

	var result []string
	blockIndent := -1
	lines := strings.Split(content, "\n")
	topIndent := yamlTopLevelIndent(lines)
	walk := newNestedKeyWalk(key, topIndent)
	for i, line := range lines {
		if blockIndent >= 0 {
			if strings.TrimSpace(line) == "" || lineIndent(line) > blockIndent {
				result = append(result, line)
				continue
			}
			blockIndent = -1
		}
		blockIndent = blockScalarIndent(line)

		if matches := flatPattern.FindStringSubmatch(line); matches != nil && len(matches[1]) == topIndent {
			if err := mappingValueUnsetRefusal(lines, i, len(matches[1]), key); err != nil {
				return "", err
			}
			result = append(result, matches[1]+"# "+strings.TrimLeft(line, " \t"))
			continue
		}

		if name, indent, ok := yamlKeyOnLine(line); ok && walk.step(name, indent) {
			if err := mappingValueUnsetRefusal(lines, i, indent, key); err != nil {
				return "", err
			}
			result = append(result, strings.Repeat(" ", indent)+"# "+strings.TrimLeft(line, " \t"))
			continue
		}
		result = append(result, line)
	}

	out := strings.Join(result, "\n")
	return strings.TrimRight(out, "\n") + content[len(strings.TrimRight(content, "\n")):], nil
}

func mappingValueUnsetRefusal(lines []string, idx, indent int, key string) error {
	_, rest, _ := strings.Cut(strings.TrimLeft(lines[idx], " \t"), ":")
	if !valueContinuesBelow(rest) {
		return nil
	}
	for _, next := range lines[idx+1:] {
		trimmed := strings.TrimSpace(next)
		if trimmed == "" || strings.HasPrefix(trimmed, "#") {
			continue
		}
		nextIndent := lineIndent(next)
		if nextIndent < indent {
			return nil
		}
		if nextIndent == indent && trimmed != "-" && !strings.HasPrefix(trimmed, "- ") {
			return nil
		}
		return fmt.Errorf("cannot unset %q: its value continues on the lines beneath it, and commenting the key out would orphan them; remove it by hand, or unset its keys individually if it is a mapping", key)
	}
	return nil
}

func valueContinuesBelow(rest string) bool {
	rest = strings.TrimSpace(rest)
	if i := strings.Index(rest, " #"); i >= 0 {
		rest = rest[:i]
	} else if strings.HasPrefix(rest, "#") {
		rest = ""
	}
	for _, tok := range strings.Fields(rest) {
		if !strings.HasPrefix(tok, "&") && !strings.HasPrefix(tok, "!") {
			return false
		}
	}
	return true
}

func yamlTopLevelIndent(lines []string) int {
	for _, line := range lines {
		if _, indent, ok := yamlKeyOnLine(line); ok {
			return indent
		}
	}
	return 0
}

type nestedKeyWalk struct {
	segments  []string
	topIndent int
	depth     int
	indents   []int
	opaque    int
}

func newNestedKeyWalk(key string, topIndent int) *nestedKeyWalk {
	return &nestedKeyWalk{segments: strings.Split(key, "."), topIndent: topIndent, opaque: -1}
}

func (w *nestedKeyWalk) step(name string, indent int) bool {
	if len(w.segments) < 2 {
		return false
	}
	if w.opaque >= 0 && indent > w.opaque {
		return false
	}
	w.opaque = -1
	for w.depth > 0 && indent <= w.indents[w.depth-1] {
		w.depth--
		w.indents = w.indents[:w.depth]
	}
	if w.depth < len(w.segments) && name == w.segments[w.depth] && (w.depth > 0 || indent == w.topIndent) {
		if w.depth == len(w.segments)-1 {
			w.depth, w.indents = 0, nil
			return true
		}
		w.indents = append(w.indents, indent)
		w.depth++
		return false
	}
	w.opaque = indent
	return false
}

func unsupportedUnsetShape(content, key string) error {
	segments := strings.Split(key, ".")
	var root yaml.Node
	if err := yaml.Unmarshal([]byte(content), &root); err != nil || len(root.Content) == 0 {
		return nil
	}

	if err := flatKeyUnsetRefusal(content, root.Content[0], key); err != nil || len(segments) == 1 {
		return err
	}

	node := root.Content[0]
	var keyNode *yaml.Node
	parents := make([]*yaml.Node, 0, len(segments))
	for _, segment := range segments {
		if node.Kind != yaml.MappingNode {
			return nil
		}
		idx := findMappingChild(node, segment)
		if idx == -1 {
			return nil
		}
		parents = append(parents, node)
		keyNode, node = node.Content[idx], node.Content[idx+1]
	}

	for i, parent := range parents {
		if parent.Style&yaml.FlowStyle != 0 {
			return flowStyleUnsetRefusal(key, segments[:i])
		}
	}
	if err := blockScalarUnsetRefusal(node, key); err != nil {
		return err
	}
	return spansLinesUnsetRefusal(content, keyNode, node, key)
}

func flatKeyUnsetRefusal(content string, top *yaml.Node, key string) error {
	if top.Kind != yaml.MappingNode {
		return nil
	}
	idx := findMappingChild(top, key)
	if idx == -1 {
		return nil
	}
	if top.Style&yaml.FlowStyle != 0 {
		return flowStyleUnsetRefusal(key, nil)
	}
	if err := blockScalarUnsetRefusal(top.Content[idx+1], key); err != nil {
		return err
	}
	return spansLinesUnsetRefusal(content, top.Content[idx], top.Content[idx+1], key)
}

func flowStyleUnsetRefusal(key string, parent []string) error {
	where := "the top level of this config file"
	if len(parent) > 0 {
		where = fmt.Sprintf("%q", strings.Join(parent, "."))
	}
	return fmt.Errorf("cannot unset %q: %s is written in flow style ({...}), which bd cannot edit; remove the key by hand", key, where)
}

func blockScalarUnsetRefusal(valueNode *yaml.Node, key string) error {
	if valueNode.Style&(yaml.LiteralStyle|yaml.FoldedStyle) == 0 {
		return nil
	}
	return fmt.Errorf("cannot unset %q: its value is a block scalar (| or >), and commenting the key out would leave the body behind as a value of its own; remove the key by hand", key)
}

func spansLinesUnsetRefusal(content string, keyNode, valueNode *yaml.Node, key string) error {
	end, ok := yamlLineEnd(content, keyNode.Line)
	if !ok {
		return nil
	}
	var cut yaml.Node
	if yaml.Unmarshal([]byte(content[:end]), &cut) == nil && !plainScalarGoesOn(&cut, keyNode, valueNode) {
		return nil
	}
	return fmt.Errorf("cannot unset %q: its value spans more than one line, and commenting the key out would leave the rest of it behind; remove the key by hand", key)
}

func plainScalarGoesOn(cut, keyNode, valueNode *yaml.Node) bool {
	if valueNode.Kind != yaml.ScalarNode || valueNode.Line != keyNode.Line {
		return false
	}
	last := cut
	for len(last.Content) > 0 {
		last = last.Content[len(last.Content)-1]
	}
	return last.Value != valueNode.Value
}

func yamlLineEnd(content string, line int) (end int, ok bool) {
	breaks := 0
	for i, r := range content {
		switch {
		case r == '\n' && i > 0 && content[i-1] == '\r':
			continue // the LF of a CRLF, already counted at its CR
		case r == '\n', r == '\r', r == '\u0085', r == '\u2028', r == '\u2029':
			breaks++
			if breaks == line {
				return i, true
			}
		}
	}
	return 0, false
}

func collateralUnsetRefusal(before, after, key string) error {
	was, wasErr := yamlDocuments(before)
	if wasErr != nil && len(was) == 0 {
		return nil
	}
	is, isErr := yamlDocuments(after)
	if wasErr != nil && len(is) >= len(was) {
		is, isErr = is[:len(was)], nil
	}
	wasLeaves, ok := yamlLeaves(was)
	if !ok {
		return fmt.Errorf("cannot unset %q: this file nests more than %d levels deep, too deep for bd to check what else commenting out the key would change; remove the key by hand", key, maxYamlLeafDepth)
	}
	isLeaves, ok := yamlLeaves(is)
	if ok && isErr == nil && onlyKeyLeavesRemoved(wasLeaves, isLeaves, key) {
		return nil
	}
	return fmt.Errorf("cannot unset %q: commenting out the lines that match it would change more of this file than the key itself; remove the key by hand", key)
}

type yamlLeaf struct {
	doc   int
	path  []string
	value string
}

func (l yamlLeaf) id() string {
	return fmt.Sprintf("%d %q %q", l.doc, l.path, l.value)
}

const maxYamlLeafDepth = 64

func yamlLeaves(docs []*yaml.Node) (leaves []yamlLeaf, ok bool) {
	for doc, root := range docs {
		if yamlNestsDeeper(root, maxYamlLeafDepth) {
			return nil, false
		}
		leaves = appendYamlLeaves(leaves, doc, nil, root)
	}
	return leaves, true
}

func yamlNestsDeeper(n *yaml.Node, depth int) bool {
	if n.Kind == yaml.MappingNode || n.Kind == yaml.SequenceNode {
		depth--
	}
	for _, child := range n.Content {
		if depth < 0 || yamlNestsDeeper(child, depth) {
			return true
		}
	}
	return false
}

func yamlDocuments(content string) ([]*yaml.Node, error) {
	dec := yaml.NewDecoder(strings.NewReader(content))
	var docs []*yaml.Node
	for {
		var root yaml.Node
		err := dec.Decode(&root)
		if errors.Is(err, io.EOF) {
			return docs, nil
		}
		if err != nil {
			return docs, err
		}
		docs = append(docs, &root)
	}
}

func appendYamlLeaves(leaves []yamlLeaf, doc int, path []string, n *yaml.Node) []yamlLeaf {
	switch n.Kind {
	case yaml.DocumentNode:
		for _, child := range n.Content {
			leaves = appendYamlLeaves(leaves, doc, path, child)
		}
	case yaml.MappingNode:
		for i := 0; i+1 < len(n.Content); i += 2 {
			leaves = appendYamlLeaves(leaves, doc, append(path[:len(path):len(path)], n.Content[i].Value), n.Content[i+1])
		}
	case yaml.SequenceNode:
		for i, item := range n.Content {
			leaves = appendYamlLeaves(leaves, doc, append(path[:len(path):len(path)], strconv.Itoa(i)), item)
		}
	case yaml.AliasNode:
		leaves = append(leaves, yamlLeaf{doc: doc, path: path, value: fmt.Sprintf("*%s@%d:%d", n.Value, n.Alias.Line, n.Alias.Column)})
	case yaml.ScalarNode:
		if n.ShortTag() != "!!null" {
			leaves = append(leaves, yamlLeaf{doc: doc, path: path, value: n.Value})
		}
	}
	return leaves
}

func onlyKeyLeavesRemoved(was, is []yamlLeaf, key string) bool {
	left := make(map[string]int, len(was))
	for _, leaf := range was {
		left[leaf.id()]++
	}
	for _, leaf := range is {
		id := leaf.id()
		if left[id] == 0 {
			return false
		}
		left[id]--
	}
	segments := strings.Split(key, ".")
	for _, leaf := range was {
		p := leaf.path
		ofKey := (len(p) > 0 && p[0] == key) || (len(p) >= len(segments) && slices.Equal(p[:len(segments)], segments))
		if left[leaf.id()] > 0 && !ofKey {
			return false
		}
	}
	return true
}

func yamlHoldsKey(content, key string) bool {
	docs, err := yamlDocuments(content)
	if err != nil && len(docs) == 0 {
		return true
	}
	segments := strings.Split(key, ".")
	for _, doc := range docs {
		for _, top := range doc.Content {
			if mappingHoldsPath(top, []string{key}) || mappingHoldsPath(top, segments) {
				return true
			}
		}
	}
	return false
}

func mappingHoldsPath(n *yaml.Node, path []string) bool {
	level := []*yaml.Node{n}
	for _, name := range path {
		var next []*yaml.Node
		for _, m := range level {
			if m.Kind != yaml.MappingNode {
				continue
			}
			for i := 0; i+1 < len(m.Content); i += 2 {
				if k := m.Content[i]; k.Kind == yaml.ScalarNode && k.Value == name {
					next = append(next, m.Content[i+1])
				}
			}
		}
		level = next
	}
	return len(level) > 0
}

func blockScalarIndent(line string) int {
	_, indent, ok := yamlKeyOnLine(line)
	if !ok {
		return -1
	}
	_, rest, _ := strings.Cut(strings.TrimLeft(line, " \t"), ":")
	rest = strings.TrimSpace(rest)
	// "|" and ">" are only ever block indicators in value position; a plain
	// scalar cannot start with either.
	if rest == "" || (rest[0] != '|' && rest[0] != '>') {
		return -1
	}
	return indent
}

func lineIndent(line string) int {
	return len(line) - len(strings.TrimLeft(line, " \t"))
}

// yamlKeyOnLine reports the key a mapping line declares and its indentation.
// Comments, list items and blank lines declare nothing.
func yamlKeyOnLine(line string) (name string, indent int, ok bool) {
	trimmed := strings.TrimLeft(line, " \t")
	if trimmed == "" || strings.HasPrefix(trimmed, "#") || strings.HasPrefix(trimmed, "- ") {
		return "", 0, false
	}
	key, _, found := strings.Cut(trimmed, ":")
	if !found {
		return "", 0, false
	}
	key = strings.TrimSpace(key)
	if key == "" || strings.ContainsAny(key, " \t") {
		return "", 0, false
	}
	return key, lineIndent(line), true
}

// formatYamlValue formats a value appropriately for YAML.
func formatYamlValue(value string) string {
	// Boolean values
	lower := strings.ToLower(value)
	if lower == "true" || lower == "false" {
		return lower
	}

	// Numeric values - return as-is
	if isNumeric(value) {
		return value
	}

	// Duration values (like "30s", "5m") - return as-is
	if isDuration(value) {
		return value
	}

	// For all other string-like values, quote to preserve YAML string semantics
	return fmt.Sprintf("%q", value)
}

func isNumeric(s string) bool {
	if s == "" {
		return false
	}
	for i, c := range s {
		if c == '-' && i == 0 {
			continue
		}
		if c == '.' {
			continue
		}
		if c < '0' || c > '9' {
			return false
		}
	}
	return true
}

func isDuration(s string) bool {
	if len(s) < 2 {
		return false
	}
	suffix := s[len(s)-1]
	if suffix != 's' && suffix != 'm' && suffix != 'h' {
		return false
	}
	return isNumeric(s[:len(s)-1])
}

// validateYamlConfigValue validates a configuration value before setting.
// Returns an error if the value is invalid for the given key.
func validateYamlConfigValue(key, value string) error {
	switch key {
	case "hierarchy.max-depth":
		// Must be a positive integer >= 1 (GH#995)
		depth, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("hierarchy.max-depth must be a positive integer, got %q", value)
		}
		if depth < 1 {
			return fmt.Errorf("hierarchy.max-depth must be at least 1, got %d", depth)
		}
	case "dolt.shared-server":
		lower := strings.ToLower(value)
		if lower != "true" && lower != "false" {
			return fmt.Errorf("dolt.shared-server must be \"true\" or \"false\", got %q", value)
		}
	case "dolt.debug":
		lower := strings.ToLower(value)
		if lower != "true" && lower != "false" {
			return fmt.Errorf("dolt.debug must be \"true\" or \"false\", got %q", value)
		}
	case "dolt.mode":
		lower := strings.ToLower(value)
		if lower != "server" && lower != "embedded" {
			return fmt.Errorf("dolt.mode must be \"server\" or \"embedded\", got %q", value)
		}
	case "prime.max-memories":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("prime.max-memories must be a non-negative integer (0 = unlimited), got %q", value)
		}
		if n < 0 {
			return fmt.Errorf("prime.max-memories must be a non-negative integer (0 = unlimited), got %q", value)
		}
	case "prime.max-memory-chars":
		n, err := strconv.Atoi(value)
		if err != nil {
			return fmt.Errorf("prime.max-memory-chars must be a non-negative integer (0 = unlimited), got %q", value)
		}
		if n < 0 {
			return fmt.Errorf("prime.max-memory-chars must be a non-negative integer (0 = unlimited), got %q", value)
		}
	}
	return nil
}
