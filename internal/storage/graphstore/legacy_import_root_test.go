//go:build cgo

package graphstore

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/legacyimport"
	"github.com/steveyegge/beads/internal/storage"
)

func TestLegacyImportRootIdentityAndAtomicity(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			initial, options := issueExperimentOptions(t, backend)
			operation := func() context.Context {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(initial), 90*time.Second)
				t.Cleanup(cancel)
				return ctx
			}
			store, err := OpenExisting(operation(), options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			input := parseLegacyFixture(t, `{"id":"old-a","title":"Target"}
{"id":"old-b","title":"Source","dependencies":[{"depends_on_id":"old-a","type":"relates-to"}],"comments":[{"id":"original-comment","author":"original","text":" exact comment ","created_at":"2026-10-20T01:02:03Z"}]}
{"_type":"memory","key":"beads/literal","value":"  body  \n exact body "}
{"_type":"memory","key":"types/memory","value":""}`)
			sourceBytes, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			before := legacyImportState(t, operation(), store)
			root := LegacyImportOptions{SourcePrefix: "old", IDRoot: "foo/bar"}
			for _, dry := range []bool{true, false} {
				injected := errors.New("owned failure after native rows")
				store.afterWrite = func(stage string) error {
					if stage == "import-native" {
						return injected
					}
					return nil
				}
				got, err := store.ImportLegacyWithOptions(operation(), input, "importer", dry, root)
				store.afterWrite = nil
				if !errors.Is(err, injected) || !reflect.DeepEqual(got, LegacyImportResult{}) || !reflect.DeepEqual(before, legacyImportState(t, operation(), store)) {
					t.Fatalf("rooted failure leaked effects/result: %+v %v", got, err)
				}
				after, err := json.Marshal(input)
				if err != nil || string(after) != string(sourceBytes) {
					t.Fatal("post-native refusal mutated caller")
				}
			}
			preview, err := store.ImportLegacyWithOptions(operation(), input, "importer", true, root)
			if err != nil {
				t.Fatal(err)
			}
			if !preview.DryRun || !preview.GeneratedIDsProvisional || preview.IssuePrefix != before["issue_prefix"] || preview.SourceIssuePrefix != "old" || preview.IDRoot != "foo/bar" || !reflect.DeepEqual(before, legacyImportState(t, operation(), store)) {
				t.Fatalf("wrong rooted dry-run: %+v", preview)
			}
			result, err := store.ImportLegacyWithOptions(operation(), input, "importer", false, root)
			if err != nil {
				t.Fatal(err)
			}
			scope := store.ScopeURL()
			if result.DryRun || result.GeneratedIDsProvisional || result.IssuePrefix != before["issue_prefix"] || !reflect.DeepEqual(result.IDs, preview.IDs) || !reflect.DeepEqual(result.MemoryIDs, preview.MemoryIDs) {
				t.Fatalf("rooted apply/source mapping changed: %+v", result)
			}
			for source, id := range result.IDs {
				if id != scope+"foo/bar/"+source {
					t.Fatalf("source identity changed: %q -> %q", source, id)
				}
			}
			paths := map[string]string{}
			for source, id := range result.IDs {
				selected, err := store.ResolveCanonicalPublicResourceIdentity(operation(), id)
				if err != nil {
					t.Fatal(err)
				}
				paths[source] = selected.PrivatePath
				issue, err := store.ShowIssue(operation(), selected.PrivatePath)
				if err != nil || issue.ID != id || issue.Type != scope+"types/native-issue/7461736b" || issue.Properties.ID != "foo/bar/"+source {
					t.Fatalf("rooted Issue identity: %+v %v", issue, err)
				}
				old, err := store.ReadVersion(operation(), selected.PrivatePath, issue.Revision)
				if err != nil {
					t.Fatal(err)
				}
				retained := old.(IssueRecord)
				currentJSON, err := canonicalJSON(issue)
				if err != nil {
					t.Fatal(err)
				}
				retainedJSON, err := canonicalJSON(retained)
				if err != nil {
					t.Fatal(err)
				}
				// Native hydration adds nonserialized ContentHash/RowVersion cache
				// fields. Compare the complete public record, including Type and
				// owned state, rather than those current-only implementation fields.
				if !bytes.Equal(currentJSON, retainedJSON) {
					t.Fatalf("initial retained rooted Issue differs: current=%s retained=%s", currentJSON, retainedJSON)
				}
				if source == "old-b" {
					if len(issue.Owned) != 1 {
						t.Fatalf("ownedLinks=%d", len(issue.Owned))
					}
					var link LinkRecord
					if err := json.Unmarshal(issue.Owned[0], &link); err != nil {
						t.Fatal(err)
					}
					if link.ID != result.DependencyIDs["old-b -> old-a"] || link.Source != result.IDs["old-b"] || link.Target != result.IDs["old-a"] {
						t.Fatalf("rooted owned endpoints: %+v", link)
					}
					local := strings.TrimPrefix(link.ID, scope+"foo/bar/")
					if local == link.ID || len(local) < graph.GeneratedResourceIDInitialLength || len(local) > graph.GeneratedResourceIDMaxLength || strings.ContainsAny(local, "/-.") {
						t.Fatalf("generated Link not short/root-once: %q", link.ID)
					}
					allocated, err := store.ResolveCanonicalPublicResourceIdentity(operation(), link.ID)
					if err != nil {
						t.Fatal(err)
					}
					version, err := store.ReadVersion(operation(), allocated.PrivatePath, link.Revision)
					if err != nil || !reflect.DeepEqual(version.(LinkRecord), link) {
						t.Fatalf("initial retained Link differs: %+v %v", version, err)
					}
					comments, err := store.IssueComments(operation(), selected.PrivatePath)
					if err != nil || len(comments) != 1 || comments[0].ID != "original-comment" || comments[0].IssueID != "foo/bar/old-b" || comments[0].Text != " exact comment " {
						t.Fatalf("rooted native comments: %+v %v", comments, err)
					}
				}
			}
			for key, body := range map[string]string{"beads/literal": "  body  \n exact body ", "types/memory": ""} {
				id := result.MemoryIDs[key]
				if id != scope+"foo/bar/"+key {
					t.Fatalf("literal Memory/root identity lost: %q -> %q", key, id)
				}
				selected, err := store.ResolveCanonicalPublicResourceIdentity(operation(), id)
				if err != nil {
					t.Fatal(err)
				}
				memory, err := store.Show(operation(), selected.PrivatePath)
				if err != nil {
					t.Fatal(err)
				}
				title := "body"
				if body == "" {
					title = "Untitled memory"
				}
				if memory.PublicID != "foo/bar/"+key || memory.Properties.Title != title || memory.Properties.Body != body {
					t.Fatalf("rooted Memory body/title/ID changed: %+v", memory)
				}
				retained, err := store.ReadVersion(operation(), selected.PrivatePath, memory.Revision)
				if err != nil || retained.(Record).ID != memory.ID || !reflect.DeepEqual(retained.(Record).Properties, memory.Properties) {
					t.Fatalf("initial retained Memory differs: %+v %v", retained, err)
				}
			}
			after, err := json.Marshal(input)
			if err != nil || string(after) != string(sourceBytes) {
				t.Fatal("rooted apply mutated source package")
			}
			fresh, err := store.CreateIssue(operation(), "", plainIssue("After rooted import"))
			if err != nil {
				t.Fatal(err)
			}
			if !strings.HasPrefix(fresh.Properties.ID, before["issue_prefix"]+"-") {
				t.Fatalf("root changed future create prefix: %q", fresh.Properties.ID)
			}
			allocated, err := store.ResolveCanonicalPublicResourceIdentity(operation(), fresh.ID)
			if err != nil {
				t.Fatal(err)
			}
			for _, pair := range [][2]string{{allocated.PrivatePath, paths["old-a"]}, {paths["old-b"], allocated.PrivatePath}} {
				if _, err := store.AddDependency(operation(), DependencyRequest{SourcePath: pair[0], TargetPath: pair[1], Actor: "importer"}); err != nil {
					t.Fatalf("checked-local post-root dependency failed: %v", err)
				}
			}
		})
	}
}

