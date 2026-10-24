package typecatalog

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	graph "github.com/steveyegge/beads/graphops"
)

// Synthetic fixtures deliberately do not encode the pending Memory catalog.
func ref(name string) Reference {
	return Reference{URI: "https://catalog.example/types/" + name, Revision: "r1"}
}

func bead(name string, parents ...string) Definition {
	d := Definition{
		Identity: ref(name), Name: name, Kind: graph.KindBead, Public: true,
		PropertiesSchema: json.RawMessage(`{"type":"object"}`),
		SchemaBase:       "https://schemas.example/" + name,
	}
	for _, parent := range parents {
		d.Parents = append(d.Parents, ref(parent))
	}
	return d
}

func endpoint(names ...string) EndpointConstraint {
	e := EndpointConstraint{ResourceKind: "bead", Scope: "same"}
	for _, name := range names {
		e.Types.AnyOf = append(e.Types.AnyOf, ref(name))
	}
	return e
}

func link(name string, source, target EndpointConstraint, parents ...string) Definition {
	d := bead(name, parents...)
	d.Kind = graph.KindLink
	d.EndpointPairs = []EndpointPair{{Source: source, Target: target}}
	return d
}

func mustCatalog(t *testing.T, definitions ...Definition) *Catalog {
	t.Helper()
	c, err := New(definitions)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCatalogRejectsInvalidGraph(t *testing.T) {
	for _, tc := range []struct {
		name        string
		definitions []Definition
	}{
		{"duplicate", []Definition{bead("a"), bead("a")}},
		{"missing parent", []Definition{bead("a", "missing")}},
		{"cycle", []Definition{bead("a", "b"), bead("b", "a")}},
		{"duplicate parent", []Definition{bead("a"), bead("b", "a", "a")}},
		{"kind mismatch", []Definition{bead("a"), link("b", endpoint("a"), endpoint("a"), "a")}},
		{"missing endpoint type", []Definition{bead("a"), link("b", endpoint("missing"), endpoint("a"))}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := New(tc.definitions); !errors.Is(err, graph.ErrValidation) {
				t.Fatalf("want validation refusal, got %v", err)
			}
		})
	}
}

func TestCatalogDiamondOwnershipAndExactReferences(t *testing.T) {
	root := bead("root")
	root.OwnsOutgoing = []Selector{{AnyOf: []Reference{ref("association")}}}
	c := mustCatalog(t, root, bead("left", "root"), bead("right", "root"), bead("leaf", "left", "right"),
		link("association", endpoint("root"), endpoint("root")),
		link("citation", endpoint("leaf"), endpoint("root"), "association"))
	closure, err := c.Closure(ref("leaf"))
	if err != nil || len(closure) != 4 {
		t.Fatalf("diamond closure = %v, %v", closure, err)
	}
	if want := []Reference{ref("leaf"), ref("left"), ref("right"), ref("root")}; !reflect.DeepEqual(closure, want) {
		t.Fatalf("closure = %v, want %v", closure, want)
	}
	owned, err := c.Owns(ref("leaf"), ref("citation"))
	if !owned || err != nil {
		t.Fatalf("inherited specialized ownership = %v, %v", owned, err)
	}
	if c.Conforms(Reference{URI: ref("leaf").URI, Revision: "stale"}, ref("root")) ||
		c.Conforms(ref("leaf"), Reference{URI: ref("root").URI, Revision: "stale"}) ||
		c.Conforms(ref("missing"), ref("missing")) {
		t.Fatal("unknown or wrong-revision reference established conformance")
	}
	if !c.Conforms(Reference{URI: ref("leaf").URI}, Reference{URI: ref("root").URI}) {
		t.Fatal("identity-only reference did not match this fixed selection")
	}
}

func TestCatalogVisibilityAndIsolation(t *testing.T) {
	hidden := bead("BeadType")
	hidden.Public = false
	root := bead("root")
	root.OwnsOutgoing = []Selector{{AnyOf: []Reference{ref("association")}}}
	definitions := []Definition{root, hidden, bead("leaf", "root"), link("association", endpoint("root"), endpoint("root"))}
	c := mustCatalog(t, definitions...)
	definitions[0].OwnsOutgoing[0].AnyOf[0] = ref("missing")
	definitions[2].Parents[0] = ref("missing")
	definitions[3].EndpointPairs[0].Source.Types.AnyOf[0] = ref("missing")
	view := c.Public(graph.KindBead)
	if len(view) != 2 || view[0].Identity != ref("leaf") || view[1].Identity != ref("root") {
		t.Fatalf("public Beads = %v", view)
	}
	view[1].OwnsOutgoing[0].AnyOf[0] = ref("missing")
	copy, _ := c.Definition(ref("association").URI)
	copy.EndpointPairs[0].Source.Types.AnyOf[0] = ref("missing")
	if owned, err := c.Owns(ref("leaf"), ref("association")); !owned || err != nil {
		t.Fatalf("mutation leaked into catalog: %v, %v", owned, err)
	}
	if err := c.ValidateEndpoints(ref("association"), facts("leaf"), facts("root")); err != nil {
		t.Fatal(err)
	}
	hidden.Public = true
	if _, err := New([]Definition{hidden}); err == nil {
		t.Fatal("public intrinsic metatype admitted")
	}
}

