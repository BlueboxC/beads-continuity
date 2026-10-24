//go:build cgo

package graphstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestNativeConcreteTypesCurrentAndRetained(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			source, err := s.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil {
				t.Fatal(err)
			}
			target, err := s.CreateIssue(ctx, "beads/target", plainIssue("Target"))
			if err != nil {
				t.Fatal(err)
			}
			task, _ := NativeIssueTypeURL(s.ScopeURL(), "task")
			bug, _ := NativeIssueTypeURL(s.ScopeURL(), "bug")
			if source.Type != task || target.Type != task || source.Properties.IssueType != types.TypeTask {
				t.Fatalf("default actual affiliation: %+v", source)
			}
			for index, kind := range []types.DependencyType{types.DepBlocks, "unfamiliar/é"} {
				result, err := s.AddDependency(ctx, DependencyRequest{Path: fmt.Sprintf("links/native-%d", index), SourcePath: "beads/work", TargetPath: "beads/target", Type: kind, Actor: "tester"})
				if err != nil {
					t.Fatal(err)
				}
				id, _ := NativeDependencyTypeURL(s.ScopeURL(), string(kind))
				if result.Link.Type != id || result.Link.Properties["dep_type"] != string(kind) {
					t.Fatalf("kind mapping: %+v", result)
				}
				if _, err := s.CurrentSnapshot(ctx); err != nil {
					t.Fatal(err)
				}
				// Native pair removal owns only blocks. Unknown kinds remain live,
				// including across the following Issue reclassification.
				before := result.Link
				removed, err := s.RemoveDependency(ctx, DependencyRemoveRequest{SourcePath: "beads/work", TargetPath: "beads/target", Actor: "tester"})
				if err != nil {
					t.Fatal(err)
				}
				if removed.Removed != (kind == types.DepBlocks) {
					t.Fatal("native pair-removal semantics changed")
				}
				retained, err := s.ReadVersion(ctx, fmt.Sprintf("links/native-%d", index), before.Revision)
				if err != nil || !reflect.DeepEqual(retained, before) {
					t.Fatalf("retained native Link: %+v %v", retained, err)
				}

			}
			current, err := s.ShowIssue(ctx, "beads/work")
			if err != nil {
				t.Fatal(err)
			}
			changed, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/work", Actor: "tester", ExpectedRevision: current.Revision, TypeURL: bug})
			if err != nil {
				t.Fatal(err)
			}
			if changed.Issue.Type != bug || changed.Issue.Properties.IssueType != types.TypeBug {
				t.Fatalf("reclassification not authoritative: %+v", changed)
			}
			assertIssueEditVersion(t, ctx, s, "beads/work", source)
			restored, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/work", Actor: "tester", ExpectedRevision: changed.Issue.Revision, FromRevision: source.Revision})
			if err != nil || restored.Issue.Type != task {
				t.Fatalf("historical affiliation restore: %+v %v", restored, err)
			}
			assertIssueEditVersion(t, ctx, s, "beads/work", changed.Issue)
		})
	}
}

func TestNativeConcreteTypesRefuseAbstractAndContradictoryInputs(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			source, err := s.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil {
				t.Fatal(err)
			}
			_, err = s.CreateIssue(ctx, "beads/target", plainIssue("Target"))
			if err != nil {
				t.Fatal(err)
			}
			before := workflowState(t, ctx, s)
			bug, _ := NativeIssueTypeURL(s.ScopeURL(), "bug")
			task := types.TypeTask
			if _, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/work", Actor: "tester", ExpectedRevision: source.Revision, TypeURL: bug, IssueType: &task}); err == nil {
				t.Fatal("contradictory native classification accepted")
			}
			if _, err = s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/work", Actor: "tester", ExpectedRevision: source.Revision, TypeURL: IssueTypeURL(s.ScopeURL())}); err == nil {
				t.Fatal("abstract native retype accepted")
			}
			if _, err = s.Create(ctx, CreateRequest{Path: "beads/abstract", TypeURL: IssueTypeURL(s.ScopeURL()), PropertiesJSON: json.RawMessage(`{"title":"invalid"}`)}); err == nil {
				t.Fatal("abstract create accepted")
			}
			if _, err = s.AddDependency(ctx, DependencyRequest{SourcePath: "beads/work", TargetPath: "beads/target", TypeURL: DependencyTypeURL(s.ScopeURL()), Actor: "tester"}); !errors.Is(err, ErrCapabilityUnavailable) {
				t.Fatalf("abstract Dependency admitted: %v", err)
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("refusal changed native, catalog, retained or owned state")
			}
		})
	}
}

