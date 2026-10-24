package typecatalog

import (
	"encoding/json"
	"testing"
)

func facts(name string) EndpointFacts {
	return EndpointFacts{ResourceKind: "bead", Scope: "same", Type: ref(name)}
}

func TestCatalogCorrelatedEndpointAlternatives(t *testing.T) {
	relation := link("relation", endpoint("a"), endpoint("b"))
	relation.EndpointPairs = append(relation.EndpointPairs, EndpointPair{Source: endpoint("b"), Target: endpoint("a")})
	c := mustCatalog(t, bead("a"), bead("b"), relation,
		link("specialized", endpoint("a"), endpoint("b"), "relation"))
	for _, tc := range []struct {
		source, target string
		valid          bool
	}{
		{"a", "b", true}, {"b", "a", true}, {"a", "a", false}, {"b", "b", false},
	} {
		err := c.ValidateEndpoints(ref("relation"), facts(tc.source), facts(tc.target))
		if (err == nil) != tc.valid {
			t.Fatalf("%s -> %s: %v", tc.source, tc.target, err)
		}
	}
	if err := c.ValidateEndpoints(ref("specialized"), facts("b"), facts("a")); err == nil {
		t.Fatal("parent's wider alternatives weakened child restriction")
	}
	// A child-local wildcard cannot weaken the parent's correlated pair rules.
	wide := link("wide", EndpointConstraint{ResourceKind: "bead", Scope: "either", Types: Selector{Any: true}},
		EndpointConstraint{ResourceKind: "bead", Scope: "either", Types: Selector{Any: true}}, "relation")
	c = mustCatalog(t, bead("a"), bead("b"), relation, wide)
	if err := c.ValidateEndpoints(ref("wide"), facts("a"), facts("a")); err == nil {
		t.Fatal("child wildcard bypassed inherited pair restrictions")
	}
}

func TestCatalogEndpointEvidence(t *testing.T) {
	rule := endpoint("a")
	rule.Scope = "cross"
	opaque := EndpointConstraint{ResourceKind: "resource"}
	c := mustCatalog(t, bead("a"), link("citation", rule, opaque))
	source := facts("a")
	source.Scope = "cross"
	resource := EndpointFacts{ResourceKind: "resource"}
	if err := c.ValidateEndpoints(ref("citation"), source, resource); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []EndpointFacts{{}, facts("a"), {ResourceKind: "link"}, {ResourceKind: "resource", Type: ref("a")}} {
		if err := c.ValidateEndpoints(ref("citation"), source, bad); err == nil {
			t.Fatalf("bad resource evidence admitted: %+v", bad)
		}
	}
	if err := c.ValidateEndpoints(ref("citation"), facts("a"), resource); err == nil {
		t.Fatal("same-scope source matched cross-scope constraint")
	}
}

func TestCatalogPropertiesComposeWithoutDiscard(t *testing.T) {
	root := bead("root")
	root.PropertiesSchema = json.RawMessage(`{"type":"object","properties":{"title":{"type":"string","minLength":1}},"required":["title"]}`)
	child := bead("child", "root")
	child.PropertiesSchema = json.RawMessage(`{"type":"object","properties":{"status":{"enum":["draft","accepted"]}},"required":["status"]}`)
	c := mustCatalog(t, root, child)
	for _, tc := range []struct {
		raw   string
		valid bool
	}{
		{`{"title":"T","status":"draft","extra":true}`, true},
		{`{"title":"T","status":"unknown"}`, false},
		{`{"status":"draft"}`, false},
		{`{"title":"T"}`, false},
		{`{"title":null,"status":"draft"}`, false},
		{`{"title":"T","status":"draft","title":"other"}`, false},
		{`null`, false}, {`[]`, false},
	} {
		input := json.RawMessage(tc.raw)
		err := c.ValidateProperties(ref("child"), input)
		if (err == nil) != tc.valid {
			t.Fatalf("%s: %v", input, err)
		}
		if string(input) != tc.raw {
			t.Fatal("validation rewrote properties")
		}
	}
	// A closed parent remains closed; child fields are not silently deleted.
	root.PropertiesSchema = json.RawMessage(`{"type":"object","properties":{"title":{"type":"string"}},"additionalProperties":false}`)
	c = mustCatalog(t, root, child)
	if err := c.ValidateProperties(ref("child"), json.RawMessage(`{"title":"T","status":"draft"}`)); err == nil {
		t.Fatal("child relaxed parent's closed object")
	}
}

