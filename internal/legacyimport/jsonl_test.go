package legacyimport

import (
	"strings"
	"testing"
)

func TestOrdinaryExportVocabulary(t *testing.T) {
	batch, err := Parse(strings.NewReader(`{"_type":"issue","id":"old-a","title":"Work","priority":0,"dependency_count":0,"dependent_count":0,"comment_count":1,"metadata":{"custom":[1,true]},"comments":[{"id":7,"issue_id":"old-a","author":"a","text":"note","created_at":"2026-01-01T00:00:00Z"}]}
{"_type":"memory","key":"context","value":"remember this"}
`))
	if err != nil {
		t.Fatal(err)
	}
	if len(batch.Issues) != 1 || len(batch.Memories) != 1 || batch.Issues[0].Comments[0].ID != "7" || batch.Issues[0].Priority != 0 {
		t.Fatalf("batch=%+v", batch)
	}
}

func TestLegacyInputRefusals(t *testing.T) {
	for _, input := range []string{
		`{"id":"x","id":"y"}`, `{"metadata":{"x":1,"x":2}}`,
		`{"title":"\ud800"}`, `{"\ud800":"x"}`, `{"comments":[{"id":1.5}]}`, `{"comments":[{"id":null}]}`,
		`{"_schema":"future"}`,
		`{"title":"original","Title":"replacement"}`, `{"_type":"memory","key":"x","value":"keep","Value":"drop"}`,
		`{"dependencies":[{"type":"blocks","Type":"parent-child"}]}`, `{"bonded_from":[{"source_id":"x","future":"lost"}]}`,
		`{"bonded_from":[{"source_id":"a","proto_id":"b","bond_type":"parallel"}]}`,
		`{"priority":null}`, `{"no_history":null}`, `{"_type":null,"id":"x"}`, `{"dependent_count":null}`, `{"wisp":null}`,
		`{"_type":"memory","key":"x"}`, `{"_type":"memory","key":"x","value":null}`,
		`null`, `[]`, `{"title":"first"} trailing`, `{"id":"x","title":"x","history":[]}`,
		`{"id":"https://example.invalid/beads/x","type":"types/issue","properties":{"title":"x"}}`,
		`{"_type":"graph","id":"x"}`, `{"_type":"memory","key":"x","value":"v","future":1}`,
		`{"title":"x","wisp":true}`, `{"title":"x","wisp_plane":true}`,
		`{"title":"x","dependency_count":-1}`, `{"title":"x","comments":[{"text":"x","history":[]}]}`,
		`{"_schema":1,"title":"lost"}`, string([]byte{'{', '"', 'x', '"', ':', '"', 255, '"', '}'}),
	} {
		t.Run(input, func(t *testing.T) {
			if _, err := Parse(strings.NewReader(input)); err == nil {
				t.Fatal("accepted unsupported input")
			}
		})
	}
	if _, err := Parse(strings.NewReader(strings.Repeat(" ", MaxBytes+1))); err == nil {
		t.Fatal("accepted oversized stream")
	}
}

func TestHistoricalHeaderAndExactCommentID(t *testing.T) {
	input := `{"_dolt_branch":"main","_dolt_commit":"abc123","_project_id":"p1","_schema":"beads-jsonl/1","_sort":"stable-v1"}
{"id":"old-a","title":"Work","comments":[{"id":9223372036854775807,"text":"note"}]}`
	batch, err := Parse(strings.NewReader(input))
	if err != nil {
		t.Fatal(err)
	}
	if batch.Issues[0].Comments[0].ID != "9223372036854775807" {
		t.Fatal("numeric comment ID changed")
	}
}

func TestHistoricalBondRef(t *testing.T) {
	batch, err := Parse(strings.NewReader(`{"id":"old-a","title":"Work","bonded_from":[{"proto_id":"old-source","bond_type":"parallel"}]}`))
	if err != nil {
		t.Fatal(err)
	}
	if batch.Issues[0].BondedFrom[0].SourceID != "old-source" {
		t.Fatal("historical BondRef lost")
	}
}
