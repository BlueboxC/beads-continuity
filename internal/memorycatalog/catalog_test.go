package memorycatalog_test

import (
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"testing"

	graph "github.com/steveyegge/beads/graphops"
	m "github.com/steveyegge/beads/internal/memorycatalog"
	"github.com/steveyegge/beads/internal/typecatalog"
)

// Literal expectations come from the pinned publication, not production helpers.
var beads = []m.Symbol{m.Memory, m.Evaluation, m.Constraint, m.Policy, m.Decision, m.Procedure, m.Requirement, m.Scenario, m.Specification}
var links = []m.Symbol{m.References, m.SupersededBy, m.Requires, m.Defines, m.SubjectTo, m.Conformance, m.Addresses, m.Satisfies, m.Violates}

func inputs() m.Inputs {
	i := m.Inputs{Bindings: map[m.Symbol]m.Binding{}, Issue: typecatalog.Reference{URI: "https://native.example/types/issue", Revision: "native-issue-contract"}}
	for index, symbol := range append(append([]m.Symbol(nil), beads...), links...) {
		i.Bindings[symbol] = m.Binding{
			Type:       typecatalog.Reference{URI: fmt.Sprintf("https://application.example/contracts/custom-%d", index), Revision: "caller-selected-revision"},
			SchemaBase: fmt.Sprintf("https://schemas.example/caller-base-%d", index),
		}
	}
	return i
}

func project(t *testing.T, input m.Inputs) *m.Projection {
	t.Helper()
	p, err := m.New(input)
	if err != nil {
		t.Fatal(err)
	}
	return p
}

func definition(t *testing.T, p *m.Projection, symbol m.Symbol) m.Definition {
	t.Helper()
	d, ok := p.Definition(symbol)
	if !ok {
		t.Fatalf("missing %s", symbol)
	}
	return d
}

func property(t *testing.T, d m.Definition, name string) m.Property {
	t.Helper()
	for _, p := range d.Properties {
		if p.Name == name {
			return p
		}
	}
	t.Fatalf("missing %s.%s", d.Symbol, name)
	return m.Property{}
}

func TestSelectedSourceAndInventory(t *testing.T) {
	p := project(t, inputs())
	want := m.Source{Repository: "gastownhall/bdp", Commit: "afec9117914bd1c1fe0027c2067e4d89677e40a3", Path: "specs/types/memory/memory.md", SHA256: "1f97f8a80f725b2a44a0c715b4ccf09918f02af2abb9885797c3395255dc9e34", Bytes: 106334}
	if p.Source() != want || m.SelectedSource() != want {
		t.Fatal("publication pin/digest changed")
	}
	var gotBeads, gotLinks []m.Symbol
	for _, d := range p.Definitions() {
		switch d.Kind {
		case graph.KindBead:
			gotBeads = append(gotBeads, d.Symbol)
		case graph.KindLink:
			gotLinks = append(gotLinks, d.Symbol)
		default:
			t.Fatalf("unexpected kind %s", d.Kind)
		}
	}
	if !reflect.DeepEqual(gotBeads, beads) || !reflect.DeepEqual(gotLinks, links) {
		t.Fatalf("inventory %v / %v", gotBeads, gotLinks)
	}
	if len(gotBeads) != 9 || len(gotLinks) != 9 || len(p.Definitions()) != 18 {
		t.Fatal("selected source must bind exactly nine Bead and nine Link definitions")
	}
	for _, absent := range []m.Symbol{"type", "beadtype", "linktype", "conformsto", "refines", "implementation", "proposal", "work-product", "observation", "lesson"} {
		if _, ok := p.Definition(absent); ok {
			t.Fatalf("unselected %s added", absent)
		}
	}
}

