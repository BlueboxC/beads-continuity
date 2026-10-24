package typeconstraints

import (
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/typecatalog"
)

const scope = "https://graph.example/work/"

func reference(name string) typecatalog.Reference {
	return typecatalog.Reference{URI: scope + "types/" + name, Revision: "r1"}
}

func catalogForTest(t *testing.T) *typecatalog.Catalog {
	t.Helper()
	var definitions []typecatalog.Definition
	for _, name := range []string{"bead", "relation", "special", "other"} {
		d := typecatalog.Definition{
			Identity: reference(name), Name: name, Kind: graph.KindBead,
			PropertiesSchema: json.RawMessage(`{"type":"object"}`), SchemaBase: scope + "schemas/" + name,
		}
		if name != "bead" {
			d.Kind = graph.KindLink
			endpoint := typecatalog.EndpointConstraint{ResourceKind: "bead", Scope: "either", Types: typecatalog.Selector{Any: true}}
			d.EndpointPairs = []typecatalog.EndpointPair{{Source: endpoint, Target: endpoint}}
		}
		if name == "special" {
			d.Parents = []typecatalog.Reference{reference("relation")}
		}
		definitions = append(definitions, d)
	}
	c, err := typecatalog.New(definitions)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func live(id, family, source, target string) Link {
	return Link{
		Identity: Identity{Scope: scope, Path: "links/" + id}, Type: reference(family),
		Source: Endpoint{Identity: Identity{Scope: scope, Path: "beads/" + source}},
		Target: Endpoint{Identity: Identity{Scope: scope, Path: "beads/" + target}},
	}
}

func check(t *testing.T, rules []Rule, links []Link, valid bool) {
	t.Helper()
	v, err := New(catalogForTest(t), rules)
	if err != nil {
		t.Fatal(err)
	}
	err = v.Validate(links)
	if valid && err != nil {
		t.Fatal(err)
	}
	if !valid && !errors.Is(err, graph.ErrValidation) {
		t.Fatalf("want validation refusal, got %v", err)
	}
}

func TestFamilyGraphRestrictions(t *testing.T) {
	family := reference("relation")
	for _, tc := range []struct {
		name  string
		rule  Rule
		links []Link
		valid bool
	}{
		{"unconstrained cycles", Rule{Family: family}, []Link{live("a", "relation", "a", "b"), live("b", "special", "b", "a")}, true},
		{"unconstrained parallel", Rule{Family: family}, []Link{live("a", "relation", "a", "b"), live("b", "special", "a", "b")}, true},
		{"family source maximum", Rule{Family: family, SourceMax: Limit{Set: true, Value: 1}}, []Link{live("a", "relation", "a", "b"), live("b", "special", "a", "c")}, false},
		{"family target maximum", Rule{Family: family, TargetMax: Limit{Set: true, Value: 1}}, []Link{live("a", "relation", "a", "c"), live("b", "special", "b", "c")}, false},
		{"maximum zero", Rule{Family: family, SourceMax: Limit{Set: true}}, []Link{live("a", "special", "a", "b")}, false},
		{"empty maximum zero", Rule{Family: family, SourceMax: Limit{Set: true}}, nil, true},
		{"unrelated family excluded", Rule{Family: family, SourceMax: Limit{Set: true, Value: 1}}, []Link{live("a", "relation", "a", "b"), live("b", "other", "a", "c")}, true},
		{"narrow family preserves parent cycle", Rule{Family: reference("special"), Acyclic: true}, []Link{live("a", "relation", "a", "b"), live("b", "relation", "b", "a")}, true},
		{"refinement closes family cycle", Rule{Family: family, Acyclic: true}, []Link{live("a", "relation", "a", "b"), live("b", "special", "b", "a")}, false},
		{"parallel acyclic edges", Rule{Family: family, Acyclic: true}, []Link{live("a", "relation", "a", "b"), live("b", "special", "a", "b")}, true},
		{"cycle beyond independent root", Rule{Family: family, Acyclic: true}, []Link{live("a", "relation", "root", "leaf"), live("b", "relation", "b", "c"), live("c", "special", "c", "b")}, false},
		{"unique across refinements", Rule{Family: family, Unique: true}, []Link{live("a", "relation", "a", "b"), live("b", "special", "a", "b")}, false},
		{"reverse pair distinct", Rule{Family: family, Unique: true}, []Link{live("a", "relation", "a", "b"), live("b", "special", "b", "a")}, true},
	} {
		t.Run(tc.name, func(t *testing.T) { check(t, []Rule{tc.rule}, tc.links, tc.valid) })
	}
}

func TestIdentityIndependentOfEndpointRevisions(t *testing.T) {
	self := live("self", "special", "same", "same")
	self.Source.Revision, self.Target.Revision = "r1", "r2"
	check(t, []Rule{{Family: reference("relation"), ForbidIdentitySelf: true}}, []Link{self}, false)
	check(t, []Rule{{Family: reference("relation"), Acyclic: true}}, []Link{self}, false)
	first, second := live("a", "relation", "a", "b"), live("b", "special", "a", "c")
	first.Source.Revision, second.Source.Revision = "r1", "r2"
	check(t, []Rule{{Family: reference("relation"), SourceMax: Limit{Set: true, Value: 1}}}, []Link{first, second}, false)
	// Same local path in different Scopes is a different Bead identity.
	self.Target.Identity.Scope = "https://other.example/"
	check(t, []Rule{{Family: reference("relation"), ForbidIdentitySelf: true}}, []Link{self}, true)
	check(t, []Rule{{Family: reference("relation"), SameScope: true}}, []Link{self}, false)
}

func TestFloatingEndpointsAndRuleIntersection(t *testing.T) {
	link := live("a", "special", "a", "b")
	for _, side := range []string{"source", "target"} {
		pinned := link
		if side == "source" {
			pinned.Source.Revision = "r1"
		} else {
			pinned.Target.Revision = "r1"
		}
		check(t, []Rule{{Family: reference("relation"), FloatingSource: true, FloatingTarget: true}}, []Link{pinned}, false)
	}
	check(t, []Rule{{Family: reference("relation"), FloatingSource: true, FloatingTarget: true}}, []Link{link}, true)
	check(t, []Rule{
		{Family: reference("relation"), SourceMax: Limit{Set: true, Value: 1}},
		{Family: reference("special")},
	}, []Link{link, live("b", "special", "a", "c")}, false)
}

func TestOpaqueResourcesInCompletePopulation(t *testing.T) {
	external := live("external", "other", "a", "b")
	external.Target = Endpoint{ResourceURI: "urn:example:document", Revision: "edition-1"}
	local := live("local", "special", "a", "b")
	check(t, []Rule{{Family: reference("relation"), SameScope: true}}, []Link{external, local}, true)
	check(t, []Rule{{Family: reference("other"), SameScope: true}}, []Link{external}, false)
	for _, uri := range []string{"relative", scope + "beads/a"} {
		bad := external
		bad.Target.ResourceURI = uri
		check(t, nil, []Link{bad}, false)
	}
	bad := external
	bad.Target.Identity = local.Target.Identity
	check(t, nil, []Link{bad}, false)
	// An opaque reference and resolved cross-Scope Bead with the same URI
	// denote the same identity for family maxima; resolution cannot split it.
	first := live("first", "relation", "a", "b")
	first.Target = Endpoint{ResourceURI: "https://other.example/beads/x"}
	second := live("second", "special", "c", "x")
	second.Target.Identity.Scope = "https://other.example/"
	check(t, []Rule{{Family: reference("relation"), TargetMax: Limit{Set: true, Value: 1}}}, []Link{first, second}, false)
}

func TestGraphValidationAdmissionAndIsolation(t *testing.T) {
	c := catalogForTest(t)
	for _, ref := range []typecatalog.Reference{reference("missing"), reference("bead"), {URI: reference("relation").URI, Revision: "old"}} {
		if _, err := New(c, []Rule{{Family: ref}}); !errors.Is(err, graph.ErrValidation) {
			t.Fatalf("invalid rule family admitted: %v", ref)
		}
	}
	if _, err := New(nil, nil); !errors.Is(err, graph.ErrValidation) {
		t.Fatal("missing catalog admitted")
	}
	var empty *Validator
	if err := empty.Validate(nil); !errors.Is(err, graph.ErrValidation) {
		t.Fatal("missing validator admitted")
	}
	original := live("a", "special", "a", "b")
	for _, mutate := range []func(*Link){
		func(l *Link) { l.Identity.Path = "beads/wrong" },
		func(l *Link) { l.Source.Identity.Path = "links/wrong" },
		func(l *Link) { l.Target.Identity.Scope = "https://graph.example" },
		func(l *Link) { l.Type = reference("missing") },
		func(l *Link) { l.Type = reference("bead") },
		func(l *Link) { l.Type.Revision = "stale" },
		func(l *Link) { l.Source.Revision = string([]byte{0xff}) },
	} {
		bad := original
		mutate(&bad)
		check(t, nil, []Link{bad}, false)
	}
	check(t, nil, []Link{original, original}, false)
	rules := []Rule{{Family: reference("relation"), SourceMax: Limit{Set: true, Value: 1}}}
	v, err := New(c, rules)
	if err != nil {
		t.Fatal(err)
	}
	rules[0].SourceMax.Set = false
	links := []Link{original, live("b", "special", "a", "c")}
	before := append([]Link(nil), links...)
	if err := v.Validate(links); !errors.Is(err, graph.ErrValidation) {
		t.Fatal("caller mutation changed retained rules")
	}
	if !reflect.DeepEqual(before, links) {
		t.Fatal("refusal mutated input population")
	}
	// Overlaying a deletion removes the competing live Link; historical copies
	// are never passed back into the next prospective population.
	if err := v.Validate(links[:1]); err != nil {
		t.Fatal(err)
	}
}