func TestNativeConcreteTypesRetainCustomBindingsAndRollback(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			const classification = "review-class"
			setConfig := func(value string) {
				t.Helper()
				if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
					_, err := tx.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES ('types.custom',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", value)
					return err
				}); err != nil {
					t.Fatal(err)
				}
			}
			setConfig(classification)
			request := plainIssue("Custom native Issue")
			request.Issue.IssueType = types.IssueType(classification)
			before := workflowState(t, ctx, s)
			fault := errors.New("after prospective native binding")
			s.afterWrite = func(stage string) error {
				if stage == "issue-catalog" {
					return fault
				}
				return nil
			}
			_, err = s.CreateIssue(ctx, "beads/custom", request)
			s.afterWrite = nil
			if !errors.Is(err, fault) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("native binding escaped rollback: %v", err)
			}
			created, err := s.CreateIssue(ctx, "beads/custom", request)
			if err != nil {
				t.Fatal(err)
			}
			customType, _ := NativeIssueTypeURL(s.ScopeURL(), classification)
			if created.Type != customType {
				t.Fatalf("custom affiliation lost: %s", created.Type)
			}
			bug, _ := NativeIssueTypeURL(s.ScopeURL(), "bug")
			changed, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/custom", Actor: "editor", ExpectedRevision: created.Revision, TypeURL: bug})
			if err != nil {
				t.Fatal(err)
			}
			setConfig("")
			beforeReads := workflowState(t, ctx, s)
			descriptor, err := s.ReadType(ctx, strings.TrimPrefix(customType, s.ScopeURL()))
			if err != nil || descriptor.ID() != customType {
				t.Fatalf("retained custom descriptor lost after config removal: %s %v", descriptor.ID(), err)
			}
			descriptors, err := s.ListInstalledTypes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			found := false
			for _, d := range descriptors {
				found = found || d.ID() == customType
			}
			if !found {
				t.Fatal("used native Type disappeared from inventory")
			}
			assertIssueEditVersion(t, ctx, s, "beads/custom", created)
			if !reflect.DeepEqual(beforeReads, workflowState(t, ctx, s)) {
				t.Fatal("descriptor/history reads registered or changed native state")
			}
			before = workflowState(t, ctx, s)
			if _, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/custom", Actor: "editor", ExpectedRevision: changed.Issue.Revision, TypeURL: customType}); err == nil {
				t.Fatal("retained descriptor bypassed current native custom authorization")
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("refused reclassification changed state")
			}
			if _, err := s.CreateIssue(ctx, "beads/new-custom", request); err == nil {
				t.Fatal("retained descriptor authorized a new custom Issue after config removal")
			}
			if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatal("refused custom creation changed state")
			}
		})
	}
}

