//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type schema8Fixture struct {
	Statements []string
	Versions   map[string]string
}

func loadSchema8Fixture(t *testing.T) (context.Context, Options, schema8Fixture) {
	t.Helper()
	o := testOptions(t)
	o.Binding.WorkspaceID = filepath.Join(o.Binding.WorkspaceID, "bound-workspace")
	o.Binding.ScopeURL = "https://schema8.example.invalid/"
	if err := os.Mkdir(o.Binding.WorkspaceID, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(o.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	s, err := openBackend(t.Context(), o, "")
	if err != nil {
		t.Fatal(err)
	}
	conn, err := s.db.Conn(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(t.Context(), "CREATE DATABASE `"+o.Database+"`"); err != nil {
		t.Fatal(err)
	}
	if _, err = conn.ExecContext(t.Context(), "USE `"+o.Database+"`"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile("testdata/schema8-fixture.json")
	if err != nil {
		t.Fatal(err)
	}
	var f schema8Fixture
	if err = json.Unmarshal(raw, &f); err != nil {
		t.Fatal(err)
	}
	for _, statement := range f.Statements {
		statement = strings.ReplaceAll(statement, hex.EncodeToString([]byte("__WORKSPACE__")), hex.EncodeToString([]byte(o.Binding.WorkspaceID)))
		if _, err = conn.ExecContext(t.Context(), statement); err != nil {
			t.Fatalf("load fixture: %.240s", err)
		}
	}
	if err = conn.Close(); err != nil {
		t.Fatal(err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	return t.Context(), o, f
}

func TestSchema8HistoryConversionAndBackupRecovery(t *testing.T) {
	ctx, o, fixture := loadSchema8Fixture(t)
	plan, err := PlanSchema8Conversion(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	backup, candidate := filepath.Join(root, "backup"), filepath.Join(root, "candidate")
	if err = CheckpointConversion(ctx, o, plan, backup, candidate); err != nil {
		t.Fatal(err)
	}
	co := o
	co.DataDir = candidate
	if err = UpgradeSchema8Candidate(ctx, co, plan, "fixture"); err != nil {
		t.Fatal(err)
	}
	s, err := OpenExisting(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	for address, want := range fixture.Versions {
		path, version, _ := strings.Cut(address, "@")
		got, err := s.ReadOriginalVersion(ctx, path, version)
		if err != nil || got.SHA256 != want {
			t.Fatalf("original %s: %v", address, err)
		}
		gotRecord, err := s.ReadVersion(ctx, path, version)
		if err != nil {
			t.Fatal(err)
		}
		raw, err := canonicalJSON(gotRecord)
		if err != nil || conversionHash(raw) != want {
			t.Fatalf("typed history %s changed", address)
		}
		_, versions, err := s.Versions(ctx, path)
		if err != nil || !listsVersion(versions, version) {
			t.Fatalf("original token missing from listing: %s %v", address, err)
		}
		if compared, err := s.CompareVersions(ctx, path, version, version); err != nil || len(compared.Changes) != 0 {
			t.Fatalf("original self comparison: %s %v", address, err)
		}
	}
	// An internally consistent forged descriptor/hash cannot rewrite the
	// immutable source Type. The transaction is rolled back after each probe.
	for address := range fixture.Versions {
		path, version, _ := strings.Cut(address, "@")
		original, err := s.ReadOriginalVersion(ctx, path, version)
		if err != nil {
			t.Fatal(err)
		}
		var head struct{ Type string }
		if err := json.Unmarshal(original.Snapshot, &head); err != nil {
			t.Fatal(err)
		}
		forged := []byte(strings.Replace(string(original.Type), `"name":`, `"forged":true,"name":`, 1))
		if err := s.withTx(ctx, false, func(tx *sql.Tx) error {
			if _, err := tx.ExecContext(ctx, "UPDATE graph_preview_original_types SET descriptor=?,fingerprint=? WHERE name=?", forged, conversionHash(forged), originalTypeKey(head.Type, s.ScopeURL())); err != nil {
				return err
			}
			got, _, _, err := s.originalVersionInTx(ctx, tx, path, version)
			if !errors.Is(err, ErrInvalidStore) || !reflect.DeepEqual(got, OriginalVersion{}) {
				t.Fatalf("forged source Type was admitted: %v", err)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		break
	}
	if got, err := s.ReadProjectContinuity(ctx); err != nil || len(got.Memories.Memories) != 2 {
		t.Fatalf("continuity: %v", err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	after, err := PlanSchema8Conversion(ctx, o)
	if err != nil || !reflect.DeepEqual(plan, after) {
		t.Fatalf("source changed: %v", err)
	}
	if err = UpgradeSchema8Candidate(ctx, co, plan, "fixture"); err == nil {
		t.Fatal("repeat conversion admitted")
	}
}

func TestSchema8ConversionFaultKeepsSourceAndRestorableCheckpoint(t *testing.T) {
	ctx, o, _ := loadSchema8Fixture(t)
	plan, err := PlanSchema8Conversion(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	co := o
	co.DataDir = filepath.Join(root, "candidate")
	if err = CheckpointConversion(ctx, o, plan, filepath.Join(root, "backup"), co.DataDir); err != nil {
		t.Fatal(err)
	}
	fault := errors.New("conversion fault before commit")
	err = upgradeSchema8Candidate(ctx, co, plan, "fixture", func(stage string) error {
		if stage == "conversion-history-verified" {
			return fault
		}
		return nil
	})
	if !errors.Is(err, fault) {
		t.Fatalf("fault: %v", err)
	}
	after, err := PlanSchema8Conversion(ctx, o)
	if err != nil || !reflect.DeepEqual(plan, after) {
		t.Fatalf("fault changed source: %v", err)
	}
	s, err := openBackend(ctx, co, co.Database)
	if err != nil {
		t.Fatal(err)
	}
	var count, schema int
	if err = s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM graph_preview_original_versions").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if err = s.db.QueryRowContext(ctx, "SELECT schema_version FROM graph_preview_scope").Scan(&schema); err != nil {
		t.Fatal(err)
	}
	if count != 0 || schema != 8 {
		t.Fatalf("partial transaction committed: %d schema=%d", count, schema)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	if err = CheckpointConversion(ctx, o, plan, filepath.Join(root, "recovery-backup"), filepath.Join(root, "recovered")); err != nil {
		t.Fatal(err)
	}
}

func TestSchema8HistoricalCompositionRetainsOriginalBytes(t *testing.T) {
	ctx, o, fixture := loadSchema8Fixture(t)
	plan, err := PlanSchema8Conversion(ctx, o)
	if err != nil {
		t.Fatal(err)
	}
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	co := o
	co.DataDir = filepath.Join(root, "candidate")
	if err = CheckpointConversion(ctx, o, plan, filepath.Join(root, "backup"), co.DataDir); err != nil {
		t.Fatal(err)
	}
	if err = UpgradeSchema8Candidate(ctx, co, plan, "fixture"); err != nil {
		t.Fatal(err)
	}
	s, err := OpenExisting(ctx, co)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Error(err)
		}
	})
	selected := map[string]string{}
	for address := range fixture.Versions {
		path, revision, _ := strings.Cut(address, "@")
		selected[path] = revision
	}
	later := "later"
	updated, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/a", Body: &later, Unconditional: true, Actor: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	restored, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/a", FromRevision: selected["beads/a"], ExpectedRevision: updated.Memory.Revision, Actor: "fixture"})
	if err != nil || restored.Memory.Properties.Body != "body" {
		t.Fatalf("Memory source: %v", err)
	}
	updatedIssue, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/first", Title: &later, Unconditional: true, Actor: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	restoredIssue, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/first", FromRevision: selected["beads/first"], ExpectedRevision: updatedIssue.Issue.Revision, Actor: "fixture"})
	if err != nil || restoredIssue.Issue.Properties.Title != "first" {
		t.Fatalf("Issue source: %v", err)
	}
	value, err := s.Read(ctx, "links/native")
	if err != nil {
		t.Fatal(err)
	}
	link := value.(LinkRecord)
	updatedLink, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/native", MetadataOnly: true, Metadata: nativeMetadata(`{"later":true}`), ExpectedRevision: link.Revision, ExpectedSourceRevision: restoredIssue.Issue.Revision, Actor: "fixture"})
	if err != nil {
		t.Fatal(err)
	}
	restoredLink, err := s.UpdateLink(ctx, LinkUpdateRequest{Path: "links/native", FromRevision: selected["links/native"], ExpectedRevision: updatedLink.Link.Revision, ExpectedSourceRevision: updatedLink.Source.(IssueRecord).Revision, Actor: "fixture"})
	if err != nil || string(restoredLink.Link.Metadata) != "{}" || restoredLink.Link.Properties["threadId"] != "retained-thread" {
		t.Fatalf("Link source: %v", err)
	}
	for path, revision := range selected {
		got, err := s.ReadOriginalVersion(ctx, path, revision)
		if err != nil || got.SHA256 != fixture.Versions[path+"@"+revision] {
			t.Fatalf("historical edit changed original %s: %v", path, err)
		}
	}
}

func TestConversionCopyPathPhysicalOverlap(t *testing.T) {
	root, err := filepath.EvalSymlinks(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	protected := filepath.Join(root, "bound-workspace")
	if err = os.Mkdir(protected, 0700); err != nil {
		t.Fatal(err)
	}
	alias := filepath.Join(root, "workspace-alias")
	if err = os.Symlink(protected, alias); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if !ConversionPathsOverlap(filepath.Join(alias, "future-output"), protected) {
		t.Fatal("physical ancestor alias admitted")
	}
	if ConversionPathsOverlap(filepath.Join(root, "separate-output"), protected) {
		t.Fatal("independent sibling refused")
	}
}
