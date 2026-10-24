//go:build cgo

package graphstore

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
)

func TestSelectedMemoryTypeRetypePreservesPropertiesAndHistory(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			policyType, specType := s.ScopeURL()+"types/policy", s.ScopeURL()+"types/specification"
			before, err := s.Create(ctx, CreateRequest{Path: "beads/rule", TypeURL: policyType,
				PropertiesJSON: json.RawMessage(`{"title":"Rule","body":"Keep data private","status":"draft","authority":{"uri":"urn:example:authority","revision":"r1"},"extension":{"team":"blue"}}`)})
			if err != nil {
				t.Fatal(err)
			}
			changed, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/rule", TypeURL: specType, ExpectedRevision: before.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if !changed.Changed || changed.Memory.Type != specType || changed.Memory.ID != before.ID || changed.Memory.Revision == before.Revision || changed.Memory.Properties != before.Properties {
				t.Fatalf("retype lost identity/properties or failed to revise: %+v", changed)
			}
			retained, err := s.ReadVersion(ctx, "beads/rule", before.Revision)
			if err != nil || !reflect.DeepEqual(retained, before) {
				t.Fatalf("historical original Type was reinterpreted: %+v %v", retained, err)
			}
			witness := workflowState(t, ctx, s)
			noop, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/rule", TypeURL: specType, ExpectedRevision: changed.Memory.Revision})
			if err != nil || noop.Changed || !reflect.DeepEqual(noop.Memory, changed.Memory) {
				t.Fatalf("same Type no-op: %+v %v", noop, err)
			}
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/rule", TypeURL: policyType, ExpectedRevision: before.Revision}); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale retype admitted: %v", err)
			}
			if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
				t.Fatal("no-op or stale retype wrote state")
			}
		})
	}
}

func TestSelectedMemoryRetypeRefusesInvalidIncidentLinksAtomically(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			constraintType := s.ScopeURL() + "types/constraint"
			source, err := s.Create(ctx, CreateRequest{Path: "beads/source", TypeURL: constraintType, Title: "Source"})
			if err != nil {
				t.Fatal(err)
			}
			target, err := s.Create(ctx, CreateRequest{Path: "beads/target", TypeURL: constraintType, Title: "Target"})
			if err != nil {
				t.Fatal(err)
			}
			added, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/requires", TypeURL: s.ScopeURL() + "types/requires", SourcePath: "beads/source", TargetPath: "beads/target", ExpectedSourceRevision: source.Revision})
			if err != nil {
				t.Fatal(err)
			}
			for _, candidate := range []Record{added.Source.(Record), target} {
				path := "beads/source"
				if candidate.ID == target.ID {
					path = "beads/target"
				}
				witness := workflowState(t, ctx, s)
				title := "Sibling edit must roll back"
				if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: path, TypeURL: MemoryTypeURL(s.ScopeURL()), Title: &title, ExpectedRevision: candidate.Revision}); !errors.Is(err, storage.ErrValidation) {
					t.Fatalf("invalid incident endpoint retype: %v", err)
				}
				if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
					t.Fatal("invalid retype changed payload/Type/revisions/retention/coordination")
				}
			}
		})
	}
}

func TestRetypeValidatesCombinedPropertyEdits(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			subject, err := s.Create(ctx, CreateRequest{Path: "beads/subject", Title: "Subject"})
			if err != nil {
				t.Fatal(err)
			}
			before, err := s.Create(ctx, CreateRequest{Path: "beads/assessment", Title: "Assessment", Body: "Existing explanation"})
			if err != nil {
				t.Fatal(err)
			}
			evaluationType := s.ScopeURL() + "types/evaluation"
			witness := workflowState(t, ctx, s)
			if _, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/assessment", TypeURL: evaluationType, ExpectedRevision: before.Revision}); !errors.Is(err, storage.ErrValidation) {
				t.Fatalf("missing required subjects admitted: %v", err)
			}
			if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
				t.Fatal("invalid final payload changed state")
			}
			merge, err := json.Marshal(map[string]any{"subjects": []any{map[string]string{"uri": subject.ID, "revision": subject.Revision}}})
			if err != nil {
				t.Fatal(err)
			}
			accepted, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/assessment", TypeURL: evaluationType, PropertiesMerge: merge, ExpectedRevision: before.Revision})
			if err != nil {
				t.Fatal(err)
			}
			if !accepted.Changed || accepted.Memory.Type != evaluationType || accepted.Memory.Properties.Title != before.Properties.Title || accepted.Memory.Properties.Body != before.Properties.Body {
				t.Fatalf("combined valid final state was not preserved: %+v", accepted)
			}
		})
	}
}

