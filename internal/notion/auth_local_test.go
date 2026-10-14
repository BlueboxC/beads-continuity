package notion

import (
	"context"
	"github.com/steveyegge/beads/internal/config"
	"testing"
)

type forbiddenAuthReader struct{}

func (forbiddenAuthReader) GetConfig(context.Context, string) (string, error) {
	panic("authentication queried synced config")
}

func TestNotionAuthNeverReadsSyncedToken(t *testing.T) {
	t.Chdir(t.TempDir())
	config.ResetForTesting()
	if err := config.Initialize(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(config.ResetForTesting)
	old := config.GetString("notion.token")
	t.Cleanup(func() { config.Set("notion.token", old) })
	config.Set("notion.token", "")
	t.Setenv("NOTION_TOKEN", "")
	a, err := ResolveAuth(context.Background(), forbiddenAuthReader{})
	if err != nil || a != nil {
		t.Fatalf("no local auth: %+v, %v", a, err)
	}
	t.Setenv("NOTION_TOKEN", "synthetic-env")
	a, err = ResolveAuth(context.Background(), forbiddenAuthReader{})
	if err != nil || a == nil || a.Token != "synthetic-env" || a.Source != AuthSourceEnv {
		t.Fatalf("env auth: %+v, %v", a, err)
	}
	config.Set("notion.token", "synthetic-local")
	a, err = ResolveAuth(context.Background(), forbiddenAuthReader{})
	if err != nil || a == nil || a.Token != "synthetic-local" || a.Source != AuthSourceConfigToken {
		t.Fatalf("local auth: %+v, %v", a, err)
	}
}