func TestCatalogSelectorsAndCategoryRestrictions(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*Definition)
	}{
		{"empty selector", func(d *Definition) { d.OwnsOutgoing = []Selector{{}} }},
		{"both selector arms", func(d *Definition) { d.OwnsOutgoing = []Selector{{Any: true, AnyOf: []Reference{ref("association")}}} }},
		{"Bead ownership target", func(d *Definition) { d.OwnsOutgoing = []Selector{{AnyOf: []Reference{ref("root")}}} }},
		{"duplicate selector entry", func(d *Definition) {
			d.OwnsOutgoing = []Selector{{AnyOf: []Reference{ref("association"), ref("association")}}}
		}},
		{"wrong parent revision", func(d *Definition) { d.Parents = []Reference{{URI: ref("root").URI, Revision: "r0"}} }},
		{"Bead endpoint pairs", func(d *Definition) {
			d.EndpointPairs = []EndpointPair{{Source: endpoint("root"), Target: endpoint("root")}}
		}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			child := bead("child", "root")
			tc.change(&child)
			if _, err := New([]Definition{bead("root"), child, link("association", endpoint("root"), endpoint("root"))}); err == nil {
				t.Fatal("invalid declaration admitted")
			}
		})
	}
	root := bead("root")
	root.OwnsOutgoing = []Selector{{Any: true}}
	c := mustCatalog(t, root, link("association", endpoint("root"), endpoint("root")))
	if owns, err := c.Owns(ref("root"), ref("association")); !owns || err != nil {
		t.Fatalf("wildcard selection = %v, %v", owns, err)
	}
}

func TestAbstractDeclarationAndNativeDomainBoundary(t *testing.T) {
	abstract := link("abstract", endpoint("native"), endpoint("native"))
	abstract.Abstract = true
	native := bead("native")
	native.NominalOnly = true
	native.PropertiesSchema, native.SchemaBase = nil, ""
	c := mustCatalog(t, native, abstract, link("concrete", endpoint("native"), endpoint("native"), "abstract"))
	if err := c.ValidateDeclaredType(ref("abstract"), graph.KindLink); !errors.Is(err, graph.ErrValidation) {
		t.Fatalf("abstract direct declaration: %v", err)
	}
	if err := c.ValidateDeclaredType(ref("concrete"), graph.KindLink); err != nil {
		t.Fatalf("concrete refinement inherited abstractness: %v", err)
	}
	if !c.Conforms(ref("concrete"), ref("abstract")) {
		t.Fatal("abstract family lost nominal matching")
	}
	if err := c.ValidateEndpoints(ref("concrete"), facts("native"), facts("native")); err != nil {
		t.Fatalf("native nominal endpoint: %v", err)
	}
	if err := c.ValidateDeclaredType(ref("native"), graph.KindBead); err != nil {
		t.Fatal(err)
	}
	if err := c.ValidateProperties(ref("native"), json.RawMessage(`{}`)); !errors.Is(err, graph.ErrValidation) {
		t.Fatalf("nominal facts substituted for native payload validation: %v", err)
	}
	for _, mutate := range []func(*Definition){
		func(d *Definition) { d.PropertiesSchema = json.RawMessage(`true`) },
		func(d *Definition) { d.SchemaBase = "https://schemas.example/native" },
		func(d *Definition) {
			d.SchemaResources = map[string]json.RawMessage{"https://schemas.example/extra": json.RawMessage(`true`)}
		},
	} {
		bad := native
		mutate(&bad)
		if _, err := New([]Definition{bad}); !errors.Is(err, graph.ErrValidation) {
			t.Fatalf("nominal-only schema admitted: %v", err)
		}
	}
	child := bead("child", "native")
	c = mustCatalog(t, native, child)
	if err := c.ValidateProperties(ref("child"), json.RawMessage(`{}`)); !errors.Is(err, graph.ErrValidation) {
		t.Fatalf("refinement bypassed native ancestor validation: %v", err)
	}
	if _, err := New([]Definition{bead("Refines")}); !errors.Is(err, graph.ErrValidation) {
		t.Fatalf("public Refines intrinsic admitted: %v", err)
	}
}
