//go:build cgo

package graphstore

import (
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/legacyimport"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

func TestLegacyImportIdentityMount(t *testing.T) {
	for _, root := range []string{"", "foo/bar"} {
		t.Run(root, func(t *testing.T) {
			input := legacyimport.Batch{Issues: []*types.Issue{
				{ID: "old-a", Title: "Source", Dependencies: []*types.Dependency{{DependsOnID: "old-b", Type: types.DepRelatesTo}}, Comments: []*types.Comment{{ID: "original-comment", Author: "original-author", Text: " note "}}},
				{ID: "old-b", Title: "Target"},
			}, Memories: []legacyimport.Memory{{Key: "beads/literal", Value: "  exact body \n"}, {Key: "types/literal", Value: ""}}}
			before, err := json.Marshal(input)
			if err != nil {
				t.Fatal(err)
			}
			prepared, err := prepareLegacyImportWithPrefix(input, "importer", "old")
			if err != nil {
				t.Fatal(err)
			}
			mounted, err := mountLegacyImport(prepared, root)
			if err != nil {
				t.Fatal(err)
			}
			prefix := root
			if root != "" {
				prefix += "/"
			}
			a, b := mounted.Issues[0], mounted.Issues[1]
			if a.ID != prefix+"old-a" || b.ID != prefix+"old-b" || mounted.prefix != "old" {
				t.Fatalf("wrong mounted Issues/prefix: %+v", mounted)
			}
			if a.Dependencies[0].IssueID != a.ID || a.Dependencies[0].DependsOnID != b.ID || a.Dependencies[0].Type != types.DepRelatesTo {
				t.Fatalf("wrong dependency: %+v", a.Dependencies[0])
			}
			c := a.Comments[0]
			if c.IssueID != a.ID || c.ID != "original-comment" || c.Author != "original-author" || c.Text != " note " {
				t.Fatalf("wrong comment: %+v", c)
			}
			if !reflect.DeepEqual(mounted.sourceIssues, map[string]string{a.ID: "old-a", b.ID: "old-b"}) || mounted.memoryIDs["beads/literal"] != prefix+"beads/literal" || mounted.memoryIDs["types/literal"] != prefix+"types/literal" {
				t.Fatalf("wrong original-source maps: %+v", mounted)
			}
			if mounted.Memories[0].Value != input.Memories[0].Value || mounted.Memories[1].Value != "" {
				t.Fatal("body changed")
			}
			after, err := json.Marshal(input)
			if err != nil || string(before) != string(after) {
				t.Fatalf("caller input changed: %s -> %s (%v)", before, after, err)
			}
		})
	}
}

func TestLegacyImportIdentityAdmission(t *testing.T) {
	for _, key := range []string{"..", "a//b", "has space", "control\x00", " ", "old-a"} {
		t.Run(key, func(t *testing.T) {
			input := legacyimport.Batch{Issues: []*types.Issue{{ID: "old-a", Title: "Issue"}}, Memories: []legacyimport.Memory{{Key: key, Value: "exact"}}}
			prepared, err := prepareLegacyImportWithPrefix(input, "importer", "old")
			if err != nil {
				t.Fatal(err)
			}
			before, err := json.Marshal(prepared.Batch)
			if err != nil {
				t.Fatal(err)
			}
			result, err := mountLegacyImport(prepared, "root")
			if !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), "Memory key") || !reflect.DeepEqual(result, legacyImportMount{}) {
				t.Fatalf("wrong compatibility refusal: %+v %v", result, err)
			}
			after, marshalErr := json.Marshal(prepared.Batch)
			if marshalErr != nil || string(before) != string(after) {
				t.Fatal("refusal mutated detached batch")
			}
		})
	}
	for _, root := range []string{"..", "a//b", "/absolute", "foo/", "foo?query"} {
		prepared, err := prepareLegacyImportWithPrefix(legacyimport.Batch{Memories: []legacyimport.Memory{{Key: "valid"}}}, "importer", "")
		if err != nil {
			t.Fatal(err)
		}
		if _, err := mountLegacyImport(prepared, root); !errors.Is(err, storage.ErrValidation) {
			t.Fatalf("invalid root %q accepted: %v", root, err)
		}
	}
	// This cap belongs to native Issue backing mappings, not general Memory IDs.
	for _, size := range []int{249, 250} {
		prepared, err := prepareLegacyImportWithPrefix(legacyimport.Batch{Issues: []*types.Issue{{ID: "old-a", Title: "Issue"}}}, "importer", "old")
		if err != nil {
			t.Fatal(err)
		}
		got, err := mountLegacyImport(prepared, strings.Repeat("r", size))
		if size == 249 && (err != nil || len(got.Issues[0].ID) != 255) {
			t.Fatalf("255-byte native boundary refused: %v", err)
		}
		if size == 250 && (!errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), "old-a") || !strings.Contains(err.Error(), "255 bytes")) {
			t.Fatalf("256-byte native boundary accepted: %v", err)
		}
	}
	prepared, err := prepareLegacyImportWithPrefix(legacyimport.Batch{Memories: []legacyimport.Memory{{Key: strings.Repeat("m", 256)}}}, "importer", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := mountLegacyImport(prepared, "root"); err != nil {
		t.Fatalf("native backing cap incorrectly applied to Memory: %v", err)
	}
}

func TestLegacyImportIdentityExpansionBudget(t *testing.T) {
	input := legacyimport.Batch{Memories: []legacyimport.Memory{{Key: "a"}, {Key: "b"}}}
	prepared, err := prepareLegacyImportWithPrefix(input, "importer", "")
	if err != nil {
		t.Fatal(err)
	}
	got, err := mountLegacyImport(prepared, strings.Repeat("r", PreviewCurrentReadByteLimit/2))
	if !errors.Is(err, ErrLimitExceeded) || !reflect.DeepEqual(got, legacyImportMount{}) {
		t.Fatalf("wrong expanded identity refusal: %+v %v", got, err)
	}
	if prepared.Memories[0].Key != "a" || prepared.Memories[1].Key != "b" {
		t.Fatal("budget refusal changed source identities")
	}
}

func TestLegacyImportIdentityNativeRouting(t *testing.T) {
	input := legacyimport.Batch{Issues: []*types.Issue{
		{ID: "old-a", Title: "Source", Dependencies: []*types.Dependency{{DependsOnID: "old-b", Type: types.DepBlocks}}},
		{ID: "old-b", Title: "Target"},
	}}
	prepared, err := prepareLegacyImportWithPrefix(input, "importer", "old")
	if err != nil {
		t.Fatal(err)
	}
	before, err := json.Marshal(prepared.Batch)
	if err != nil {
		t.Fatal(err)
	}
	got, err := mountLegacyImport(prepared, "external:x")
	if !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), "external:x") || !strings.Contains(err.Error(), "old-a") || !reflect.DeepEqual(got, legacyImportMount{}) {
		t.Fatalf("wrong native root routing refusal: %+v %v", got, err)
	}
	after, err := json.Marshal(prepared.Batch)
	if err != nil || string(before) != string(after) {
		t.Fatal("native routing refusal mutated detached batch")
	}
}