func TestLegacyImportRootAndAuthorityRefusals(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			initial, options := issueExperimentOptions(t, backend)
			operation := func() context.Context {
				ctx, cancel := context.WithTimeout(context.WithoutCancel(initial), 90*time.Second)
				t.Cleanup(cancel)
				return ctx
			}
			store, err := OpenExisting(operation(), options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			before := legacyImportState(t, operation(), store)
			cases := []struct {
				input   string
				options LegacyImportOptions
			}{
				{`{"id":"old-a","title":"A"}
{"_type":"memory","key":"old-a","value":"collision"}`, LegacyImportOptions{IDRoot: "root"}},
				{`{"id":"old-a","title":"A","dependencies":[{"depends_on_id":"old-b","type":"blocks"}]}
{"id":"old-b","title":"B"}`, LegacyImportOptions{IDRoot: "external:x"}},
				{`{"id":"old-a","title":"A"}`, LegacyImportOptions{IDRoot: strings.Repeat("r", 250)}},
				{`{"_type":"memory","key":"a//b","value":"incompatible"}`, LegacyImportOptions{IDRoot: "root"}},
				{`{"_type":"memory","key":"issue","value":"root creates a reserved identity"}`, LegacyImportOptions{IDRoot: "types"}},
				{`{"id":"old-a","title":"A"}
{"_type":"memory","key":"types/native-issue/7461736b","value":"reserved native task Type"}`, LegacyImportOptions{}},
				{`{"id":"types/native-issue/7461736b","title":"Issue colliding with its own Type"}`, LegacyImportOptions{SourcePrefix: "types/native"}},
				{`{"id":"old-a","title":"A","dependencies":[{"depends_on_id":"old-b","type":"custom-kind"}]}
{"id":"old-b","title":"B"}
{"_type":"memory","key":"types/native-dependency/637573746f6d2d6b696e64","value":"prospective later dependency Type"}`, LegacyImportOptions{}},
			}
			for _, id := range []string{"types/issue", "types/memory", "types/dependency", "bdp.json"} {
				raw, err := json.Marshal(legacyimport.Memory{Key: id, Value: "reserved"})
				if err != nil {
					t.Fatal(err)
				}
				// Memory's JSONL record discriminator belongs to framing, not Memory.
				var m map[string]any
				if err := json.Unmarshal(raw, &m); err != nil {
					t.Fatal(err)
				}
				m["_type"] = "memory"
				line, err := json.Marshal(m)
				if err != nil {
					t.Fatal(err)
				}
				cases = append(cases, struct {
					input   string
					options LegacyImportOptions
				}{string(line), LegacyImportOptions{}})
			}
			for _, test := range cases {
				input := parseLegacyFixture(t, test.input)
				source, err := json.Marshal(input)
				if err != nil {
					t.Fatal(err)
				}
				for _, dry := range []bool{true, false} {
					got, err := store.ImportLegacyWithOptions(operation(), input, "importer", dry, test.options)
					if !errors.Is(err, storage.ErrValidation) || !reflect.DeepEqual(got, LegacyImportResult{}) || !reflect.DeepEqual(before, legacyImportState(t, operation(), store)) {
						t.Fatalf("root/authority refusal leaked: %+v %v", got, err)
					}
					if (strings.Contains(test.input, "prospective later dependency Type") || strings.Contains(test.input, "Issue colliding with its own Type")) && !strings.Contains(err.Error(), "reserved public ID") {
						t.Fatalf("prospective binding collision failed at wrong boundary: %v", err)
					}
					after, err := json.Marshal(input)
					if err != nil || string(after) != string(source) {
						t.Fatal("refusal changed caller input")
					}
				}
			}
		})
	}
}

