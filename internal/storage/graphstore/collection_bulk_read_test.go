//go:build cgo

package graphstore

import (
	"errors"
	"fmt"
	"reflect"
	"testing"

	"github.com/steveyegge/beads/internal/types"
)

func TestSnapshotBulkMatchesIndividualReads(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		for _, version := range []int{SchemaVersion} {
			t.Run(fmt.Sprintf("%s/%d", backend, version), func(t *testing.T) {
				ctx, o := issueExperimentOptions(t, backend)
				s := openVersionsStore(t, ctx, o)
				memory, err := s.Create(ctx, CreateRequest{Title: "Memory", Path: "beads/memory", Body: "prior solution"})
				if err != nil {
					t.Fatal(err)
				}
				if _, err := s.CreateIssue(ctx, "beads/source", plainIssue("Source")); err != nil {
					t.Fatal(err)
				}
				kinds := []types.DependencyType{types.DepBlocks}
				if version == SchemaVersion {
					kinds = append(kinds, types.DepParentChild, types.DepDiscoveredFrom, types.DepRelated)
				}
				for i, typ := range kinds {
					path := fmt.Sprintf("beads/target-%d", i)
					if _, err := s.CreateIssue(ctx, path, plainIssue("Target")); err != nil {
						t.Fatal(err)
					}
					if _, err := s.AddDependency(ctx, DependencyRequest{SourcePath: "beads/source", TargetPath: path, Type: typ, Actor: "fixture"}); err != nil {
						t.Fatal(err)
					}
				}
				if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{TypeURL: s.ScopeURL() + "types/preview-related-v2", Path: "links/memory-owned", SourcePath: "beads/memory", TargetPath: "beads/source", ExpectedSourceRevision: memory.Revision, Actor: "fixture"}); err != nil {
					t.Fatal(err)
				}
				if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{TypeURL: s.ScopeURL() + "types/preview-related-v2", Path: "links/issue-independent", SourcePath: "beads/source", TargetPath: "beads/memory", Actor: "fixture"}); err != nil {
					t.Fatal(err)
				}
				snap, err := s.CurrentSnapshot(ctx)
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range snap.Records {
					var id string
					switch r := record.(type) {
					case Record:
						id = r.ID
					case IssueRecord:
						id = r.ID
					case LinkRecord:
						id = r.ID
					default:
						t.Fatalf("unexpected %T", record)
					}
					single, err := s.Read(ctx, id)
					if err != nil || !reflect.DeepEqual(single, record) {
						t.Fatalf("bulk and individual disagree for %s: %v", id, err)
					}
				}
			})
		}
	}
}

func TestSnapshotBulkRefusesNativeCorruption(t *testing.T) {
	cases := map[string]string{
		"metadata":         `UPDATE dependencies SET metadata='{"unexpected":true}'`,
		"thread":           `UPDATE dependencies SET thread_id='unversioned'`,
		"type":             `UPDATE dependencies SET type='related'`,
		"missing-retained": `DELETE FROM graph_preview_versions WHERE path LIKE 'links/%'`,
		"missing-endpoint": `DELETE FROM graph_preview_catalog WHERE path='beads/target'`,
		"missing-owned":    `UPDATE graph_preview_issue_versions SET owned='[]' WHERE path='beads/source'`,
	}
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, o := issueExperimentOptions(t, backend)
			s := openVersionsStore(t, ctx, o)
			for _, p := range []string{"source", "target"} {
				if _, err := s.CreateIssue(ctx, "beads/"+p, plainIssue(p)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.AddDependency(ctx, DependencyRequest{SourcePath: "beads/source", TargetPath: "beads/target", Actor: "fixture"}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CurrentSnapshot(ctx); err != nil {
				t.Fatal(err)
			}
			for name, statement := range cases {
				t.Run(name, func(t *testing.T) {
					tx, err := s.db.BeginTx(ctx, nil)
					if err != nil {
						t.Fatal(err)
					}
					defer func() { _ = tx.Rollback() }()
					if _, err := tx.ExecContext(ctx, statement); err != nil {
						t.Fatal(err)
					}
					snap, err := s.currentSnapshotInTx(ctx, tx)
					if !errors.Is(err, ErrInvalidStore) || !reflect.DeepEqual(snap, Snapshot{}) {
						t.Fatalf("corruption emitted a snapshot: %v", err)
					}
				})
			}
		})
	}
}