func TestBindingsRequireExplicitDistinctInputs(t *testing.T) {
	for _, tc := range []struct {
		name   string
		change func(*m.Inputs)
	}{
		{"missing", func(i *m.Inputs) { delete(i.Bindings, m.Scenario) }},
		{"extra", func(i *m.Inputs) { i.Bindings["implementation"] = i.Bindings[m.Memory] }},
		{"unknown instead", func(i *m.Inputs) { i.Bindings["unknown"] = i.Bindings[m.Memory]; delete(i.Bindings, m.Memory) }},
		{"duplicate identity", func(i *m.Inputs) {
			b := i.Bindings[m.Policy]
			b.Type = i.Bindings[m.Memory].Type
			i.Bindings[m.Policy] = b
		}},
		{"Issue collision", func(i *m.Inputs) { i.Issue = i.Bindings[m.Memory].Type }},
		{"missing Issue", func(i *m.Inputs) { i.Issue = typecatalog.Reference{} }},
		{"invalid revision", func(i *m.Inputs) { b := i.Bindings[m.Memory]; b.Type.Revision = "\xff"; i.Bindings[m.Memory] = b }},
		{"missing schema base", func(i *m.Inputs) { b := i.Bindings[m.Memory]; b.SchemaBase = ""; i.Bindings[m.Memory] = b }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			i := inputs()
			tc.change(&i)
			if _, err := m.New(i); !errors.Is(err, graph.ErrValidation) {
				t.Fatalf("want validation refusal, got %v", err)
			}
		})
	}
	i := inputs()
	p := project(t, i)
	for _, d := range p.Definitions() {
		if d.Binding != i.Bindings[d.Symbol] {
			t.Fatal("caller identity/revision/base replaced")
		}
	}
	// URI-only references remain URI-only, never an invented latest revision.
	// Shared retrieval bases are bindings here; closure admission is later work.
	b := i.Bindings[m.Memory]
	b.Type.Revision = ""
	i.Bindings[m.Memory] = b
	i.Issue.Revision = ""
	b = i.Bindings[m.Policy]
	b.SchemaBase = i.Bindings[m.Memory].SchemaBase
	i.Bindings[m.Policy] = b
	p = project(t, i)
	if definition(t, p, m.Memory).Binding.Type.Revision != "" || p.ExistingIssue().Type.Revision != "" || definition(t, p, m.Policy).Binding.SchemaBase != i.Bindings[m.Memory].SchemaBase {
		t.Fatal("caller URI-only/shared-base bindings were changed")
	}
}

