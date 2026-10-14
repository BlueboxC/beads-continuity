package workapi

import (
	"errors"
	"testing"

	"github.com/steveyegge/beads/issueops"
)

func TestSettingWriteRefusesLocalCredentials(t *testing.T) {
	for _, key := range []string{"github.token", "linear.api_key", "linear.oauth_client_id", "linear.oauth_client_secret", "jira.api_token", "gitlab.token", "ado.pat", "notion.token", "GITHUB.TOKEN", " github.token ", "ai.api_key", "AI.api_key", " ai.api_key "} {
		for _, value := range []string{"synthetic-credential", ""} {
			if _, err := ValidateSettingWrite(key, value); !errors.Is(err, issueops.ErrValidation) {
				t.Errorf("ValidateSettingWrite(%q) = %v, want validation refusal", key, err)
			}
		}
	}
}
