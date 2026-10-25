//go:build cgo && !windows

package main

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

func TestSchema8WorkspaceInterruptedPublicationAndRecovery(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	workspace := filepath.Join(root, "project", ".beads")
	if err := os.MkdirAll(workspace, 0700); err != nil {
		t.Fatal(err)
	}
	engine := filepath.Join(workspace, "embeddeddolt")
	if err := os.Mkdir(engine, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Minute)
	defer cancel()
	db, closeDB, err := embeddeddolt.OpenSQL(ctx, engine, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.ExecContext(ctx, "CREATE DATABASE graph"); err != nil {
		t.Fatal(err)
	}
	if err = closeDB(); err != nil {
		t.Fatal(err)
	}
	db, closeDB, err = embeddeddolt.OpenSQL(ctx, engine, "graph", "")
	if err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("../../internal/storage/graphstore/testdata/schema8-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var fixture struct{ Statements []string }
	if err = json.Unmarshal(raw, &fixture); err != nil {
		t.Fatal(err)
	}
	for _, q := range fixture.Statements {
		q = strings.ReplaceAll(q, hex.EncodeToString([]byte("__WORKSPACE__")), hex.EncodeToString([]byte(workspace)))
		if _, err = db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if err = closeDB(); err != nil {
		t.Fatal(err)
	}
	// Read the binding from the fixture rather than inventing another authority.
	var authority string
	db, closeDB, err = embeddeddolt.OpenSQL(ctx, engine, "graph", "")
	if err != nil {
		t.Fatal(err)
	}
	if err = db.QueryRowContext(ctx, "SELECT authority_id FROM graph_preview_scope").Scan(&authority); err != nil {
		t.Fatal(err)
	}
	if err = closeDB(); err != nil {
		t.Fatal(err)
	}
	metadata := map[string]any{"backend": "dolt", "dolt_mode": "embedded", "dolt_database": "graph", "graph_mode": "link", "graph_schema_version": 8, "graph_ready": true, "graph_scope_url": "https://schema8.example.invalid/", "graph_authority_id": authority, "graph_workspace": workspace}
	if err = graphConversionWriteNew(filepath.Join(workspace, "metadata.json"), metadata); err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(workspace, graphPreviewMarker), []byte("link-preview-v7\n"), 0600); err != nil {
		t.Fatal(err)
	}
	planFile := filepath.Join(root, "plan.json")
	plan, err := runGraphConversionPlan(conversionFixtureCommand(map[string]string{"workspace": workspace, "scope-url": "https://schema8.example.invalid/", "out": planFile}))
	if err != nil {
		t.Fatal(err)
	}
	receipt, err := runGraphConversionPrepare(conversionFixtureCommand(map[string]string{"plan": planFile, "out": filepath.Join(root, "rehearsal"), "actor": "test-owner"}, "writers-stopped"))
	if err != nil {
		t.Fatal(err)
	}
	fault := errors.New("interrupted")
	for _, step := range []string{"intent", "candidate-staged", "fenced", "native-retained", "graph-installed", "selector-published", "format-published"} {
		err = runGraphConversionActivate(ctx, receipt, func(got string) error {
			if got == step {
				return fault
			}
			return nil
		})
		if !errors.Is(err, fault) {
			t.Fatalf("%s: %v", step, err)
		}
	}
	if err = runGraphConversionActivate(ctx, receipt, nil); err != nil {
		t.Fatal(err)
	}
	if _, err = graphConversionVerifyReceipt(ctx, receipt); err != nil {
		t.Fatal(err)
	}
	for _, step := range []string{"rollback-intent", "graph-retained", "native-restored", "native-selector-restored", "native-format-restored"} {
		err = runGraphConversionRollback(ctx, receipt, func(got string) error {
			if got == step {
				return fault
			}
			return nil
		})
		if !errors.Is(err, fault) {
			t.Fatalf("%s: %v", step, err)
		}
	}
	if err = runGraphConversionRollback(ctx, receipt, nil); err != nil {
		t.Fatal(err)
	}
	current, err := graphstore.PlanSchema8WorkspaceConversion(ctx, graphConversionOptions(plan))
	if err != nil || current.State.Head != plan.Store.State.Head {
		t.Fatalf("recovery: %v", err)
	}
	marker, err := os.ReadFile(filepath.Join(workspace, graphPreviewMarker))
	if err != nil || string(marker) != "link-preview-v7\n" {
		t.Fatalf("source marker recovery: %v", err)
	}
	o := graphConversionOptions(plan)
	if _, err = graphstore.PlanSchema8Conversion(context.Background(), o); err == nil {
		t.Fatal("copy-only planner admitted live source")
	}
}