func TestSelectedParentsAndProperties(t *testing.T) {
	i := inputs()
	p := project(t, i)
	parents := map[m.Symbol]m.Symbol{m.Evaluation: m.Memory, m.Constraint: m.Memory, m.Policy: m.Constraint, m.Decision: m.Constraint, m.Procedure: m.Constraint, m.Requirement: m.Constraint, m.Scenario: m.Constraint, m.Specification: m.Constraint, m.SupersededBy: m.References, m.Requires: m.References, m.Defines: m.Requires, m.SubjectTo: m.References, m.Conformance: m.References, m.Addresses: m.Conformance, m.Satisfies: m.Conformance, m.Violates: m.Conformance}
	for _, d := range p.Definitions() {
		ancestor, hasParent := parents[d.Symbol]
		if !hasParent {
			if len(d.Parents) != 0 {
				t.Fatalf("unexpected parent of %s", d.Symbol)
			}
		} else if len(d.Parents) != 1 || d.Parents[0] != i.Bindings[ancestor].Type {
			t.Fatalf("wrong direct parent of %s", d.Symbol)
		}
		seen := map[m.Symbol]bool{}
		for _, symbol := range d.Ancestors {
			if seen[symbol] {
				t.Fatal("duplicate ancestor")
			}
			seen[symbol] = true
		}
		if !seen[d.Symbol] {
			t.Fatal("self absent from closure")
		}
		for _, prop := range d.Properties {
			if prop.Shape == "reserved-undefined" {
				continue
			}
			if !json.Valid(prop.ShapeFragment) {
				t.Fatalf("invalid fragment %s.%s", d.Symbol, prop.Name)
			}
		}
	}
	for _, symbol := range beads {
		d := definition(t, p, symbol)
		title, body, status := property(t, d, "title"), property(t, d, "body"), property(t, d, "status")
		if !title.Required || !title.Nonblank || title.DeclaredBy != m.Memory {
			t.Fatal("Memory title contract lost")
		}
		if !body.Required || !body.Markdown || body.Nonblank || body.DeclaredBy != m.Memory {
			t.Fatal("empty Memory body must remain legal")
		}
		if status.Required || !status.Nonblank || !status.OpenLabel {
			t.Fatal("optional open status contract lost")
		}
		if symbol != m.Memory && symbol != m.Evaluation {
			reserved := property(t, d, "applicability")
			if reserved.DeclaredBy != m.Constraint || reserved.Required || reserved.Shape != "reserved-undefined" || reserved.ShapeFragment != nil || reserved.ReservedReason == "" {
				t.Fatal("applicability became a concrete/executable schema")
			}
		}
	}
	for _, symbol := range links {
		d := definition(t, p, symbol)
		body := property(t, d, "body")
		if body.Required || !body.Nonblank || !body.Markdown || body.DeclaredBy != m.References || len(body.AdditionalChecks) == 0 {
			t.Fatal("Link body boundary differs from selected optional nonblank contract")
		}
	}
	for _, symbol := range []m.Symbol{m.Policy, m.Decision} {
		authority := property(t, definition(t, p, symbol), "authority")
		if authority.Shape != "reference" || authority.Required || len(authority.AdditionalChecks) == 0 {
			t.Fatal("authority shape lacks shared-reference requirement")
		}
	}
	version := property(t, definition(t, p, m.Specification), "version")
	if version.Required || !version.Nonblank || !version.OpenLabel || version.DeclaredBy != m.Specification {
		t.Fatal("Specification version is not the open optional label")
	}
	for _, symbol := range []m.Symbol{m.Conformance, m.Addresses, m.Satisfies, m.Violates} {
		got := property(t, definition(t, p, symbol), "evidence")
		if got.DeclaredBy != m.Conformance || !got.Nonblank || got.Required {
			t.Fatal("claim evidence must be optional nonblank text declared by Conformance")
		}
		if symbol != m.Conformance && len(definition(t, p, symbol).LocalProperties) != 0 {
			t.Fatal("peer claim Type redeclares inherited evidence")
		}
	}
}

func TestEvaluationSubjectsAndLocalAbstractness(t *testing.T) {
	p := project(t, inputs())
	evaluation := definition(t, p, m.Evaluation)
	subjects := property(t, evaluation, "subjects")
	if !subjects.Required || subjects.DeclaredBy != m.Evaluation || subjects.Shape != "exact-bead-references" {
		t.Fatal("Evaluation must declare a required exact subject group")
	}
	var shape struct {
		Type     string `json:"type"`
		MinItems int    `json:"minItems"`
		Items    struct {
			Type                 string   `json:"type"`
			Required             []string `json:"required"`
			AdditionalProperties bool     `json:"additionalProperties"`
			Properties           map[string]struct {
				Type      string `json:"type"`
				MinLength int    `json:"minLength"`
			} `json:"properties"`
		} `json:"items"`
	}
	if err := json.Unmarshal(subjects.ShapeFragment, &shape); err != nil {
		t.Fatal(err)
	}
	if shape.Type != "array" || shape.MinItems != 1 || shape.Items.Type != "object" || shape.Items.AdditionalProperties || !reflect.DeepEqual(shape.Items.Required, []string{"uri", "revision"}) || len(shape.Items.Properties) != 2 {
		t.Fatal("empty groups, floating strings or unknown reference members admitted by shape")
	}
	for _, name := range []string{"uri", "revision"} {
		if field := shape.Items.Properties[name]; field.Type != "string" || field.MinLength != 1 {
			t.Fatalf("%s must be a nonempty string", name)
		}
	}
	if !reflect.DeepEqual(subjects.AdditionalChecks, []string{"shared-reference-uri-syntax-and-semantics", "exact-retained-bead-revision", "same-scope-subject-bindings", "collective-full-subject-group"}) {
		t.Fatal("shape alone falsely discharges runtime subject binding")
	}
	for _, d := range p.Definitions() {
		if d.Abstract != (d.Symbol == m.Conformance) {
			t.Fatalf("abstractness must be local only: %s", d.Symbol)
		}
	}
	for _, symbol := range []m.Symbol{m.Memory, m.Constraint} {
		for _, prop := range definition(t, p, symbol).Properties {
			if prop.Name == "subjects" {
				t.Fatal("subject group leaked into unrelated Memory family")
			}
		}
	}
}