func TestNativeConcreteTypeFamiliesAndAllocationCollision(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, path := range []string{"beads/a", "beads/b", "beads/c"} {
				request := plainIssue(path)
				if path == "beads/c" {
					request.Issue.IssueType = types.TypeBug
				}
				if _, err := s.CreateIssue(ctx, path, request); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/memory", Title: "Memory"}); err != nil {
				t.Fatal(err)
			}
			task, _ := NativeIssueTypeURL(s.ScopeURL(), "task")
			for _, tc := range []struct {
				exact, family string
				want          int
			}{{"", IssueTypeURL(s.ScopeURL()), 3}, {IssueTypeURL(s.ScopeURL()), "", 0}, {task, "", 2}, {"", task, 2}, {"", "", 4}} {
				page, err := s.ListBeads(ctx, BeadListRequest{TypeURL: tc.exact, ConformsTo: tc.family, All: true})
				if err != nil || len(page.Items) != tc.want {
					t.Fatalf("Bead family/exact: %+v count=%d err=%v", tc, len(page.Items), err)
				}
			}
			for index, kind := range []types.DependencyType{types.DepBlocks, "retained-kind"} {
				target := "beads/b"
				if index == 1 {
					target = "beads/c"
				}
				if _, err := s.AddDependency(ctx, DependencyRequest{Path: fmt.Sprintf("links/family-%d", index), SourcePath: "beads/a", TargetPath: target, Type: kind, Actor: "tester"}); err != nil {
					t.Fatal(err)
				}
			}
			concrete, _ := NativeDependencyTypeURL(s.ScopeURL(), "retained-kind")
			for _, tc := range []struct {
				exact, family string
				want          int
			}{{"", DependencyTypeURL(s.ScopeURL()), 2}, {DependencyTypeURL(s.ScopeURL()), "", 0}, {concrete, "", 1}, {"", concrete, 1}} {
				links, err := s.CollectLinks(ctx, LinkCollectionRequest{TypeURL: tc.exact, ConformsTo: tc.family})
				if err != nil || len(links) != tc.want {
					t.Fatalf("Link family/exact: %+v count=%d err=%v", tc, len(links), err)
				}
			}
			for _, path := range []string{"beads/collision-a", "beads/collision-b"} {
				if _, err := s.CreateIssue(ctx, path, plainIssue(path)); err != nil {
					t.Fatal(err)
				}
			}
			// Even a tombstone reserves an identity before a prospective native Type
			// binding. The collision fixture is private SQL, never a data migration.
			for _, state := range []string{"live", "deleted"} {
				id, _ := NativeDependencyTypeURL(s.ScopeURL(), "allocated-"+state)
				path := strings.TrimPrefix(id, s.ScopeURL())
				if _, err := s.db.ExecContext(ctx, `INSERT INTO graph_preview_catalog (path,resource_kind,type_url,revision,allocation_state,backing) VALUES (?,'bead',?,? ,?,'generic')`, path, MemoryTypeURL(s.ScopeURL()), strings.Repeat("a", 32), state); err != nil {
					t.Fatal(err)
				}
				before := workflowState(t, ctx, s)
				_, err := s.AddDependency(ctx, DependencyRequest{Path: "links/collision-" + state, SourcePath: "beads/collision-a", TargetPath: "beads/collision-b", Type: types.DependencyType("allocated-" + state), Actor: "tester"})
				if !errors.Is(err, ErrAlreadyExists) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("%s collision mutated state: %v", state, err)
				}
				if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_catalog WHERE path=?`, path); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
}

func TestNativeConcreteTypeBindingCorruptionRefusesWithoutWrites(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, path := range []string{"beads/a", "beads/b"} {
				if _, err := s.CreateIssue(ctx, path, plainIssue(path)); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.AddDependency(ctx, DependencyRequest{Path: "links/custom", SourcePath: "beads/a", TargetPath: "beads/b", Type: "custom-kind", Actor: "tester"}); err != nil {
				t.Fatal(err)
			}
			id, _ := NativeDependencyTypeURL(s.ScopeURL(), "custom-kind")
			expected, err := nativeConcreteDescriptor(s.ScopeURL(), id)
			if err != nil {
				t.Fatal(err)
			}
			var key string
			if err := s.db.QueryRowContext(ctx, `SELECT name FROM graph_preview_types WHERE fingerprint=?`, expected.Fingerprint()).Scan(&key); err != nil {
				t.Fatal(err)
			}
			for _, corruption := range []string{"malformed", "oversized", "fingerprint", "wrong-namespace", "wrong-kind", "noncanonical-identity", "unknown-key", "duplicate"} {
				t.Run(corruption, func(t *testing.T) {
					raw, fp := expected.CanonicalJSON(), expected.Fingerprint()
					rowKey := key
					switch corruption {
					case "malformed":
						raw = []byte("{")
					case "oversized":
						raw = []byte(strings.Repeat("x", 20000))
					case "fingerprint":
						fp = strings.Repeat("0", 64)
					case "wrong-namespace":
						raw = []byte(strings.Replace(string(raw), "types/native-dependency/", "types/unregistered/", 1))
					case "wrong-kind":
						raw = []byte(strings.Replace(string(raw), `"describes":"link"`, `"describes":"bead"`, 1))
					case "noncanonical-identity":
						raw = []byte(strings.Replace(string(raw), "637573746f6d2d6b696e64", "637573746F6d2d6b696e64", 1))
					case "unknown-key":
						rowKey = "unregistered-extra"
					case "duplicate":
						if _, err := s.db.ExecContext(ctx, `INSERT INTO graph_preview_types (name,descriptor,fingerprint) VALUES (?,?,?)`, "native-binding-"+strings.Repeat("f", 32), raw, fp); err != nil {
							t.Fatal(err)
						}
					}
					if corruption != "duplicate" {
						if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_types SET name=?,descriptor=?,fingerprint=? WHERE name=?`, rowKey, raw, fp, key); err != nil {
							t.Fatal(err)
						}
					}
					before := workflowState(t, ctx, s)
					if _, err := s.ListInstalledTypes(ctx); !errors.Is(err, ErrInvalidStore) {
						t.Fatalf("%s catalog accepted: %v", corruption, err)
					}
					if !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatal("refused catalog read mutated state")
					}
					if corruption == "duplicate" {
						if _, err := s.db.ExecContext(ctx, `DELETE FROM graph_preview_types WHERE name=?`, "native-binding-"+strings.Repeat("f", 32)); err != nil {
							t.Fatal(err)
						}
					} else {
						if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_types SET name=?,descriptor=?,fingerprint=? WHERE name=?`, key, expected.CanonicalJSON(), expected.Fingerprint(), rowKey); err != nil {
							t.Fatal(err)
						}
					}
				})
			}
		})
	}
}

// This composes the native binding seam with the common public-ID adapter.
// A Type ID is reserved by a Resource even when its private locator differs.
func TestNativeConcreteTypePublicIdentityCollision(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			for _, path := range []string{"beads/native-public-a", "beads/native-public-b"} {
				if _, err := s.CreateIssue(ctx, path, plainIssue(path)); err != nil {
					t.Fatal(err)
				}
			}
			for _, family := range []string{"issue", "dependency"} {
				for _, state := range []string{"live", "deleted"} {
					classification := family + "-collision-" + state
					id, err := NativeDependencyTypeURL(s.ScopeURL(), classification)
					if family == "issue" {
						id, err = NativeIssueTypeURL(s.ScopeURL(), classification)
					}
					if err != nil {
						t.Fatal(err)
					}
					publicID := strings.TrimPrefix(id, s.ScopeURL())
					memory, err := s.Create(ctx, CreateRequest{PublicID: publicID, Title: "Public native Type collision"})
					if err != nil {
						t.Fatal(err)
					}
					privatePath, err := s.ResolveExactAllocatedResourcePath(ctx, publicID)
					if err != nil || privatePath == publicID || memory.ID != id {
						t.Fatalf("fixture failed to separate public ID and locator: %s %s %+v %v", publicID, privatePath, memory, err)
					}
					if state == "deleted" {
						if _, err := s.DeleteMemory(ctx, MemoryDeleteRequest{Path: privatePath, ExpectedRevision: memory.Revision}); err != nil {
							t.Fatal(err)
						}
					}
					if family == "issue" {
						if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
							_, err := tx.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES ('types.custom',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", classification)
							return err
						}); err != nil {
							t.Fatal(err)
						}
					}
					before := workflowState(t, ctx, s)
					if family == "issue" {
						request := plainIssue("Refused prospective native Type")
						request.Issue.IssueType = types.IssueType(classification)
						_, err = s.CreateIssue(ctx, "beads/refused-"+classification, request)
					} else {
						_, err = s.AddDependency(ctx, DependencyRequest{Path: "links/refused-" + classification, SourcePath: "beads/native-public-a", TargetPath: "beads/native-public-b", Type: types.DependencyType(classification), Actor: "collision-tester"})
					}
					if !errors.Is(err, ErrAlreadyExists) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatalf("%s/%s public-ID collision changed state: %v", family, state, err)
					}
				}
			}
		})
	}
}

func TestNativeConcreteTypesProspectiveIdentityReservations(t *testing.T) {
	for _, engine := range []string{"embedded", "server"} {
		t.Run(engine, func(t *testing.T) {
			ctx, _, s := memoryDeleteStore(t, engine)
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES ('types.custom',?) ON DUPLICATE KEY UPDATE value=VALUES(value)", "self-class,cross-left,cross-right")
				return err
			}); err != nil {
				t.Fatal(err)
			}
			selfType, _ := NativeIssueTypeURL(s.ScopeURL(), "self-class")
			self := plainIssue("Cannot share identity with own Type")
			self.Issue.ID = strings.TrimPrefix(selfType, s.ScopeURL())
			self.Issue.IssueType = types.IssueType("self-class")
			self.ForceIDPrefix = true
			before := workflowState(t, ctx, s)
			if _, err := s.CreateIssue(ctx, "", self); !errors.Is(err, ErrAlreadyExists) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("Issue acquired prospective own Type identity or left state: %v", err)
			}
			for _, path := range []string{"beads/reservation-a", "beads/reservation-b", "beads/reservation-c"} {
				if _, err := s.CreateIssue(ctx, path, plainIssue(path)); err != nil {
					t.Fatal(err)
				}
			}
			selfLinkType, _ := NativeDependencyTypeURL(s.ScopeURL(), "self-edge")
			before = workflowState(t, ctx, s)
			_, err := s.AddDependency(ctx, DependencyRequest{PublicID: strings.TrimPrefix(selfLinkType, s.ScopeURL()), SourcePath: "beads/reservation-a", TargetPath: "beads/reservation-b", Type: types.DependencyType("self-edge"), Actor: "tester"})
			if !errors.Is(err, ErrAlreadyExists) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("Dependency acquired prospective own Type identity or left state: %v", err)
			}
			leftType, _ := NativeIssueTypeURL(s.ScopeURL(), "cross-left")
			rightType, _ := NativeIssueTypeURL(s.ScopeURL(), "cross-right")
			for direction := 0; direction < 2; direction++ {
				left, right := plainIssue("Left").Issue, plainIssue("Right").Issue
				left.IssueType, right.IssueType = types.IssueType("cross-left"), types.IssueType("cross-right")
				left.ID, right.ID = "batch-left", "batch-right"
				if direction == 0 {
					left.ID = strings.TrimPrefix(rightType, s.ScopeURL())
				} else {
					right.ID = strings.TrimPrefix(leftType, s.ScopeURL())
				}
				before := workflowState(t, ctx, s)
				records, err := s.CreateIssueBatch(ctx, publicops.CreateBatchRequest{Actor: "tester", ForceIDPrefix: true, Items: []publicops.BatchCreateItem{{Issue: left}, {Issue: right}}})
				if !errors.Is(err, ErrAlreadyExists) || records != nil || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("batch direction %d acquired another item's Type or left state: %v", direction, err)
				}
			}
			// A raw nominal URI is recognition, not permission to register an
			// unseen kind. Native-kind authoring can establish its binding first.
			const kind = types.DependencyType("retained-custom-edge")
			declared, _ := NativeDependencyTypeURL(s.ScopeURL(), string(kind))
			request := DependencyRequest{Path: "links/custom-edge", SourcePath: "beads/reservation-a", TargetPath: "beads/reservation-b", Type: kind, TypeURL: declared, Actor: "tester"}
			before = workflowState(t, ctx, s)
			if _, err := s.AddDependency(ctx, request); !errors.Is(err, ErrCapabilityUnavailable) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("unseen nominal URI bypassed native Type admission: %v", err)
			}
			request.TypeURL = ""
			created, err := s.AddDependency(ctx, request)
			if err != nil || created.Link.Type != declared {
				t.Fatalf("ordinary valid native kind failed to establish concrete affiliation: %+v %v", created, err)
			}
			request.Path, request.TargetPath, request.TypeURL = "links/custom-edge-second", "beads/reservation-c", declared
			second, err := s.AddDependency(ctx, request)
			if err != nil || second.Link.Type != declared {
				t.Fatalf("registered nominal Type disagreed with native kind: %+v %v", second, err)
			}
			request.Path, request.Type = "links/uri-only", ""
			before = workflowState(t, ctx, s)
			if _, err := s.AddDependency(ctx, request); !errors.Is(err, ErrCapabilityUnavailable) || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("nominal URI silently replaced default native kind: %v", err)
			}
		})
	}
}
