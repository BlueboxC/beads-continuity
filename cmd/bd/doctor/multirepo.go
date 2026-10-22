package doctor

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/steveyegge/beads/internal/config"
	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/dolt"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

// multiRepoYAMLConfig represents the types section of config.yaml for YAML unmarshaling.
type multiRepoYAMLConfig struct {
	Types struct {
		Custom []string `yaml:"custom"`
	} `yaml:"types"`
}

// CheckMultiRepoTypes discovers and reports custom types used by child repos in multi-repo setups.
// This is informational - the federation trust model means we don't require parent config to
// list child types, but it's useful to know what types each child uses.
func CheckMultiRepoTypes(repoPath string) DoctorCheck {
	multiRepo := config.GetMultiRepoConfig()
	if multiRepo == nil || len(multiRepo.Additional) == 0 {
		return DoctorCheck{
			Name:     "Multi-Repo Types",
			Status:   StatusOK,
			Message:  "N/A (single-repo mode)",
			Category: CategoryData,
		}
	}

	var details []string
	var warnings []string

	// Discover types from each child repo
	for _, repoPathStr := range multiRepo.Additional {
		childTypes, refused := discoverChildTypes(repoPathStr)
		if len(childTypes) > 0 {
			details = append(details, fmt.Sprintf("  %s: %s", repoPathStr, strings.Join(childTypes, ", ")))
		} else {
			details = append(details, fmt.Sprintf("  %s: (no custom types)", repoPathStr))
		}
		if refused != nil {
			details = append(details, fmt.Sprintf("    database not read, so only config.yaml types are listed: %v", refused))
		}
	}

	// Check for hydrated issues using types not found anywhere
	unknownTypes := findUnknownTypesInHydratedIssues(repoPath, multiRepo)
	if len(unknownTypes) > 0 {
		warnings = append(warnings, fmt.Sprintf("Issues with unknown types: %s", strings.Join(unknownTypes, ", ")))
	}

	status := StatusOK
	message := fmt.Sprintf("Discovered types from %d child repo(s)", len(multiRepo.Additional))

	if len(warnings) > 0 {
		status = StatusWarning
		message = fmt.Sprintf("Found %d type warning(s)", len(warnings))
		details = append(details, "")
		details = append(details, "Warnings:")
		details = append(details, warnings...)
	}

	return DoctorCheck{
		Name:     "Multi-Repo Types",
		Status:   status,
		Message:  message,
		Detail:   strings.Join(details, "\n"),
		Category: CategoryData,
	}
}

// discoverChildTypes retains the YAML fallback and reports a refused database read.
func discoverChildTypes(repoPath string) ([]string, error) {
	if strings.HasPrefix(repoPath, "~") {
		if home, err := os.UserHomeDir(); err == nil {
			repoPath = filepath.Join(home, repoPath[1:])
		}
	}
	beadsDir := ResolveBeadsDirForRepo(repoPath)
	childTypes, refused := readTypesFromDB(beadsDir)
	if refused == nil && len(childTypes) > 0 {
		return childTypes, nil
	}
	if os.IsNotExist(refused) {
		refused = nil
	}
	childTypes, err := readTypesFromYAML(beadsDir)
	if err == nil {
		return childTypes, refused
	}
	return nil, refused
}

// nativeTypeDiscoveryConfig classifies additional repositories without migrating
// their legacy metadata. Graph workspaces use a different adapter in this fork.
func nativeTypeDiscoveryConfig(beadsDir string) (*configfile.Config, error) {
	cfg, err := configfile.LoadForDiscovery(beadsDir)
	if err != nil || cfg == nil {
		return cfg, err
	}
	if !configfile.IsSupportedBackend(cfg.Backend) {
		return nil, fmt.Errorf("unrecognized backend %q", cfg.Backend)
	}
	mode, err := cfg.GetGraphMode()
	if err != nil {
		return nil, err
	}
	if mode != configfile.GraphModeDependency {
		return nil, fmt.Errorf("native type discovery does not open graph mode %q", mode)
	}
	if _, err := os.Lstat(filepath.Join(beadsDir, "graph-preview-format")); !os.IsNotExist(err) {
		return nil, fmt.Errorf("native type discovery cannot open a graph-format workspace: %v", err)
	}
	return cfg, nil
}

