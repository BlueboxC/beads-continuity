package graphstore

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

// No backend is constructed: these tests exercise only detached result closure,
// pre-BEGIN context validation, and lossless string admission.
func TestExportCurrentSnapshotContextBeforeOpen(t *testing.T) {
	s := &Store{}
	got, err := s.ExportCurrentSnapshot(nil)
	if err == nil || !reflect.DeepEqual(got, ExportCurrentState{}) {
		t.Fatalf("nil context: %+v %v", got, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	got, err = s.ExportCurrentSnapshot(ctx)
	if !errors.Is(err, context.Canceled) || !reflect.DeepEqual(got, ExportCurrentState{}) {
		t.Fatalf("canceled context: %+v %v", got, err)
	}
}

func TestExportCommentOriginalUTF8(t *testing.T) {
	original := types.Comment{ID: "comment-記憶", IssueID: "web-app-a1b", Author: " Donna \n", Text: "\t空 🌱\n"}
	if err := validateExportComment(original); err != nil {
		t.Fatal(err)
	}
	for _, field := range []string{"ID", "IssueID", "Author", "Text"} {
		t.Run(field, func(t *testing.T) {
			invalid := original
			reflect.ValueOf(&invalid).Elem().FieldByName(field).SetString("original\xffbytes")
			if err := validateExportComment(invalid); !errors.Is(err, ErrInvalidStore) {
				t.Fatalf("invalid UTF-8 was admitted: %v", err)
			}
		})
	}
	if err := validateExportComment(types.Comment{}); err != nil {
		t.Fatalf("empty strings must not gain new content restrictions: %v", err)
	}
}

func TestExportSnapshotClosure(t *testing.T) {
	const scope = "https://example.test/graph/"
	snapshot := exportClosureFixture(t, scope)
	paths, err := exportSnapshotClosure(scope, snapshot)
	want := map[string]string{"web-app-a1b": "beads/work", "web-app-b2c": "beads/prereq"}
	if err != nil || !reflect.DeepEqual(paths, want) {
		t.Fatalf("closed inventory/native identities: %+v %v", paths, err)
	}
	cases := map[string]func(*Snapshot){
		"missing-endpoint": func(s *Snapshot) {
			link := s.Records[3].(LinkRecord)
			link.Target = scope + "beads/absent"
			s.Records[3] = link
		},
		"endpoint-is-link": func(s *Snapshot) {
			link := s.Records[3].(LinkRecord)
			link.Target = scope + "links/context"
			s.Records[3] = link
		},
		"missing-type":               func(s *Snapshot) { s.Types = nil },
		"missing-ownership-metadata": func(s *Snapshot) { delete(s.OwnedLinkTypes, MemoryTypeURL(scope)) },
		"wrong-type-kind": func(s *Snapshot) {
			memory := s.Records[0].(Record)
			memory.Type = scope + "types/references"
			s.Records[0] = memory
		},
		"duplicate-type":     func(s *Snapshot) { s.Types = append(s.Types, s.Types[0]) },
		"duplicate-resource": func(s *Snapshot) { s.Records = append(s.Records, s.Records[0]) },
		"missing-owned-link": func(s *Snapshot) {
			memory := s.Records[0].(Record)
			memory.Owned = memory.Owned[:1]
			s.Records[0] = memory
		},
		"duplicate-owned-link": func(s *Snapshot) {
			memory := s.Records[0].(Record)
			memory.Owned[1] = memory.Owned[0]
			s.Records[0] = memory
		},
		"owned-link-content-differs": func(s *Snapshot) {
			link := s.Records[4].(LinkRecord)
			link.Properties = map[string]any{"note": "different"}
			s.Records[4] = link
		},
		"unowned-issue-informational-link": func(s *Snapshot) {
			issue := s.Records[2].(IssueRecord)
			raw, marshalErr := canonicalJSON(s.Records[6])
			if marshalErr != nil {
				t.Fatal(marshalErr)
			}
			issue.Owned = []json.RawMessage{raw}
			s.Records[2] = issue
		},
		"duplicate-native-issue-id": func(s *Snapshot) {
			issue := s.Records[2].(IssueRecord)
			issue.Properties.ID = "web-app-a1b"
			s.Records[2] = issue
		},
		"noncanonical-identity": func(s *Snapshot) {
			memory := s.Records[0].(Record)
			memory.ID = scope + "beads/../memory"
			s.Records[0] = memory
		},
		"unknown-record": func(s *Snapshot) { s.Records = append(s.Records, "record") },
	}
	for name, corrupt := range cases {
		t.Run(name, func(t *testing.T) {
			snapshot := exportClosureFixture(t, scope)
			corrupt(&snapshot)
			paths, err := exportSnapshotClosure(scope, snapshot)
			if !errors.Is(err, ErrInvalidStore) || paths != nil {
				t.Fatalf("broken closure returned identities: %+v %v", paths, err)
			}
		})
	}
}

func exportClosureFixture(t *testing.T, scope string) Snapshot {
	t.Helper()
	snapshot := Snapshot{OwnedLinkTypes: map[string][]string{}}
	store := &Store{options: Options{Binding: Binding{ScopeURL: scope}}}
	for _, definition := range previewTypeDefinitions() {
		descriptor, err := definition.build(scope)
		if err != nil {
			t.Fatal(err)
		}
		snapshot.Types = append(snapshot.Types, descriptor)
		if descriptor.Describes() == "bead" {
			owned, err := store.SelectedOwnedLinkTypes(descriptor.ID())
			if err != nil {
				t.Fatal(err)
			}
			snapshot.OwnedLinkTypes[descriptor.ID()] = owned
		}
	}
	links := []LinkRecord{
		{ID: scope + "links/block", Type: scope + "types/native-dependency/626c6f636b73", Source: scope + "beads/work", Target: scope + "beads/prereq", Properties: map[string]any{"dep_type": "blocks", "nativeId": "fixture-native-link", "threadId": ""}},
		{ID: scope + "links/context", Type: scope + "types/subject-to", Source: scope + "beads/memory", Target: scope + "beads/work", Properties: map[string]any{}},
		{ID: scope + "links/follows", Type: scope + "types/subject-to", Source: scope + "beads/memory", Target: scope + "beads/prereq", Properties: map[string]any{}},
		{ID: scope + "links/issue-context", Type: scope + "types/references", Source: scope + "beads/prereq", Target: scope + "beads/memory", Properties: map[string]any{}},
	}
	raw := make([]json.RawMessage, len(links))
	for i, link := range links {
		var err error
		raw[i], err = canonicalJSON(link)
		if err != nil {
			t.Fatal(err)
		}
	}
	snapshot.Records = []any{
		Record{ID: scope + "beads/memory", Type: MemoryTypeURL(scope), Owned: []json.RawMessage{raw[1], raw[2]}},
		IssueRecord{ID: scope + "beads/work", Type: IssueTypeURL(scope), Properties: &publicops.Issue{ID: "web-app-a1b"}, Owned: []json.RawMessage{raw[0]}},
		IssueRecord{ID: scope + "beads/prereq", Type: IssueTypeURL(scope), Properties: &publicops.Issue{ID: "web-app-b2c"}, Owned: []json.RawMessage{}},
	}
	for _, link := range links {
		snapshot.Records = append(snapshot.Records, link)
	}
	return snapshot
}

func TestExportReadProjectionIncludesUnjoinedComments(t *testing.T) {
	var comments, terminals, legacyHeads int
	for _, part := range exportReadSizeQueries() {
		if strings.HasPrefix(part.from, "comments") {
			comments++
			if part.from != "comments" || part.columns != "id,issue_id,author,text,created_at" || part.copies != 1 {
				t.Fatalf("comment identities/orphans must be acquired within the full budget: %+v", part)
			}
		}
		if strings.Contains(part.from, "c.allocation_state='deleted' AND c.backing='issue'") {
			switch {
			case strings.HasPrefix(part.from, "graph_preview_versions d "):
				terminals++
				if part.columns != "d.snapshot,d.actor,d.native_participation_generation" || part.copies != 1 {
					t.Fatalf("native terminal must be charged without its predecessor: %+v", part)
				}
			case strings.Contains(part.from, "d.version IS NULL AND m.version=c.revision"):
				legacyHeads++
				for _, column := range []string{"m.issue_id", "m.owned", "m.revision_context", "v.durable_state", "v.change_actor", "v.attribution_status", "v.change_at"} {
					var copies int
					for _, acquired := range strings.Split(part.columns, ",") {
						if acquired == column {
							copies++
						}
					}
					if copies != 1 {
						t.Fatalf("legacy acquired column %s must be charged once: %+v", column, part)
					}
				}
				if part.copies != 1 {
					t.Fatalf("legacy final head must be charged once: %+v", part)
				}
			default:
				t.Fatalf("unexpected duplicate deleted-native projection: %+v", part)
			}
		}
	}
	if comments != 1 || terminals != 1 || legacyHeads != 1 {
		t.Fatalf("projection omitted or duplicated required data: comments=%d terminals=%d legacyHeads=%d", comments, terminals, legacyHeads)
	}
}

func TestExportSnapshotClosurePreservesEndpointPins(t *testing.T) {
	const scope = "https://example.test/graph/"
	snapshot := exportClosureFixture(t, scope)
	// Native ownership is concrete: keep the complete baseline owned set
	// valid before testing the informational Link's selected endpoint edition.
	blocking := snapshot.Records[3].(LinkRecord)
	var err error
	blocking.Type, err = NativeDependencyTypeURL(scope, "blocks")
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Records[3] = blocking
	work := snapshot.Records[1].(IssueRecord)
	work.Owned[0], err = canonicalJSON(blocking)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Records[1] = work
	link := snapshot.Records[4].(LinkRecord)
	link.TargetRevision = "old-target-version"
	snapshot.Records[4] = link
	owner := snapshot.Records[0].(Record)
	raw, err := canonicalJSON(link)
	if err != nil {
		t.Fatal(err)
	}
	owner.Owned[0] = raw
	snapshot.Records[0] = owner
	if _, err := exportSnapshotClosure(scope, snapshot); err != nil {
		t.Fatal(err)
	}
	// Identity membership alone cannot erase a disagreement about the selected edition.
	link.TargetRevision = "different-target-version"
	snapshot.Records[4] = link
	if _, err := exportSnapshotClosure(scope, snapshot); !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("pin-only owned mismatch: %v", err)
	}
	link.TargetRevision = "old-target-version"
	link.SourceRevision = "owned-source-version"
	snapshot.Records[4] = link
	if _, err := exportSnapshotClosure(scope, snapshot); !errors.Is(err, ErrCapabilityUnavailable) {
		t.Fatalf("owned source pin admitted: %v", err)
	}
}

// The retained Issue reader's acquisition contract includes its mapped native
// identity, complete owned state, payload and attribution (including timestamp).
func TestExportHistoricalIssueProjectionCoversReaderFields(t *testing.T) {
	required := []string{"m.issue_id", "m.owned", "v.durable_state", "v.change_actor", "v.attribution_status", "v.change_at"}
	found := false
	for _, projection := range exportPinnedEndpointSizeQueries() {
		if !strings.Contains(projection.from, "JOIN issue_versions") {
			continue
		}
		found = true
		columns := strings.Split(projection.columns, ",")
		for _, field := range required {
			present := false
			for _, column := range columns {
				if strings.TrimSpace(column) == field {
					present = true
				}
			}
			if !present {
				t.Fatalf("retained Issue acquisition field %s is uncharged", field)
			}
		}
	}
	if !found {
		t.Fatal("retained Issue validation projection missing")
	}
}