func TestEvaluationExactSubjectsAndClaimOwnership(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			subject, err := s.Create(ctx, CreateRequest{Path: "beads/build", Title: "Build"})
			if err != nil {
				t.Fatal(err)
			}
			constraint, err := s.Create(ctx, CreateRequest{Path: "beads/constraint", TypeURL: s.ScopeURL() + "types/constraint", Title: "Constraint"})
			if err != nil {
				t.Fatal(err)
			}
			properties, err := json.Marshal(map[string]any{"title": "Assessment", "body": "", "subjects": []any{map[string]string{"uri": subject.ID, "revision": subject.Revision}}})
			if err != nil {
				t.Fatal(err)
			}
			evaluation, err := s.Create(ctx, CreateRequest{Path: "beads/evaluation", TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: properties})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/claim", TypeURL: s.ScopeURL() + "types/satisfies", SourcePath: "beads/evaluation", TargetPath: "beads/constraint", ExpectedSourceRevision: evaluation.Revision, Properties: map[string]any{"evidence": "Test run"}})
			if err != nil {
				t.Fatal(err)
			}
			if owner := claim.Source.(Record); owner.Revision == evaluation.Revision || len(owner.Owned) != 1 {
				t.Fatal("claim did not revise its evaluation owner")
			}
			if current, err := s.Show(ctx, "beads/build"); err != nil || !reflect.DeepEqual(current, subject) {
				t.Fatalf("claim revised assessed subject: %v", err)
			}
			if current, err := s.Show(ctx, "beads/constraint"); err != nil || !reflect.DeepEqual(current, constraint) {
				t.Fatalf("claim revised target: %v", err)
			}
			for _, bad := range []LinkCreateRequest{
				{Path: "links/direct", TypeURL: s.ScopeURL() + "types/satisfies", SourcePath: "beads/build", TargetPath: "beads/constraint", ExpectedSourceRevision: subject.Revision},
				{Path: "links/abstract", TypeURL: s.ScopeURL() + "types/conformance", SourcePath: "beads/evaluation", TargetPath: "beads/constraint", ExpectedSourceRevision: claim.Source.(Record).Revision},
			} {
				witness := workflowState(t, ctx, s)
				if _, err := s.AddInformationalLink(ctx, bad); !errors.Is(err, storage.ErrValidation) {
					t.Fatalf("invalid claim admission: %v", err)
				}
				if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
					t.Fatal("refused claim changed state")
				}
			}
			for _, subjects := range []any{[]any{}, []any{subject.ID}, []any{map[string]string{"uri": subject.ID}}, []any{map[string]string{"uri": subject.ID, "revision": "unknown"}}} {
				raw, err := json.Marshal(map[string]any{"title": "Bad assessment", "body": "", "subjects": subjects})
				if err != nil {
					t.Fatal(err)
				}
				witness := workflowState(t, ctx, s)
				if _, err := s.Create(ctx, CreateRequest{Path: "beads/bad-evaluation", TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: raw}); err == nil {
					t.Fatal("invalid exact subjects admitted")
				}
				if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
					t.Fatal("refused subjects changed state")
				}
			}
		})
	}
}

func TestNativeIssueSubjectToOwnershipKeepsNativePayload(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			issue, err := s.CreateIssue(ctx, "beads/work", plainIssue("Work"))
			if err != nil {
				t.Fatal(err)
			}
			rule, err := s.Create(ctx, CreateRequest{Path: "beads/rule", TypeURL: s.ScopeURL() + "types/constraint", Title: "Rule"})
			if err != nil {
				t.Fatal(err)
			}
			added, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/applies", TypeURL: s.ScopeURL() + "types/subject-to", SourcePath: "beads/work", TargetPath: "beads/rule", ExpectedSourceRevision: issue.Revision})
			if err != nil {
				t.Fatal(err)
			}
			owner, ok := added.Source.(IssueRecord)
			if !ok || owner.Revision == issue.Revision || len(owner.Owned) != len(issue.Owned)+1 || owner.Properties.Title != issue.Properties.Title || owner.Properties.IssueType != issue.Properties.IssueType {
				t.Fatalf("Issue ownership changed native content or missed revision: %+v", added.Source)
			}
			assertIssueEditVersion(t, ctx, s, "beads/work", issue)
			if current, err := s.Show(ctx, "beads/rule"); err != nil || !reflect.DeepEqual(current, rule) {
				t.Fatalf("SubjectTo changed target: %v", err)
			}
		})
	}
}