func TestCatalogOfflineSchemaResources(t *testing.T) {
	d := bead("a")
	d.PropertiesSchema = json.RawMessage(`{"$ref":"https://schemas.example/common"}`)
	if _, err := New([]Definition{d}); err == nil {
		t.Fatal("missing retained schema admitted")
	}
	d.SchemaResources = map[string]json.RawMessage{
		"https://schemas.example/common": json.RawMessage(`{"type":"object","required":["body"]}`),
	}
	c := mustCatalog(t, d)
	d.SchemaResources["https://schemas.example/common"][0] = 'x'
	if err := c.ValidateProperties(ref("a"), json.RawMessage(`{}`)); err == nil {
		t.Fatal("retained resource not applied")
	}
	if err := c.ValidateProperties(ref("a"), json.RawMessage(`{"body":"B"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRejectsMalformedSchemas(t *testing.T) {
	for _, schema := range []string{`null`, `[]`, `{"type":123}`, `{"$ref":"file:///private/no-network-or-file-load"}`, `{"$schema":"http://json-schema.org/draft-07/schema#"}`,
		`{"$defs":{"embedded":{"$id":"https://schemas.example/embedded","$schema":"http://json-schema.org/draft-07/schema#"}}}`,
		`{"properties":{"x":{"$schema":"https://json-schema.org/draft/2020-12/schema"}}}`,
		`{"properties":{"x":{"format":"unknown-format"}}}`,
		`{"format":"idn-email"}`,
	} {
		d := bead("a")
		d.PropertiesSchema = json.RawMessage(schema)
		if _, err := New([]Definition{d}); err == nil {
			t.Fatalf("bad schema admitted: %s", schema)
		}
	}
}

func TestCatalogFormatsAndLiteralSchemaWords(t *testing.T) {
	d := bead("a")
	d.PropertiesSchema = json.RawMessage(`{"properties":{"at":{"type":"string","format":"date-time"}},"required":["at"],"default":{"$schema":"literal data","format":"literal data"},"examples":[{"$schema":"also data"}]}`)
	c := mustCatalog(t, d)
	if err := c.ValidateProperties(ref("a"), json.RawMessage(`{"at":"2026-10-19T07:00:00Z"}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateProperties(ref("a"), json.RawMessage(`{"at":"not a date"}`)); err == nil {
		t.Fatal("format assertion was ignored")
	}
}

func TestCatalogReferencedSchemaBaseline(t *testing.T) {
	for _, schema := range []string{
		`{"$ref":"#/definitions/object","definitions":{"object":{"properties":{"x":{"format":"unknown-format"}}}}}`,
		`{"definitions":{"embedded":{"$id":"https://schemas.example/embedded","$schema":"http://json-schema.org/draft-07/schema#"}}}`,
		`{"$ref":"#/opaque","opaque":{"properties":{"x":{"format":"unknown-format"}}}}`,
		`{"$ref":"#/opaque","opaque":{"$schema":"https://json-schema.org/draft/2020-12/schema"}}`,
		`{"$ref":"#/opaque","opaque":{"$id":"https://schemas.example/embedded","$schema":"http://json-schema.org/draft-07/schema#"}}`,
	} {
		d := bead("a")
		d.PropertiesSchema = json.RawMessage(schema)
		if _, err := New([]Definition{d}); err == nil {
			t.Fatalf("referenced/legacy schema bypassed admission: %s", schema)
		}
	}
	// An unknown annotation object is data until a reference actually selects it
	// as a schema; imposing schema rules on all objects would reject this input.
	d := bead("a")
	d.PropertiesSchema = json.RawMessage(`{"opaque":{"$schema":"literal data","format":"literal data"}}`)
	mustCatalog(t, d)
	// A valid pointer target outside known keyword positions still works and
	// its format is asserted, rather than forbidding such references wholesale.
	d.PropertiesSchema = json.RawMessage(`{"$ref":"#/opaque","opaque":{"properties":{"at":{"format":"date-time"}},"required":["at"]}}`)
	c := mustCatalog(t, d)
	if err := c.ValidateProperties(ref("a"), json.RawMessage(`{"at":"invalid"}`)); err == nil {
		t.Fatal("referenced format was ignored")
	}
	if err := c.ValidateProperties(ref("a"), json.RawMessage(`{"at":"2026-10-19T07:00:00Z"}`)); err != nil {
		t.Fatal(err)
	}
}

func TestCatalogRejectsCompilerReservedRetrievalURIs(t *testing.T) {
	for _, prefix := range []string{"http://json-schema.org/", "https://json-schema.org/"} {
		for _, schema := range []string{`{"type":"object","required":"title"}`, `{"type":"object"}`} {
			t.Run(prefix+schema, func(t *testing.T) {
				d := bead("a")
				d.SchemaBase = prefix + "beads/application"
				d.PropertiesSchema = json.RawMessage(schema)
				if _, err := New([]Definition{d}); err == nil {
					t.Fatal("reserved application SchemaBase admitted")
				}
				// The root is ordinary; the separately retained resource is
				// deliberately unused so admission cannot rely on a $ref.
				d = bead("a")
				d.SchemaResources = map[string]json.RawMessage{
					prefix + "beads/retained": json.RawMessage(schema),
				}
				if _, err := New([]Definition{d}); err == nil {
					t.Fatal("reserved retained-resource retrieval key admitted")
				}
			})
		}
	}
}

func TestCatalogStandardDialectRetrievalControls(t *testing.T) {
	for _, base := range []string{"https://schemas.example/application", "https://json-schema.org.example/application"} {
		d := bead("a")
		d.SchemaBase = base
		d.PropertiesSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","properties":{"title":{"type":"string"}},"required":["title"]}`)
		c := mustCatalog(t, d)
		if err := c.ValidateProperties(ref("a"), json.RawMessage(`{"title":"T"}`)); err != nil {
			t.Fatal(err)
		}
		if err := c.ValidateProperties(ref("a"), json.RawMessage(`{}`)); err == nil {
			t.Fatal("ordinary retrieval schema constraint was lost")
		}
		got, _ := c.Definition(ref("a").URI)
		if got.SchemaBase != base {
			t.Fatal("retrieval identity was rewritten")
		}
	}
	d := bead("a")
	d.PropertiesSchema = json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","$ref":"https://schemas.example/retained"}`)
	d.SchemaResources = map[string]json.RawMessage{
		"https://schemas.example/retained": json.RawMessage(`{"$schema":"https://json-schema.org/draft/2020-12/schema","type":"object","required":["body"]}`),
	}
	c := mustCatalog(t, d)
	if err := c.ValidateProperties(ref("a"), json.RawMessage(`{"body":"B"}`)); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateProperties(ref("a"), json.RawMessage(`{}`)); err == nil {
		t.Fatal("ordinary retained schema constraint was lost")
	}
}
