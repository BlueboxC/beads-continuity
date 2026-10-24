package main

import (
	"context"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage/graphstore"
)

func TestSchema8CopyCannotPublishIntoAWorkspace(t *testing.T) {
	receipt := graphConversionReceipt{Plan: graphConversionPlan{Store: graphstore.ConversionPlan{SourceSchema: 8}}}
	if err := runGraphConversionActivate(context.Background(), receipt, nil); err == nil || !strings.Contains(err.Error(), "rehearsal-only") {
		t.Fatalf("copy activation did not refuse before storage access: %v", err)
	}
	if err := runGraphConversionRollback(context.Background(), receipt, nil); err == nil || !strings.Contains(err.Error(), "never activates") {
		t.Fatalf("copy rollback did not refuse before storage access: %v", err)
	}
}