func TestSelectedLinkFamilyLimitsAtCommit(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			for _, name := range []string{"a", "b", "c"} {
				if _, err := s.Create(ctx, CreateRequest{Path: "beads/" + name, TypeURL: s.ScopeURL() + "types/constraint", Title: name}); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/defines", TypeURL: s.ScopeURL() + "types/defines", SourcePath: "beads/a", TargetPath: "beads/c", UnconditionalSource: true}); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []LinkCreateRequest{
				{Path: "links/second-parent", TypeURL: s.ScopeURL() + "types/defines", SourcePath: "beads/b", TargetPath: "beads/c", UnconditionalSource: true},
				{Path: "links/defines-cycle", TypeURL: s.ScopeURL() + "types/defines", SourcePath: "beads/c", TargetPath: "beads/a", UnconditionalSource: true},
			} {
				witness := workflowState(t, ctx, s)
				if _, err := s.AddInformationalLink(ctx, bad); !errors.Is(err, storage.ErrValidation) {
					t.Fatalf("family rule was bypassed: %v", err)
				}
				if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
					t.Fatal("refused family rule changed source/Link/history")
				}
			}
			// A broader Requires-family cycle is legal; Defines restrictions do
			// not leak upward into every Requires Link.
			if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/requires-back", TypeURL: s.ScopeURL() + "types/requires", SourcePath: "beads/c", TargetPath: "beads/a", UnconditionalSource: true}); err != nil {
				t.Fatal(err)
			}
			if _, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/replacement", TypeURL: s.ScopeURL() + "types/superseded-by", SourcePath: "beads/a", TargetPath: "beads/b", UnconditionalSource: true}); err != nil {
				t.Fatal(err)
			}
			for _, bad := range []LinkCreateRequest{
				{Path: "links/second-replacement", TypeURL: s.ScopeURL() + "types/superseded-by", SourcePath: "beads/a", TargetPath: "beads/c", UnconditionalSource: true},
				{Path: "links/replacement-cycle", TypeURL: s.ScopeURL() + "types/superseded-by", SourcePath: "beads/b", TargetPath: "beads/a", UnconditionalSource: true},
			} {
				witness := workflowState(t, ctx, s)
				if _, err := s.AddInformationalLink(ctx, bad); !errors.Is(err, storage.ErrValidation) {
					t.Fatalf("replacement rule bypassed: %v", err)
				}
				if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
					t.Fatal("refused replacement changed state")
				}
			}
		})
	}
}