func TestLegacyImportNativeIDBoundaryAndRootedAuthorityKey(t *testing.T) {
	for _, backend := range []string{"embedded", "server"} {
		t.Run(backend, func(t *testing.T) {
			ctx, options := issueExperimentOptions(t, backend)
			store, err := OpenExisting(ctx, options)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := store.Close(); err != nil {
					t.Error(err)
				}
			})
			input := parseLegacyFixture(t, `{"id":"old-a","title":"Exact native backing limit"}
{"_type":"memory","key":"types/issue","value":"literal key relocated away from authority"}`)
			root := strings.Repeat("r", 249)
			publicID := root + "/old-a"
			if len(publicID) != 255 {
				t.Fatal("fixture must exercise exactly 255 UTF-8 bytes")
			}
			before := legacyImportState(t, ctx, store)
			preview, err := store.ImportLegacyWithOptions(ctx, input, "importer", true, LegacyImportOptions{IDRoot: root})
			if err != nil || !preview.DryRun || !reflect.DeepEqual(before, legacyImportState(t, ctx, store)) {
				t.Fatalf("255-byte dry-run failed or changed state: %+v %v", preview, err)
			}
			result, err := store.ImportLegacyWithOptions(ctx, input, "importer", false, LegacyImportOptions{IDRoot: root})
			if err != nil || !reflect.DeepEqual(result.IDs, preview.IDs) || !reflect.DeepEqual(result.MemoryIDs, preview.MemoryIDs) {
				t.Fatalf("255-byte apply changed source mapping: %+v %v", result, err)
			}
			uri := store.ScopeURL() + publicID
			identity, err := store.ResolveCanonicalPublicResourceIdentity(ctx, uri)
			if err != nil || result.IDs["old-a"] != uri {
				t.Fatalf("255-byte identity not preserved: %+v %v", result, err)
			}
			current, err := store.ShowIssue(ctx, identity.PrivatePath)
			if err != nil || current.Properties.ID != publicID {
				t.Fatalf("255-byte native ID lost: %+v %v", current, err)
			}
			retained, err := store.ReadVersion(ctx, identity.PrivatePath, current.Revision)
			if err != nil {
				t.Fatal(err)
			}
			currentJSON, err := canonicalJSON(current)
			if err != nil {
				t.Fatal(err)
			}
			retainedRecord := retained.(IssueRecord)
			retainedJSON, err := canonicalJSON(retainedRecord)
			if err != nil || !bytes.Equal(currentJSON, retainedJSON) {
				t.Fatalf("255-byte retained record differs: %s %s %v", currentJSON, retainedJSON, err)
			}
			var size int
			if err := store.db.QueryRowContext(ctx, `SELECT OCTET_LENGTH(backing_key) FROM graph_preview_catalog WHERE path=?`, identity.PrivatePath).Scan(&size); err != nil || size != 255 {
				t.Fatalf("native catalog boundary: %d %v", size, err)
			}
			memoryURI := store.ScopeURL() + root + "/types/issue"
			selected, err := store.ResolveCanonicalPublicResourceIdentity(ctx, memoryURI)
			if err != nil || result.MemoryIDs["types/issue"] != memoryURI {
				t.Fatalf("root failed to relocate a reserved spelling: %+v %v", result, err)
			}
			memory, err := store.Show(ctx, selected.PrivatePath)
			if err != nil || memory.ID != memoryURI || memory.Properties.Body != "literal key relocated away from authority" {
				t.Fatalf("relocated literal key lost: %+v %v", memory, err)
			}
		})
	}
}
