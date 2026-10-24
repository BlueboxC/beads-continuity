//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	publicops "github.com/steveyegge/beads/issueops"
)

// This enabled setup is explicit and limited to retained assessment semantics.
// All ordinary experiment/testOptions callers retain the default-off policy.
func assessmentExperimentOptions(t *testing.T, backend string) (context.Context, Options) {
	t.Helper()
	ctx, options := issueExperimentOptions(t, backend)
	options.AssessmentsEnabled = true
	return ctx, options
}
func assessmentMemoryStore(t *testing.T, backend string) (context.Context, Options, *Store) {
	t.Helper()
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
	return ctx, options, s
}

func TestAssessmentAvailabilityDefaultOff(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := assessmentExperimentOptions(t, backend)
			enabled, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			subject, err := enabled.Create(ctx, CreateRequest{Path: "beads/subject", Title: "Subject"})
			if err != nil {
				t.Fatal(err)
			}
			constraint, err := enabled.Create(ctx, CreateRequest{Path: "beads/rule", Title: "Rule", TypeURL: enabled.ScopeURL() + "types/constraint"})
			if err != nil {
				t.Fatal(err)
			}
			props, err := json.Marshal(map[string]any{"title": "Evaluation", "body": "", "subjects": []map[string]string{{"uri": subject.ID, "revision": subject.Revision}}})
			if err != nil {
				t.Fatal(err)
			}
			eval, err := enabled.Create(ctx, CreateRequest{Path: "beads/eval", TypeURL: enabled.ScopeURL() + "types/evaluation", PropertiesJSON: props})
			if err != nil {
				t.Fatal(err)
			}
			initial := eval
			var claims []LinkRecord
			for _, name := range []string{"addresses", "satisfies", "violates"} {
				got, err := enabled.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/" + name, SourcePath: "beads/eval", TargetPath: "beads/rule", TypeURL: enabled.ScopeURL() + "types/" + name, UnconditionalSource: true})
				if err != nil {
					t.Fatal(err)
				}
				claims = append(claims, got.Link)
				eval = got.Source.(Record)
			}
			var availableOwned []LinkRecord
			for _, name := range []string{"subject-to", "superseded-by"} {
				target := "beads/rule"
				if name == "superseded-by" {
					target = "beads/subject"
				}
				got, err := enabled.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/" + name, SourcePath: "beads/eval", TargetPath: target, TypeURL: enabled.ScopeURL() + "types/" + name, UnconditionalSource: true})
				if err != nil {
					t.Fatal(err)
				}
				availableOwned = append(availableOwned, got.Link)
				eval = got.Source.(Record)
			}
			recoverable, err := enabled.Create(ctx, CreateRequest{Path: "beads/recovery", TypeURL: eval.Type, PropertiesJSON: props})
			if err != nil {
				t.Fatal(err)
			}
			installed, err := enabled.ListInstalledTypes(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if err := enabled.Close(); err != nil {
				t.Fatal(err)
			}
			options.AssessmentsEnabled = false
			off, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := off.Close(); err != nil {
					t.Error(err)
				}
			})
			gotTypes, err := off.ListInstalledTypes(ctx)
			if err != nil || len(gotTypes) != 55 || !reflect.DeepEqual(installed, gotTypes) {
				t.Fatalf("gate changed installed definitions: %v", err)
			}
			if got, err := off.Show(ctx, "beads/eval"); err != nil || !reflect.DeepEqual(got, eval) {
				t.Fatalf("disabled read lost owned claims: %v", err)
			}
			if got, err := off.ReadVersion(ctx, "beads/eval", initial.Revision); err != nil || !reflect.DeepEqual(got, initial) {
				t.Fatalf("disabled history changed: %v", err)
			}
			refuse := func(name string, fn func() error) {
				t.Helper()
				before := workflowState(t, ctx, off)
				if err := fn(); !errors.Is(err, ErrCapabilityUnavailable) {
					t.Fatalf("%s: want unavailable, got %v", name, err)
				}
				if !reflect.DeepEqual(before, workflowState(t, ctx, off)) {
					t.Fatalf("%s mutated current/retained/owned state", name)
				}
			}
			refuse("allocation helper", func() error {
				return off.withTypeValidationTx(ctx, func(tx *sql.Tx) error {
					return off.createInTx(ctx, tx, CreateRequest{Path: "beads/helper"}, Record{Type: eval.Type}, nil, nil)
				})
			})
			refuse("create", func() error {
				_, e := off.Create(ctx, CreateRequest{Path: "beads/refused", TypeURL: eval.Type, PropertiesJSON: props})
				return e
			})
			refuse("retype", func() error {
				_, e := off.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/subject", TypeURL: eval.Type, PropertiesMerge: props, Unconditional: true})
				return e
			})
			refuse("replacement", func() error {
				_, e := off.UpdateMemory(ctx, MemoryUpdateRequest{Path: "beads/eval", Properties: eval.Properties, Unconditional: true})
				return e
			})
			refuse("same type no-op", func() error {
				_, e := off.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/eval", TypeURL: eval.Type, Unconditional: true})
				return e
			})
			refuse("metadata", func() error {
				_, e := off.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/eval", Metadata: publicops.MetadataPatch{Set: map[string]json.RawMessage{"note": json.RawMessage(`true`)}}, Unconditional: true})
				return e
			})
			refuse("ordered patch", func() error {
				_, e := off.PatchMemoryProperties(ctx, MemoryPropertiesPatchRequest{Path: "beads/eval", Patch: []byte(`[{"op":"add","path":"/body","value":"changed"}]`), Unconditional: true})
				return e
			})
			refuse("live historical restore", func() error {
				_, e := off.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/eval", FromRevision: initial.Revision, Unconditional: true})
				return e
			})
			for _, claim := range claims {
				refuse("create "+claim.Type, func() error {
					_, e := off.AddInformationalLink(ctx, LinkCreateRequest{SourcePath: "beads/eval", TargetPath: "beads/rule", TypeURL: claim.Type, UnconditionalSource: true})
					return e
				})
				path := "links/" + claim.Type[len(off.ScopeURL()+"types/"):]
				refuse("claim no-op", func() error {
					_, e := off.UpdateLink(ctx, LinkUpdateRequest{Path: path, Properties: claim.Properties, Unconditional: true, UnconditionalSource: true})
					return e
				})
				refuse("claim ordered patch", func() error {
					_, e := off.PatchLinkProperties(ctx, LinkPropertiesPatchRequest{Path: path, Patch: []byte(`[{"op":"add","path":"/evidence","value":"changed"}]`), Unconditional: true, UnconditionalSource: true})
					return e
				})
				pin := EndpointPin{Revision: constraint.Revision}
				refuse("claim repin", func() error {
					_, e := off.UpdateLink(ctx, LinkUpdateRequest{Path: path, TargetPin: &pin, Unconditional: true, UnconditionalSource: true})
					return e
				})
			}
			for _, link := range availableOwned {
				name := link.Type[len(off.ScopeURL()+"types/"):]
				path := "links/" + name
				target := "beads/rule"
				revision := constraint.Revision
				if name == "superseded-by" {
					target = "beads/subject"
					revision = subject.Revision
				}
				refuse("available owned create "+name, func() error {
					_, e := off.AddInformationalLink(ctx, LinkCreateRequest{SourcePath: "beads/eval", TargetPath: target, TypeURL: link.Type, UnconditionalSource: true})
					return e
				})
				refuse("available owned update "+name, func() error {
					_, e := off.UpdateLink(ctx, LinkUpdateRequest{Path: path, Properties: map[string]any{"note": "refused"}, Unconditional: true, UnconditionalSource: true})
					return e
				})
				pin := EndpointPin{Revision: revision}
				refuse("available owned repin "+name, func() error {
					_, e := off.UpdateLink(ctx, LinkUpdateRequest{Path: path, TargetPin: &pin, Unconditional: true, UnconditionalSource: true})
					return e
				})
			}
			beforeReference, err := off.Show(ctx, "beads/eval")
			if err != nil {
				t.Fatal(err)
			}
			if _, err := off.AddInformationalLink(ctx, LinkCreateRequest{Path: "links/independent", SourcePath: "beads/eval", TargetPath: "beads/subject", TypeURL: off.ScopeURL() + "types/references"}); err != nil {
				t.Fatal(err)
			}
			if after, err := off.Show(ctx, "beads/eval"); err != nil || !reflect.DeepEqual(beforeReference, after) {
				t.Fatalf("unowned Reference changed disabled source: %v", err)
			}
			refuse("abstract Conformance", func() error {
				_, e := off.AddInformationalLink(ctx, LinkCreateRequest{SourcePath: "beads/eval", TargetPath: "beads/rule", TypeURL: off.ScopeURL() + "types/conformance", UnconditionalSource: true})
				return e
			})
			deleted, err := off.DeleteMemory(ctx, MemoryDeleteRequest{Path: "beads/recovery", ExpectedRevision: recoverable.Revision})
			if err != nil || !deleted.Deleted {
				t.Fatalf("disabled cleanup failed: %v", err)
			}
			refuse("deleted recovery", func() error {
				_, e := off.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/recovery", FromRevision: recoverable.Revision, Unconditional: true})
				return e
			})
			// A populated assessment graph must not poison unrelated writes.
			if _, err := off.Create(ctx, CreateRequest{Path: "beads/ordinary", Title: "Ordinary"}); err != nil {
				t.Fatal(err)
			}
			refuse("invalid retype-out fixture", func() error {
				_, e := off.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/eval", FromRevision: initial.Revision, TypeURL: MemoryTypeURL(off.ScopeURL()), Unconditional: true})
				return e
			})
			for _, claim := range claims {
				path := "links/" + claim.Type[len(off.ScopeURL()+"types/"):]
				if _, err := off.Unlink(ctx, LinkDeleteRequest{Path: path, Unconditional: true, UnconditionalSource: true}); err != nil {
					t.Fatal(err)
				}
			}
			out, err := off.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/eval", TypeURL: MemoryTypeURL(off.ScopeURL()), Unconditional: true})
			if err != nil {
				t.Fatalf("valid retype-out failed: %v", err)
			}
			refuse("re-entry", func() error {
				_, e := off.PatchMemory(ctx, MemoryPatchRequest{Path: "beads/eval", TypeURL: eval.Type, ExpectedRevision: out.Memory.Revision})
				return e
			})
		})
	}
}