func TestTypeValidationDoesNotAcquireNativeIssuePayloads(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			// Near-budget fixture work is bounded independently from initialization.
			ctx = currentReadBudgetContext(t, ctx)
			request := plainIssue("Large native payload")
			// Keep ordinary native authoring inside its growth budget. The
			// oversized nominal-read control is installed separately below.
			request.Issue.Description = strings.Repeat("x", PreviewCurrentReadByteLimit/4)
			large, err := s.CreateIssue(ctx, "beads/large", request)
			if err != nil {
				t.Fatal(err)
			}
			title := "Native title edit"
			updated, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", Actor: "test-author", Title: &title, ExpectedRevision: large.Revision})
			if err != nil {
				t.Fatal(err)
			}
			rule, err := s.Create(ctx, CreateRequest{Path: "beads/rule", TypeURL: s.ScopeURL() + "types/constraint", Title: "Rule"})
			if err != nil {
				t.Fatal(err)
			}
			linked, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/applies", TypeURL: s.ScopeURL() + "types/subject-to", SourcePath: "beads/large", TargetPath: "beads/rule", ExpectedSourceRevision: updated.Issue.Revision})
			if err != nil {
				t.Fatal(err)
			}
			owner := linked.Source.(IssueRecord)
			if owner.Revision == updated.Issue.Revision || owner.Properties.Description != request.Issue.Description || owner.Properties.Title != title || len(owner.Owned) != 1 {
				t.Fatal("native payload or participating owned-source revision changed incorrectly")
			}
			if current, err := s.Show(ctx, "beads/rule"); err != nil || !reflect.DeepEqual(current, rule) {
				t.Fatalf("native owned Link changed target: %v", err)
			}
			subjects, err := json.Marshal(map[string]any{"title": "Native assessment", "body": "", "subjects": []any{map[string]string{"uri": owner.ID, "revision": owner.Revision}}})
			if err != nil {
				t.Fatal(err)
			}
			evaluation, err := s.Create(ctx, CreateRequest{Path: "beads/evaluation", TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: subjects})
			if err != nil {
				t.Fatalf("exact native subject binding acquired its unrelated payload: %v", err)
			}
			title = "Native edit in mixed workspace"
			mixed, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/large", Actor: "test-author", Title: &title, ExpectedRevision: owner.Revision})
			if err != nil {
				t.Fatalf("mixed native owner edit: %v", err)
			}
			if !mixed.Changed || mixed.Issue.Properties.Title != title || mixed.Issue.Properties.Description != request.Issue.Description || !reflect.DeepEqual(mixed.Issue.Owned, owner.Owned) {
				t.Fatal("native edit changed its owned Link or unrelated payload")
			}
			if current, err := s.Show(ctx, "beads/evaluation"); err != nil || !reflect.DeepEqual(current, evaluation) {
				t.Fatalf("native edit changed exact subject binding: %v", err)
			}
			// Reading the retained large payload is a separate proof, not a
			// Type-admission latency assertion sharing the mutation window.
			ctx = currentReadBudgetContext(t, ctx)
			assertIssueEditVersion(t, ctx, s, "beads/large", owner)
			// Nominal Type validation must not hydrate an unrelated oversized
			// native payload, even when that legacy fixture blocks new growth.
			if _, err := s.db.ExecContext(ctx, "UPDATE issues SET description=? WHERE id=?", strings.Repeat("x", PreviewCurrentReadByteLimit+1), owner.Properties.ID); err != nil {
				t.Fatal(err)
			}
			if err := s.withTx(ctx, false, func(tx *sql.Tx) error { return s.validateTypeStateInTx(ctx, tx) }); err != nil {
				t.Fatal(err)
			}
			if _, err := s.CurrentSnapshot(ctx); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("legacy oversized control: %v", err)
			}
		})
	}
}

func TestNativePayloadUpdateDoesNotScanUnrelatedMemoryPopulation(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			// This companion oversized-payload isolation fixture uses the same
			// bounded workload allowance, without changing acquisition limits.
			ctx = currentReadBudgetContext(t, ctx)
			issue, err := s.CreateIssue(ctx, "beads/work", plainIssue("Original native title"))
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/unrelated", Title: "Unrelated"}); err != nil {
				t.Fatal(err)
			}
			// Deliberately corrupt only this fixture's unrelated current payload.
			// This exceeds the selected acquisition budget without changing any
			// nominal endpoint fact needed by the native payload-only operation.
			raw, err := json.Marshal(map[string]string{"title": "Unrelated", "body": strings.Repeat("x", PreviewCurrentReadByteLimit+1)})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := s.db.ExecContext(ctx, `UPDATE graph_preview_payloads SET properties=? WHERE path='beads/unrelated'`, raw); err != nil {
				t.Fatal(err)
			}
			title := "X"
			changed, err := s.UpdateIssue(ctx, UpdateIssueRequest{Path: "beads/work", Actor: "test-author", Title: &title, ExpectedRevision: issue.Revision})
			if err != nil || !changed.Changed || changed.Issue.Properties.Title != title {
				t.Fatalf("unrelated Memory population blocked native edit: %v", err)
			}
			witness := workflowState(t, ctx, s)
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/checked", Title: "Selected mutation"}); !errors.Is(err, ErrLimitExceeded) {
				t.Fatalf("selected mutation bypassed population budget: %v", err)
			}
			if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
				t.Fatal("failed selected mutation published staged state")
			}
		})
	}
}

