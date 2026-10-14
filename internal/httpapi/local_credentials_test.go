package httpapi

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/uow"
)

type credentialWriteProvider struct {
	uow.UnitOfWorkProvider
	opened int
}

func (p *credentialWriteProvider) NewUOW(context.Context) (uow.UnitOfWork, error) {
	p.opened++
	return nil, fmt.Errorf("unexpected storage access")
}

func TestHTTPSettingCredentialsRefusedBeforeStorage(t *testing.T) {
	p := &credentialWriteProvider{}
	settings, err := uow.NewWorkspaceConfig(p)
	if err != nil {
		t.Fatal(err)
	}
	ts := newTestServer(t, rolesConfig(Config{Settings: settings}))
	for _, key := range []string{"github.token", "linear.api_key", "jira.api_token", "gitlab.token", "ado.pat", "notion.token", "github%2Etoken", "GITHUB.TOKEN", "AI.api_key"} {
		resp := ts.setSetting(t, "/v0/beads/config/"+key, `{"value":"synthetic-credential"}`)
		raw := readAll(t, resp)
		if resp.StatusCode != http.StatusBadRequest {
			t.Errorf("%s: status %d, want 400: %s", key, resp.StatusCode, raw)
		}
		if strings.Contains(raw, "synthetic-credential") {
			t.Errorf("%s: response exposed input", key)
		}
	}
	if p.opened != 0 {
		t.Errorf("opened %d units of work for refused writes", p.opened)
	}
}
