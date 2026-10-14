package dolt

import (
	"context"
	"strings"
	"testing"
)

func TestConfigTransactionRefusesCredentialBeforeMutation(t *testing.T) {
	tx := &doltTransaction{}
	if err := tx.SetConfig(context.Background(), "GITHUB.TOKEN", "synthetic"); err == nil || !strings.Contains(err.Error(), "config.yaml") {
		t.Fatalf("refusal = %v", err)
	}
}