func TestEvaluationRefusesRemovedNativeSubjectRevision(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			subject, err := s.CreateIssue(ctx, "beads/subject", plainIssue("Subject"))
			if err != nil {
				t.Fatal(err)
			}
			// Existing removal fixture seam, not a production removal capability.
			if err := s.withTx(ctx, true, func(tx *sql.Tx) error {
				return issueops.MarkLatestVersionRemovedInTx(ctx, tx, subject.Properties.ID, issueops.AsOfRestrictionGoneRetention, "test subject unavailable")
			}); err != nil {
				t.Fatal(err)
			}
			raw, err := json.Marshal(map[string]any{"title": "Evaluation", "body": "", "subjects": []any{map[string]string{"uri": subject.ID, "revision": subject.Revision}}})
			if err != nil {
				t.Fatal(err)
			}
			witness := workflowState(t, ctx, s)
			if _, err := s.Create(ctx, CreateRequest{Path: "beads/evaluation", TypeURL: s.ScopeURL() + "types/evaluation", PropertiesJSON: raw}); !errors.Is(err, ErrVersionUnknown) {
				t.Fatalf("removed exact subject revision admitted: %v", err)
			}
			if !reflect.DeepEqual(witness, workflowState(t, ctx, s)) {
				t.Fatal("refused subject admission published staged state")
			}
		})
	}
}

func TestEvaluationSubjectsStayBoundAndDistinct(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := s.Close(); err != nil {
					t.Error(err)
				}
			})
			marshal := func(value any) json.RawMessage {
				t.Helper()
				raw, err := json.Marshal(value)
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			a, err := s.Create(ctx, CreateRequest{Path: "beads/a", Title: "A"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Create(ctx, CreateRequest{Path: "beads/b", Title: "B"})
			if err != nil {
				t.Fatal(err)
			}
			rule, err := s.Create(ctx, CreateRequest{Path: "beads/rule", TypeURL: s.ScopeURL() + "types/constraint", Title: "Rule"})
			if err != nil {
				t.Fatal(err)
			}
			aRef := map[string]string{"uri": a.ID, "revision": a.Revision}
			bRef := map[string]string{"uri": b.ID, "revision": b.Revision}
			evalType := s.ScopeURL() + "types/evaluation"
			evaluation, err := s.Create(ctx, CreateRequest{Path: "beads/evaluation", TypeURL: evalType,
				PropertiesJSON: marshal(map[string]any{"title": "Evaluation", "body": "", "subjects": []any{aRef, bRef}})})
			if err != nil {
				t.Fatal(err)
			}
			claim, err := s.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/claim", TypeURL: s.ScopeURL() + "types/satisfies",
				SourcePath: "beads/evaluation", TargetPath: "beads/rule", ExpectedSourceRevision: evaluation.Revision})
			if err != nil {
				t.Fatal(err)
			}
			owner := claim.Source.(Record)
			changedSubjects := []any{aRef}
			var replacement Properties
			if err := json.Unmarshal(marshal(map[string]any{"title": "Evaluation", "body": "", "subjects": changedSubjects}), &replacement); err != nil {
				t.Fatal(err)
			}
			for _, test := range []struct {
				name  string
				write func() (MemoryMutationResult, error)
			}{
				{"merge", func() (MemoryMutationResult, error) {
					return s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/evaluation", ExpectedRevision: owner.Revision,
						PropertiesMerge: marshal(map[string]any{"subjects": changedSubjects})})
				}},
				{"replacement", func() (MemoryMutationResult, error) {
					return s.UpdateMemory(ctx, MemoryUpdateRequest{Path: "beads/evaluation", ExpectedRevision: owner.Revision, Properties: replacement})
				}},
				{"ordered patch", func() (MemoryMutationResult, error) {
					return s.PatchMemoryProperties(ctx, MemoryPropertiesPatchRequest{Path: "beads/evaluation", ExpectedRevision: owner.Revision,
						Patch: marshal([]any{map[string]any{"op": "replace", "path": "/subjects", "value": changedSubjects}})})
				}},
			} {
				t.Run(test.name, func(t *testing.T) {
					before := workflowState(t, ctx, s)
					got, err := test.write()
					if !errors.Is(err, storage.ErrValidation) || !reflect.ValueOf(got).IsZero() || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
						t.Fatalf("group reassignment changed Evaluation, claim or history: %+v %v", got, err)
					}
				})
			}
			// Array order is not group membership. Ordinary content remains editable.
			var reordered Properties
			if err := json.Unmarshal(marshal(map[string]any{"title": "Evaluation", "body": "Revised account", "subjects": []any{bRef, aRef}}), &reordered); err != nil {
				t.Fatal(err)
			}
			edited, err := s.UpdateMemory(ctx, MemoryUpdateRequest{Path: "beads/evaluation", ExpectedRevision: owner.Revision, Properties: reordered})
			if err != nil || !edited.Changed || !reflect.DeepEqual(edited.Memory.Owned, owner.Owned) {
				t.Fatalf("same group content edit: %+v %v", edited, err)
			}
			// A pre-Evaluation revision of the same identity is still self-assessment.
			before := workflowState(t, ctx, s)
			got, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/a", TypeURL: evalType, ExpectedRevision: a.Revision,
				PropertiesMerge: marshal(map[string]any{"subjects": []any{aRef}})})
			if !errors.Is(err, storage.ErrValidation) || !reflect.ValueOf(got).IsZero() || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
				t.Fatalf("self-assessment retype changed state: %+v %v", got, err)
			}
			// A distinct Evaluation may assess an Evaluation's exact revision.
			_, err = s.Create(ctx, CreateRequest{Path: "beads/review", TypeURL: evalType, PropertiesJSON: marshal(map[string]any{
				"title": "Review", "body": "", "subjects": []any{map[string]string{"uri": edited.Memory.ID, "revision": edited.Memory.Revision}},
			})})
			if err != nil {
				t.Fatalf("distinct Evaluation could not assess Evaluation: %v", err)
			}
			if current, err := s.Show(ctx, "beads/rule"); err != nil || !reflect.DeepEqual(current, rule) {
				t.Fatalf("Evaluation edits changed Constraint: %v", err)
			}
		})
	}
}

