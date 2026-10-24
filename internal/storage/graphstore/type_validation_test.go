package graphstore

import (
	"encoding/json"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
	"github.com/steveyegge/beads/internal/graphpatch"
)

func TestTypeValidationBeadScanAcceptsNativeNULLAndGenericBytes(t *testing.T) {
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		mock.ExpectClose()
		if err := db.Close(); err != nil {
			t.Error(err)
		}
		if err := mock.ExpectationsWereMet(); err != nil {
			t.Error(err)
		}
	})
	const generic = `{"title":"Policy","body":"","extension":{"large":9007199254740994}}`
	mock.ExpectQuery("selected bead rows").WillReturnRows(sqlmock.NewRows([]string{"path", "type_url", "backing", "properties"}).
		AddRow("beads/native", "native-type", "issue", nil).
		AddRow("beads/generic", "memory-type", "generic", []byte(generic)).
		AddRow("beads/missing", "memory-type", "generic", nil))
	rows, err := db.Query("selected bead rows")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := rows.Close(); err != nil {
			t.Error(err)
		}
	}()
	for _, expected := range []struct{ path, typ, backing, properties string }{
		{"beads/native", "native-type", "issue", ""},
		{"beads/generic", "memory-type", "generic", generic},
		{"beads/missing", "memory-type", "generic", ""},
	} {
		if !rows.Next() {
			t.Fatalf("missing row: %v", rows.Err())
		}
		got, err := scanTypeValidationBead(rows)
		if err != nil {
			t.Fatal(err)
		}
		if got.path != expected.path || got.typ != expected.typ || got.backing != expected.backing || string(got.properties) != expected.properties {
			t.Fatalf("nullable acquisition changed endpoint facts or generic properties: %+v", got)
		}
		if expected.properties == "" && got.properties != nil {
			t.Fatal("SQL NULL became a synthetic properties object")
		}
	}
	if rows.Next() || rows.Err() != nil {
		t.Fatalf("unexpected trailing row/error: %v", rows.Err())
	}
}

func TestMemoryPropertiesPreserveTypedExtensions(t *testing.T) {
	const raw = `{"title":"Policy","body":"","authority":{"uri":"https://policy.example/rules","revision":"r1"},"status":"draft","version":"vNext","extension":{"large":9007199254740994,"items":[true,null,"text"]}}`
	var properties Properties
	if err := json.Unmarshal([]byte(raw), &properties); err != nil {
		t.Fatal(err)
	}
	before, err := canonicalJSON(properties)
	if err != nil {
		t.Fatal(err)
	}
	expected, err := canonicalJSON(json.RawMessage(raw))
	if err != nil {
		t.Fatal(err)
	}
	if string(before) != string(expected) {
		t.Fatalf("roundtrip lost properties: %s", before)
	}
	copy := properties
	copy.Title = "Revised policy"
	if properties.Title != "Policy" || copy.extra != properties.extra {
		t.Fatal("value copy changed the predecessor or discarded extensions")
	}
	encoded, err := canonicalJSON(copy)
	if err != nil {
		t.Fatal(err)
	}
	var roundtrip Properties
	if err := json.Unmarshal(encoded, &roundtrip); err != nil || roundtrip != copy {
		t.Fatalf("comparable copy did not roundtrip: %v", err)
	}
}

func TestMemoryPropertyPatchPreservesUneditedTypedFields(t *testing.T) {
	var before Properties
	if err := json.Unmarshal([]byte(`{"title":"T","body":"B","authority":{"uri":"https://policy.example/","revision":"r1"},"status":"draft"}`), &before); err != nil {
		t.Fatal(err)
	}
	patch, err := graphpatch.Parse([]byte(`[{"op":"replace","path":"/title","value":"Revised"}]`))
	if err != nil {
		t.Fatal(err)
	}
	after, err := applyMemoryPropertiesPatch(patch, before)
	if err != nil {
		t.Fatal(err)
	}
	if after.Title != "Revised" || after.Body != before.Body || after.extra != before.extra || before.Title != "T" {
		t.Fatal("patch changed an unedited property or its predecessor")
	}
}

func TestMemoryPropertiesRefuseLossyOrMalformedInput(t *testing.T) {
	for _, raw := range []string{
		`null`, `[]`, `{}`, `{"title":"T"}`, `{"title":null,"body":""}`,
		`{"title":"T","body":false}`, `{"title":"first","title":"second","body":""}`,
		`{"title":"T","body":"","extra":{"x":1,"x":2}}`,
		`{"title":"T","body":"","number":9007199254740993}`,
	} {
		before := Properties{Title: "unchanged", Body: "unchanged"}
		after := before
		if err := json.Unmarshal([]byte(raw), &after); err == nil {
			t.Fatalf("malformed properties admitted: %s", raw)
		}
		if after != before {
			t.Fatalf("refused input mutated destination: %s", raw)
		}
	}
	var plain Properties
	if err := json.Unmarshal([]byte(`{"title":"T","body":""}`), &plain); err != nil || plain != (Properties{Title: "T"}) {
		t.Fatalf("legacy common fields changed: %+v %v", plain, err)
	}
	if _, err := json.Marshal(Properties{Title: string([]byte{0xff})}); err == nil {
		t.Fatal("encoding/json replaced invalid direct Go text")
	}
}
