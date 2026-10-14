package config

import "testing"

func TestLocalCredentialsStayOutOfSyncedConfig(t *testing.T) {
	for _, key := range []string{"github.token", "linear.api_key", "linear.oauth_client_id", "linear.oauth_client_secret", "jira.api_token", "gitlab.token", "ado.pat", "notion.token", "GITHUB.TOKEN", " github.token ", "ai.api_key", "AI.api_key", " ai.api_key "} {
		if !IsYamlOnlyKey(key) {
			t.Errorf("%q must use local configuration", key)
		}
		if err := ValidateSyncedConfigKey(key); err == nil {
			t.Errorf("%q was admitted to synced config", key)
		}
	}
	for _, key := range []string{"notion.data_source_id", "custom.token", "kv.token", "kv.memory.token", "sync.remote", "routing.mode"} {
		if err := ValidateSyncedConfigKey(key); err != nil {
			t.Errorf("compatible key %q: %v", key, err)
		}
	}
}