func TestCollectiveClaimAndApplicabilityMeanings(t *testing.T) {
	p := project(t, inputs())
	contains := func(symbol m.Symbol, rule string) bool {
		for _, meaning := range definition(t, p, symbol).Meanings {
			if meaning.Rule == rule {
				return true
			}
		}
		return false
	}
	for _, tc := range []struct {
		symbol m.Symbol
		rule   string
	}{
		{m.SubjectTo, "subject-to-nontransitive-obligations-via-target-requires-defines"},
		{m.Evaluation, "all-owned-claims-concern-full-group-no-subset-selector"},
		{m.Satisfies, "compatible-claim-composition-through-evaluation-subject-bindings"},
		{m.Satisfies, "singleton-intermediate-constraint-only-no-collective-distribution"},
		{m.Satisfies, "no-native-satisfies-link-transitivity"},
	} {
		if !contains(tc.symbol, tc.rule) {
			t.Fatalf("missing %s meaning %s", tc.symbol, tc.rule)
		}
	}
	if contains(m.SubjectTo, "subject-to-transitive-and-target-requires-composition") || contains(m.Satisfies, "uninterrupted-compatible-satisfies-chain-only") {
		t.Fatal("superseded direct subject/claim transitivity retained")
	}
}

func TestFamilyOwnershipAndGraphSidecar(t *testing.T) {
	i := inputs()
	p := project(t, i)
	for _, symbol := range beads {
		d := definition(t, p, symbol)
		want := 3
		if symbol == m.Memory {
			want = 2
		}
		if len(d.Ownership) != want {
			t.Fatalf("%s ownership = %v", symbol, d.Ownership)
		}
		seen := map[m.Symbol]bool{}
		for _, owner := range d.Ownership {
			if seen[owner.Family] || owner.Family == m.References || owner.Type != i.Bindings[owner.Family].Type {
				t.Fatal("duplicate, broad or rebound ownership")
			}
			seen[owner.Family] = true
		}
		for _, family := range []m.Symbol{m.SubjectTo, m.SupersededBy} {
			if !seen[family] {
				t.Fatal("missing Memory ownership")
			}
		}
		if (symbol == m.Evaluation) != seen[m.Conformance] {
			t.Fatal("only Evaluation owns Conformance claims")
		}
		if symbol != m.Memory && symbol != m.Evaluation && !seen[m.Requires] {
			t.Fatal("missing Constraint Requires ownership")
		}
	}
	for _, symbol := range links {
		d := definition(t, p, symbol)
		if len(d.Ownership) != 0 || len(d.GraphRules) == 0 {
			t.Fatal("Link owns something or graph sidecar missing")
		}
		base := d.GraphRules[0]
		if base.DeclaredBy != m.References || !base.ForbidIdentitySelf || !base.SameScope {
			t.Fatal("inherited no-self/same-Scope rule lost")
		}
		for _, rule := range d.GraphRules {
			if rule.Unique {
				t.Fatal("invented ordered-pair uniqueness")
			}
			if rule.Population != "live-links" {
				t.Fatal("graph participation must not count retained history")
			}
		}
	}
	sup := definition(t, p, m.SupersededBy).LocalGraphRule
	if sup.SourceMax != (m.Limit{Present: true, Value: 1}) || sup.TargetMax.Present || !sup.Acyclic || !sup.FloatingSource || !sup.FloatingTarget {
		t.Fatal("supersession rules lost")
	}
	defines := definition(t, p, m.Defines).LocalGraphRule
	if defines.TargetMax != (m.Limit{Present: true, Value: 1}) || defines.SourceMax.Present || !defines.Acyclic {
		t.Fatal("Defines tree rules lost")
	}
	for _, symbol := range []m.Symbol{m.Requires, m.SubjectTo, m.Conformance, m.Addresses, m.Satisfies, m.Violates} {
		for _, rule := range definition(t, p, symbol).GraphRules {
			if rule.Acyclic || rule.SourceMax.Present || rule.TargetMax.Present {
				t.Fatalf("%s accidentally restricted by narrower family", symbol)
			}
		}
	}
	issue := p.ExistingIssue()
	if issue.Type != i.Issue || !issue.NativePayloadValidationRequired || issue.IntegrationState != "not-integrated" {
		t.Fatal("nominal Issue facts replaced native validation or claimed integration")
	}
	var owned []m.Symbol
	for _, owner := range issue.OwnershipRequired {
		owned = append(owned, owner.Family)
	}
	if !reflect.DeepEqual(owned, []m.Symbol{m.SubjectTo}) {
		t.Fatal("Issue source integration requirements missing")
	}
	var otherOwned []m.Symbol
	for _, owner := range p.OtherSourceOwnershipRequirements() {
		otherOwned = append(otherOwned, owner.Family)
	}
	if !reflect.DeepEqual(otherOwned, owned) {
		t.Fatal("AnyBead source ownership seam differs from Issue seam")
	}
}

