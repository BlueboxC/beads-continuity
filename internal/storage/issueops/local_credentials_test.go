package issueops

import (
	"context"
	"strings"
	"testing"
)

func TestConfigSQLRefusesCredentialBeforeMutation(t *testing.T) {
	if err := SetConfigInTx(context.Background(), nil, "github.token", "synthetic"); err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("refusal = %v", err)
	}
}