func TestEvaluationGroupSurvivesRetyping(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			s, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { _ = s.Close() })
			a, err := s.Create(ctx, CreateRequest{Path: "beads/first-subject", Title: "First"})
			if err != nil {
				t.Fatal(err)
			}
			b, err := s.Create(ctx, CreateRequest{Path: "beads/other-subject", Title: "Other"})
			if err != nil {
				t.Fatal(err)
			}
			properties := func(subject Record) json.RawMessage {
				t.Helper()
				raw, err := json.Marshal(map[string]any{"title": "Assessment", "body": "", "subjects": []exactSubjectBinding{{URI: subject.ID, Revision: subject.Revision}}})
				if err != nil {
					t.Fatal(err)
				}
				return raw
			}
			// A Memory's arbitrary subjects extension is not an Evaluation binding.
			memory, err := s.Create(ctx, CreateRequest{Path: "beads/assessment", PropertiesJSON: properties(b)})
			if err != nil {
				t.Fatal(err)
			}
			evalType := s.ScopeURL() + "types/evaluation"
			first, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/assessment", TypeURL: evalType,
				ExpectedRevision: memory.Revision, PropertiesJSON: properties(a)})
			if err != nil {
				t.Fatalf("first Evaluation must establish its own group: %v", err)
			}
			for cycle := 0; cycle < 2; cycle++ {
				plain, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/assessment", TypeURL: s.ScopeURL() + "types/memory",
					ExpectedRevision: first.Memory.Revision, PropertiesJSON: json.RawMessage(`{"title":"Between evaluations","body":""}`)})
				if err != nil {
					t.Fatal(err)
				}
				// The binding must survive reopening, rather than depend on process state.
				if err := s.Close(); err != nil {
					t.Fatal(err)
				}
				s, err = OpenExisting(ctx, options)
				if err != nil {
					t.Fatal(err)
				}
				before := workflowState(t, ctx, s)
				got, err := s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/assessment", TypeURL: evalType,
					ExpectedRevision: plain.Memory.Revision, PropertiesJSON: properties(b)})
				if !errors.Is(err, storage.ErrValidation) || !reflect.ValueOf(got).IsZero() || !reflect.DeepEqual(before, workflowState(t, ctx, s)) {
					t.Fatalf("cycle %d reassigned identity's group or changed state: %+v %v", cycle, got, err)
				}
				first, err = s.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/assessment", TypeURL: evalType,
					ExpectedRevision: plain.Memory.Revision, PropertiesJSON: properties(a)})
				if err != nil || !first.Changed {
					t.Fatalf("cycle %d could not resume original group: %+v %v", cycle, first, err)
				}
			}
		})
	}
}