// This test exercises ONLY the existing pure endpoint matcher with projected
// pairs. Every properties schema below is an explicitly restrictive synthetic
// fixture. None is a replacement or claimed projection of native Issue payload
// validation or a complete installable Memory properties contract.
func endpointFixture(t *testing.T, p *m.Projection, i m.Inputs) *typecatalog.Catalog {
	t.Helper()
	var defs []typecatalog.Definition
	for _, d := range p.Definitions() {
		fixture := typecatalog.Definition{Identity: d.Binding.Type, Name: string(d.Symbol), Kind: d.Kind, Public: true, Parents: d.Parents, SchemaBase: d.Binding.SchemaBase, PropertiesSchema: json.RawMessage(`{"type":"object","required":["fixture-proof"]}`), EndpointPairs: d.LocalEndpointPairs, Abstract: d.Abstract}
		for _, owner := range d.LocalOwnership {
			fixture.OwnsOutgoing = append(fixture.OwnsOutgoing, typecatalog.Selector{AnyOf: []typecatalog.Reference{owner.Type}})
		}
		defs = append(defs, fixture)
	}
	for index, ref := range []typecatalog.Reference{i.Issue, {URI: "https://native.example/types/unrelated", Revision: "native-unrelated"}} {
		defs = append(defs, typecatalog.Definition{Identity: ref, Name: fmt.Sprintf("synthetic-native-%d", index), Kind: graph.KindBead, SchemaBase: fmt.Sprintf("https://fixtures.example/native-%d", index), PropertiesSchema: json.RawMessage(`{"type":"object","required":["native-fixture-proof"]}`)})
	}
	c, err := typecatalog.New(defs)
	if err != nil {
		t.Fatal(err)
	}
	return c
}

