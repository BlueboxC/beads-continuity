package db

import (
	"context"
	"strings"
	"testing"
)

func TestConfigRepositoryRefusesCredentialBeforeMutation(t *testing.T) {
	r := NewConfigSQLRepository(nil)
	if err := r.SetConfig(context.Background(), "notion.token", "synthetic"); err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("refusal = %v", err)
	}
}