// readTypesFromDB reads types.custom from the database config table
func readTypesFromDB(beadsDir string) ([]string, error) {
	cfg, err := nativeTypeDiscoveryConfig(beadsDir)
	if err != nil || cfg == nil {
		return nil, err
	}
	if cfg.GetBackend() != configfile.BackendDolt {
		return nil, fmt.Errorf("not dolt backend")
	}

	doltPath := cfg.DatabasePath(beadsDir)
	if _, err := os.Stat(doltPath); os.IsNotExist(err) {
		return nil, err
	}

	ctx := context.Background()
	store, err := dolt.NewFromConfigWithCLIOptions(ctx, beadsDir, &dolt.Config{ReadOnly: true})
	if err != nil {
		return nil, err
	}
	defer func() { _ = store.Close() }()

	typesStr, err := store.GetConfig(ctx, "types.custom")
	if err != nil {
		return nil, err
	}

	// ParseTypesConfigValue returns nil for an empty or whitespace-only value.
	return issueops.ParseTypesConfigValue(typesStr), nil
}

// readTypesFromYAML reads types.custom from config.yaml
func readTypesFromYAML(beadsDir string) ([]string, error) {
	configPath := filepath.Join(beadsDir, "config.yaml")
	content, err := os.ReadFile(configPath) // #nosec G304 - path is controlled
	if err != nil {
		return nil, err
	}

	var cfg multiRepoYAMLConfig
	if err := yaml.Unmarshal(content, &cfg); err != nil {
		return nil, fmt.Errorf("parsing config.yaml: %w", err)
	}

	if len(cfg.Types.Custom) == 0 {
		return nil, nil
	}

	return cfg.Types.Custom, nil
}

// findUnknownTypesInHydratedIssues checks if any hydrated issues use types not found in any config
func findUnknownTypesInHydratedIssues(repoPath string, multiRepo *config.MultiRepoConfig) []string {
	beadsDir := ResolveBeadsDirForRepo(repoPath)

	cfg, err := nativeTypeDiscoveryConfig(beadsDir)
	if err != nil || cfg == nil {
		return nil
	}
	if cfg.GetBackend() != configfile.BackendDolt {
		return nil
	}

	doltPath := cfg.DatabasePath(beadsDir)
	if _, err := os.Stat(doltPath); os.IsNotExist(err) {
		return nil
	}

	ctx := context.Background()
	store, err := dolt.NewFromConfigWithCLIOptions(ctx, beadsDir, &dolt.Config{ReadOnly: true})
	if err != nil {
		return nil
	}
	defer func() { _ = store.Close() }()

	// Collect known custom types (parent custom + all child custom).
	// Built-in types — including orchestrator types like gate, molecule,
	// spike, story, and milestone — are recognized via IsBuiltIn below, so
	// this map only needs the configured custom types.
	knownTypes := make(map[string]bool)

	// Add parent's custom types
	parentTypes, err := store.GetConfig(ctx, "types.custom")
	if err == nil && parentTypes != "" {
		for _, t := range issueops.ParseTypesConfigValue(parentTypes) {
			knownTypes[t] = true
		}
	}

	// Add child types
	for _, repoPathStr := range multiRepo.Additional {
		childTypes, _ := discoverChildTypes(repoPathStr)
		for _, t := range childTypes {
			knownTypes[t] = true
		}
	}

	// Find issues with types not in knownTypes
	db := store.UnderlyingDB()
	rows, err := db.QueryContext(ctx, `
		SELECT DISTINCT issue_type FROM issues
		WHERE source_repo != '' AND source_repo != '.'
	`)
	if err != nil {
		return nil
	}
	defer rows.Close()

	var unknownTypes []string
	seen := make(map[string]bool)
	for rows.Next() {
		var issueType string
		if err := rows.Scan(&issueType); err != nil {
			continue
		}
		if !types.IssueType(issueType).IsBuiltIn() && !knownTypes[issueType] && !seen[issueType] {
			unknownTypes = append(unknownTypes, issueType)
			seen[issueType] = true
		}
	}
	// Best effort: rows.Err() ignored since partial results are acceptable for type discovery
	_ = rows.Err()

	return unknownTypes
}