func TestCorrelatedPairsAndAnyBeadProjection(t *testing.T) {
	i := inputs()
	p := project(t, i)
	refs := definition(t, p, m.References)
	if len(refs.LocalEndpointPairs) != 4 {
		t.Fatal("References must retain four alternatives")
	}
	fourth := refs.LocalEndpointPairs[3]
	if !fourth.Source.Types.Any || fourth.Source.ResourceKind != "bead" || fourth.Target.Types.Any || !reflect.DeepEqual(fourth.Target.Types.AnyOf, []typecatalog.Reference{i.Bindings[m.Constraint].Type}) {
		t.Fatal("AnyBead→Constraint alternative lost")
	}
	if len(definition(t, p, m.Satisfies).EndpointRules) != 3 {
		t.Fatal("Satisfies must retain References, Conformance and local correlated rules")
	}
	c := endpointFixture(t, p, i)
	other := typecatalog.Reference{URI: "https://native.example/types/unrelated", Revision: "native-unrelated"}
	facts := func(ref typecatalog.Reference) typecatalog.EndpointFacts {
		return typecatalog.EndpointFacts{ResourceKind: "bead", Scope: "same", Type: ref}
	}
	for _, tc := range []struct {
		name           string
		link           m.Symbol
		source, target typecatalog.Reference
		valid          bool
	}{
		{"Memory pair", m.References, i.Bindings[m.Memory].Type, i.Bindings[m.Memory].Type, true},
		{"Memory Issue", m.References, i.Bindings[m.Memory].Type, i.Issue, true},
		{"Issue Memory", m.References, i.Issue, i.Bindings[m.Memory].Type, true},
		{"invalid Cartesian Issue Issue", m.References, i.Issue, i.Issue, false},
		{"unrelated source Constraint", m.References, other, i.Bindings[m.Policy].Type, true},
		{"unrelated source Memory", m.References, other, i.Bindings[m.Memory].Type, false},
		{"unrelated subject", m.SubjectTo, other, i.Bindings[m.Policy].Type, true},
		{"Issue subject", m.SubjectTo, i.Issue, i.Bindings[m.Policy].Type, true},
		// Abstractness is separate from nominal endpoint matching.
		{"evaluation neutral endpoints", m.Conformance, i.Bindings[m.Evaluation].Type, i.Bindings[m.Policy].Type, true},
		{"evaluation assessment", m.Satisfies, i.Bindings[m.Evaluation].Type, i.Bindings[m.Requirement].Type, true},
		{"unrelated assessment refused", m.Satisfies, other, i.Bindings[m.Requirement].Type, false},
		{"subject direct breach refused", m.Violates, i.Issue, i.Bindings[m.Policy].Type, false},
		{"Requires constraint", m.Requires, i.Bindings[m.Policy].Type, i.Bindings[m.Procedure].Type, true},
		{"Requires plain Memory", m.Requires, i.Bindings[m.Memory].Type, i.Bindings[m.Policy].Type, false},
		{"Defines unrelated", m.Defines, other, i.Bindings[m.Policy].Type, false},
		{"replacement across subtypes", m.SupersededBy, i.Bindings[m.Policy].Type, i.Bindings[m.Memory].Type, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			err := c.ValidateEndpoints(i.Bindings[tc.link].Type, facts(tc.source), facts(tc.target))
			if (err == nil) != tc.valid {
				t.Fatalf("eligibility: %v", err)
			}
		})
	}
	cross := facts(other)
	cross.Scope = "cross"
	if err := c.ValidateEndpoints(i.Bindings[m.SubjectTo].Type, cross, facts(i.Bindings[m.Policy].Type)); err == nil {
		t.Fatal("cross-Scope source admitted")
	}
	if err := c.ValidateEndpoints(i.Bindings[m.SubjectTo].Type, typecatalog.EndpointFacts{ResourceKind: "resource"}, facts(i.Bindings[m.Policy].Type)); err == nil {
		t.Fatal("AnyBead widened to opaque Resource")
	}
	owned, err := c.Owns(i.Bindings[m.Policy].Type, i.Bindings[m.Defines].Type)
	if err != nil || !owned {
		t.Fatal("Requires family did not include Defines")
	}
	owned, err = c.Owns(i.Bindings[m.Evaluation].Type, i.Bindings[m.Satisfies].Type)
	if err != nil || !owned {
		t.Fatal("Conformance family did not include Satisfies")
	}
	for _, symbol := range []m.Symbol{m.Addresses, m.Satisfies, m.Violates} {
		if !c.Conforms(i.Bindings[symbol].Type, i.Bindings[m.Conformance].Type) {
			t.Fatal("peer does not refine Conformance")
		}
		for _, peer := range []m.Symbol{m.Addresses, m.Satisfies, m.Violates} {
			if peer != symbol && c.Conforms(i.Bindings[symbol].Type, i.Bindings[peer].Type) {
				t.Fatal("peer claim Type unexpectedly refines another peer")
			}
		}
		for _, source := range []m.Symbol{m.Memory, m.Constraint, m.Policy} {
			owns, err := c.Owns(i.Bindings[source].Type, i.Bindings[symbol].Type)
			if err != nil || owns {
				t.Fatal("assessed subject acquired Conformance ownership")
			}
		}
		foundParallel := false
		for _, meaning := range definition(t, p, symbol).Meanings {
			if meaning.Rule == "independent-parallel-evidence-claims-preserved" {
				foundParallel = true
				if meaning.DeclaredBy != m.Conformance {
					t.Fatal("parallel-claim contract not inherited from Conformance")
				}
			}
			if symbol != m.Addresses && meaning.Rule == "contribution-unspecified-completeness" {
				t.Fatal("peer claim inherited Addresses contribution meaning")
			}
		}
		if !foundParallel {
			t.Fatal("common independent parallel-claim meaning missing")
		}
	}
	owned, err = c.Owns(i.Bindings[m.Policy].Type, i.Bindings[m.References].Type)
	if err != nil || owned {
		t.Fatal("direct References became owned")
	}
}

