package notion

import (
	"context"
	"os"
	"strings"

	"github.com/steveyegge/beads/internal/config"
)

const configKeyToken = "notion.token"

type AuthSource string

const (
	AuthSourceConfigToken AuthSource = "config_token"
	AuthSourceEnv         AuthSource = "env"
)

type ResolvedAuth struct {
	Token  string
	Source AuthSource
}

// ConfigReader reads a Notion configuration value.
type ConfigReader interface {
	GetConfig(ctx context.Context, key string) (string, error)
}

// ResolveAuth uses only local configuration and environment, never synced rows.
// Keep the reader argument for callers compiled against the existing signature.
func ResolveAuth(_ context.Context, _ ConfigReader) (*ResolvedAuth, error) {
	if token := strings.TrimSpace(config.GetString(configKeyToken)); token != "" {
		return &ResolvedAuth{Token: token, Source: AuthSourceConfigToken}, nil
	}

	if token := strings.TrimSpace(os.Getenv("NOTION_TOKEN")); token != "" {
		return &ResolvedAuth{Token: token, Source: AuthSourceEnv}, nil
	}
	return nil, nil
}