func TestProjectionDoesNotClaimDeferredExecution(t *testing.T) {
	p := project(t, inputs())
	support := p.SupportMap()
	if support["applicability"].State != "reserved-undefined" || !support["applicability"].SpecificationDeferred {
		t.Fatal("applicability has a fabricated executable contract")
	}
	if support["direct-conformance-authoring"].State != "abstract-definition" || support["direct-conformance-authoring"].SpecificationDeferred {
		t.Fatal("Conformance abstractness missing")
	}
	for _, feature := range []string{"graph-rules", "ownership", "native-domain-validation", "operation-admission"} {
		if support[feature].State == "" || support[feature].State == "supported" {
			t.Fatalf("false runtime support for %s", feature)
		}
	}
	if support["graph-rules"].SpecificationDeferred {
		t.Fatal("selected graph enforcement falsely waived as spec deferral")
	}
	if support["derived-claims"].SpecificationDeferred {
		t.Fatal("selected claim meanings falsely deferred")
	}
}

func TestProjectionInputAndReturnedDataIsolation(t *testing.T) {
	i := inputs()
	p := project(t, i)
	old := i.Bindings[m.Memory]
	i.Bindings[m.Memory] = m.Binding{}
	i.Issue = typecatalog.Reference{}
	d := definition(t, p, m.Policy)
	d.Parents[0] = typecatalog.Reference{}
	d.Ancestors[0] = "changed"
	d.Properties[0].ShapeFragment[0] = 'x'
	d.Properties[0].AdditionalChecks[0] = "changed"
	d.Ownership[0].Family = m.References
	d.LocalProperties[0].AdditionalChecks[0] = "changed"
	l := definition(t, p, m.Satisfies)
	l.EndpointRules[0].Pairs[0].Source.Types.AnyOf[0] = typecatalog.Reference{}
	l.LocalEndpointPairs[0].Target.Types.AnyOf[0] = typecatalog.Reference{}
	l.GraphRules[0].ForbidIdentitySelf = false
	l.Meanings[0].Rule = "changed"
	all := p.Definitions()
	all[0].Binding = m.Binding{}
	issue := p.ExistingIssue()
	issue.OwnershipRequired[0].Family = m.References
	requirements := p.OtherSourceOwnershipRequirements()
	requirements[0].Family = m.References
	support := p.SupportMap()
	support["applicability"] = m.Support{State: "supported"}
	if definition(t, p, m.Memory).Binding != old || definition(t, p, m.Policy).Properties[0].AdditionalChecks[0] != "nonblank-text" || !json.Valid(definition(t, p, m.Policy).Properties[0].ShapeFragment) {
		t.Fatal("caller/return mutation leaked")
	}
	fresh := definition(t, p, m.Satisfies)
	if fresh.EndpointRules[0].Pairs[0].Source.Types.AnyOf[0].URI == "" || fresh.LocalEndpointPairs[0].Target.Types.AnyOf[0].URI == "" || !fresh.GraphRules[0].ForbidIdentitySelf || fresh.Meanings[0].Rule == "changed" {
		t.Fatal("nested pair/rule mutation leaked")
	}
	if p.ExistingIssue().OwnershipRequired[0].Family != m.SubjectTo || p.SupportMap()["applicability"].State != "reserved-undefined" {
		t.Fatal("native requirement/support mutation leaked")
	}
}
