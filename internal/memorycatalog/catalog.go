package memorycatalog

import (
	"encoding/json"
	"fmt"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/typecatalog"
)

// Binding supplies application-chosen identity/revision and explicit schema
// retrieval base. The publication assigns none of these application values.
type Binding struct {
	Type       typecatalog.Reference
	SchemaBase string
}

// Inputs contains exactly the eighteen fixed bindings and an established native
// Issue nominal reference. Issue is not supplied a substitute properties schema.
// The caller remains responsible for establishing native identity/category facts.
type Inputs struct {
	Bindings map[Symbol]Binding
	Issue    typecatalog.Reference
}

// Property is a selected declaration. ShapeFragment is only a value-shape
// fragment, never a complete effective properties schema. AdditionalChecks are
// requirements NOT discharged by that fragment. No whole-object extension or
// additionalProperties policy is selected here.
type Property struct {
	Name             string
	DeclaredBy       Symbol
	Required         bool
	Shape            string // text, reference, exact-bead-references, or reserved-undefined
	Markdown         bool
	Nonblank         bool
	OpenLabel        bool
	ShapeFragment    json.RawMessage
	AdditionalChecks []string
	ReservedReason   string
}

// Ownership is a positive family declaration. A specialized Link matches via
// conformance, not by enumerating its name. It is not a storage revision effect.
type Ownership struct {
	DeclaredBy Symbol
	Family     Symbol
	Type       typecatalog.Reference
}

// Limit distinguishes no local maximum from a selected maximum (including zero).
type Limit struct {
	Present bool
	Value   uint64
}

// GraphRule is an explicit selected family restriction, not an evaluator.
// False/absent adds no local restriction and never disables an inherited one.
// Maxima count live Links by endpoint Bead identity across the indicated
// family, never per endpoint-pair alternative or per revision selector.
type GraphRule struct {
	DeclaredBy         Symbol
	Family             typecatalog.Reference
	Population         string // live-links, never retained historical copies
	SourceMax          Limit
	TargetMax          Limit
	Acyclic            bool
	Unique             bool // false adds no ordered-pair uniqueness promise
	ForbidIdentitySelf bool
	SameScope          bool
	FloatingSource     bool
	FloatingTarget     bool
}

// EndpointRule is a union of correlated pairs from one declaring definition.
// Every inherited rule must independently pass; rules are never flattened into
// independent source and target unions.
type EndpointRule struct {
	DeclaredBy Symbol
	Pairs      []typecatalog.EndpointPair
}

// Meaning retains selected behavior beyond shape or graph restrictions. These
// declarations neither evaluate evidence nor materialize inferred Links.
type Meaning struct {
	DeclaredBy Symbol
	Rule       string
}

// Definition holds local and inherited declarations in source order. Inherited
// declarations keep their origin. This is not graphops.TypeDescriptor wire JSON,
// typecatalog.Definition, or a complete installable schema/operation contract.
type Definition struct {
	Symbol             Symbol
	Kind               graph.ResourceKind
	Abstract           bool // local to this definition; never inherited
	Binding            Binding
	Parents            []typecatalog.Reference
	Ancestors          []Symbol // self included exactly once
	LocalProperties    []Property
	Properties         []Property
	LocalOwnership     []Ownership
	Ownership          []Ownership
	LocalEndpointPairs []typecatalog.EndpointPair
	EndpointRules      []EndpointRule
	LocalGraphRule     GraphRule // zero for Beads
	GraphRules         []GraphRule
	Meanings           []Meaning
}

// ExistingDomainRequirement preserves the native domain boundary. It contains
// nominal facts and selected future ownership requirements only. There is no
// permissive Issue schema and no replacement for native Issue validation.
type ExistingDomainRequirement struct {
	Role                            string
	Type                            typecatalog.Reference
	NativePayloadValidationRequired bool
	OwnershipRequired               []Ownership
	IntegrationState                string
}

// Support records projection support independently of selected product promises.
// "declared" never means installed, evaluated, enforced or qualified.
type Support struct {
	State                 string
	SpecificationDeferred bool
	Detail                string
}

// Projection is immutable after construction. Accessors return independent data.
type Projection struct {
	definitions map[Symbol]Definition
	issue       ExistingDomainRequirement
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: memory catalog projection: %s", graph.ErrValidation, fmt.Sprintf(format, args...))
}

// New projects the fixed design using explicit caller bindings. It reads no
// files/network/storage, compiles no schema, chooses no authority/default IDs,
// and does not decide runtime handling of reserved applicability values.
func New(input Inputs) (*Projection, error) {
	all := symbols()
	if len(input.Bindings) != len(all) {
		return nil, invalid("need exactly eighteen selected Type bindings")
	}
	if err := validReference(input.Issue); err != nil {
		return nil, err
	}
	ids := map[string]bool{input.Issue.URI: true}
	for _, symbol := range all {
		binding, ok := input.Bindings[symbol]
		if !ok {
			return nil, invalid("missing binding for %s", symbol)
		}
		if err := validReference(binding.Type); err != nil {
			return nil, err
		}
		if ids[binding.Type.URI] {
			return nil, invalid("duplicate Type identity %s", binding.Type.URI)
		}
		ids[binding.Type.URI] = true
		if err := graph.ValidateTypeURL(binding.SchemaBase); err != nil {
			return nil, err
		}
	}
	p := &Projection{definitions: make(map[Symbol]Definition, len(all))}
	for _, symbol := range all {
		d := Definition{Symbol: symbol, Kind: graph.KindBead, Binding: input.Bindings[symbol], Ancestors: []Symbol{symbol}, Abstract: symbol == Conformance}
		if isLink(symbol) {
			d.Kind = graph.KindLink
		}
		if ancestor := parent(symbol); ancestor != "" {
			base, ok := p.definitions[ancestor]
			if !ok {
				return nil, invalid("fixed parent order missing %s", ancestor)
			}
			d.Parents = []typecatalog.Reference{base.Binding.Type}
			d.Ancestors = append(d.Ancestors, base.Ancestors...)
			d.Properties = cloneProperties(base.Properties)
			d.Ownership = append([]Ownership(nil), base.Ownership...)
			d.EndpointRules = cloneEndpointRules(base.EndpointRules)
			d.GraphRules = append([]GraphRule(nil), base.GraphRules...)
			d.Meanings = append([]Meaning(nil), base.Meanings...)
		}
		d.LocalProperties = localProperties(symbol)
		d.Properties = append(d.Properties, cloneProperties(d.LocalProperties)...)
		for _, family := range localOwnership(symbol) {
			d.LocalOwnership = append(d.LocalOwnership, Ownership{DeclaredBy: symbol, Family: family, Type: input.Bindings[family].Type})
		}
		d.Ownership = append(d.Ownership, d.LocalOwnership...)
		if d.Kind == graph.KindLink {
			d.LocalEndpointPairs = endpointPairs(symbol, input)
			d.EndpointRules = append(d.EndpointRules, EndpointRule{DeclaredBy: symbol, Pairs: clonePairs(d.LocalEndpointPairs)})
			d.LocalGraphRule = localGraphRule(symbol, d.Binding.Type)
			d.GraphRules = append(d.GraphRules, d.LocalGraphRule)
		}
		for _, rule := range localMeanings(symbol) {
			d.Meanings = append(d.Meanings, Meaning{DeclaredBy: symbol, Rule: rule})
		}
		p.definitions[symbol] = d
	}
	p.issue = ExistingDomainRequirement{Role: "issue", Type: input.Issue, NativePayloadValidationRequired: true, IntegrationState: "not-integrated"}
	for _, family := range []Symbol{SubjectTo} {
		p.issue.OwnershipRequired = append(p.issue.OwnershipRequired, Ownership{Family: family, Type: input.Bindings[family].Type})
	}
	return p, nil
}

func validReference(ref typecatalog.Reference) error {
	if err := graph.ValidateTypeURL(ref.URI); err != nil {
		return err
	}
	if !utf8.ValidString(ref.Revision) {
		return invalid("Type revision must be UTF-8 for %s", ref.URI)
	}
	return nil
}

func isLink(symbol Symbol) bool {
	for _, link := range linkSymbols() {
		if symbol == link {
			return true
		}
	}
	return false
}

// Source returns the selected immutable publication identity.
func (p *Projection) Source() Source { return SelectedSource() }

// Definitions returns selected Bead then Link declarations in source order.
// This internal order does not select a public CLI ordering or output schema.
func (p *Projection) Definitions() []Definition {
	result := make([]Definition, 0, len(p.definitions))
	for _, symbol := range symbols() {
		result = append(result, cloneDefinition(p.definitions[symbol]))
	}
	return result
}

// Definition returns a copied declaration; unknown/retired names never alias one.
func (p *Projection) Definition(symbol Symbol) (Definition, bool) {
	d, ok := p.definitions[symbol]
	if !ok {
		return Definition{}, false
	}
	return cloneDefinition(d), true
}

// ExistingIssue returns nominal/ownership integration requirements only.
func (p *Projection) ExistingIssue() ExistingDomainRequirement {
	r := p.issue
	r.OwnershipRequired = append([]Ownership(nil), r.OwnershipRequired...)
	return r
}

// OtherSourceOwnershipRequirements applies to every participating non-Memory
// source definition, not only Issue. It installs nothing on those definitions.
func (p *Projection) OtherSourceOwnershipRequirements() []Ownership {
	return p.ExistingIssue().OwnershipRequired
}

// SupportMap reports the boundary of this projection. Runtime support is not
// inferred from source publication or from the existence of these declarations.
func (p *Projection) SupportMap() map[string]Support {
	return map[string]Support{
		"fixed-definitions":             {State: "declared", Detail: "eighteen fixed symbols; caller-bound identities; not installation"},
		"properties":                    {State: "shape-fragments-only", Detail: "requiredness/nonblank/reference checks retained separately; no complete properties schema or extension policy"},
		"endpoint-pairs":                {State: "declared-not-admitted", Detail: "same-Scope correlated pairs; caller must establish actual selected endpoint facts"},
		"ownership":                     {State: "declared-not-integrated", Detail: "positive families; native source retention/revisions and grouping not implemented"},
		"graph-rules":                   {State: "declared-not-enforced", Detail: "identity-self, family maxima/acyclic/floating constraints retained; no graph evaluator"},
		"applicability":                 {State: "reserved-undefined", SpecificationDeferred: true, Detail: "no concrete value schema/evaluator/authority/context; no runtime preserve-versus-refuse choice"},
		"derived-claims":                {State: "selected-not-implemented", Detail: "supporting paths, necessity, additive inclusion, compatible satisfaction chains and unresolved/conflict behavior retained as meanings"},
		"revision-context-mechanics":    {State: "specification-deferred", SpecificationDeferred: true, Detail: "exact subject revisions selected; actual retained Bead/revision admission requires a runtime bridge; no new context or authority policy"},
		"structured-condition-evidence": {State: "specification-deferred", SpecificationDeferred: true, Detail: "no conditional override, sufficiency solver or structured adjudication; text evidence retained"},
		"native-domain-validation":      {State: "bridge-required", Detail: "existing Issue validation remains native; nominal facts are not a permissive validator"},
		"crud-cli-storage":              {State: "not-integrated", Detail: "no public selectors, persistence, recovery/retype, Type installation or bd type tree"},
		"direct-conformance-authoring":  {State: "abstract-definition", Detail: "Conformance cannot be instantiated directly; concrete refinements remain concrete"},
		"operation-admission":           {State: "unavailable", Detail: "projection cannot establish authorization, guards, native atomicity or full Type compliance"},
	}
}

func textProperty(origin Symbol, name string, required, markdown, nonblank, label bool) Property {
	p := Property{Name: name, DeclaredBy: origin, Required: required, Shape: "text", Markdown: markdown, Nonblank: nonblank, OpenLabel: label, ShapeFragment: json.RawMessage(`{"type":"string"}`)}
	if nonblank {
		p.AdditionalChecks = []string{"nonblank-text"}
	}
	return p
}

func localProperties(symbol Symbol) []Property {
	switch symbol {
	case Memory:
		return []Property{textProperty(symbol, "title", true, false, true, false), textProperty(symbol, "body", true, true, false, false), textProperty(symbol, "status", false, false, true, true)}
	case Evaluation:
		return []Property{{Name: "subjects", DeclaredBy: symbol, Required: true, Shape: "exact-bead-references", ShapeFragment: json.RawMessage(`{"type":"array","minItems":1,"items":{"type":"object","properties":{"uri":{"type":"string","minLength":1},"revision":{"type":"string","minLength":1}},"required":["uri","revision"],"additionalProperties":false}}`), AdditionalChecks: []string{"shared-reference-uri-syntax-and-semantics", "exact-retained-bead-revision", "same-scope-subject-bindings", "collective-full-subject-group"}}}
	case Constraint:
		return []Property{{Name: "applicability", DeclaredBy: symbol, Shape: "reserved-undefined", ReservedReason: "concrete executable representation/evaluator deferred; no authored value admitted by this declaration"}}
	case Policy, Decision:
		return []Property{{Name: "authority", DeclaredBy: symbol, Shape: "reference", ShapeFragment: json.RawMessage(`{"anyOf":[{"type":"string"},{"type":"object","properties":{"uri":{"type":"string"},"revision":{"type":"string","minLength":1}},"required":["uri","revision"],"additionalProperties":false}]}`), AdditionalChecks: []string{"shared-reference-uri-syntax-and-semantics"}}}
	case Specification:
		return []Property{textProperty(symbol, "version", false, false, true, true)}
	case References:
		return []Property{textProperty(symbol, "title", false, false, true, false), textProperty(symbol, "body", false, true, true, false), textProperty(symbol, "status", false, false, true, true)}
	case Conformance:
		return []Property{textProperty(symbol, "evidence", false, false, true, false)}
	}
	return nil
}

func localOwnership(symbol Symbol) []Symbol {
	switch symbol {
	case Memory:
		return []Symbol{SubjectTo, SupersededBy}
	case Evaluation:
		return []Symbol{Conformance}
	case Constraint:
		return []Symbol{Requires}
	}
	return nil
}

func endpoint(ref typecatalog.Reference) typecatalog.EndpointConstraint {
	return typecatalog.EndpointConstraint{ResourceKind: "bead", Scope: "same", Types: typecatalog.Selector{AnyOf: []typecatalog.Reference{ref}}}
}

func endpointPairs(symbol Symbol, input Inputs) []typecatalog.EndpointPair {
	memory := endpoint(input.Bindings[Memory].Type)
	constraint := endpoint(input.Bindings[Constraint].Type)
	issue := endpoint(input.Issue)
	anyBead := typecatalog.EndpointConstraint{ResourceKind: "bead", Scope: "same", Types: typecatalog.Selector{Any: true}}
	switch symbol {
	case References:
		return []typecatalog.EndpointPair{{Source: memory, Target: memory}, {Source: memory, Target: issue}, {Source: issue, Target: memory}, {Source: anyBead, Target: constraint}}
	case SupersededBy:
		return []typecatalog.EndpointPair{{Source: memory, Target: memory}}
	case Requires, Defines:
		return []typecatalog.EndpointPair{{Source: constraint, Target: constraint}}
	case SubjectTo:
		return []typecatalog.EndpointPair{{Source: anyBead, Target: constraint}}
	case Conformance, Addresses, Satisfies, Violates:
		return []typecatalog.EndpointPair{{Source: endpoint(input.Bindings[Evaluation].Type), Target: constraint}}
	}
	return nil
}

func localGraphRule(symbol Symbol, family typecatalog.Reference) GraphRule {
	r := GraphRule{DeclaredBy: symbol, Family: family, Population: "live-links"}
	switch symbol {
	case References:
		r.ForbidIdentitySelf, r.SameScope = true, true
	case SupersededBy:
		r.SourceMax = Limit{Present: true, Value: 1}
		r.Acyclic, r.FloatingSource, r.FloatingTarget = true, true, true
	case Defines:
		r.TargetMax = Limit{Present: true, Value: 1}
		r.Acyclic = true
	}
	return r
}

func localMeanings(symbol Symbol) []string {
	switch symbol {
	case Evaluation:
		return []string{"one-nonempty-exact-subject-group-assessed-collectively", "all-owned-claims-concern-full-group-no-subset-selector", "claim-mutation-revises-evaluation-not-assessed-subjects", "independent-evaluations-of-same-group-permitted", "no-component-substitution-or-distribution"}
	case Constraint:
		return []string{"requires-targets-necessary-not-sufficient", "empty-child-set-not-satisfaction", "conditions-in-body-not-automatic-dependency-overrides", "implicit-applicability-additive-never-filters-explicit"}
	case References:
		return []string{"direct-references-unowned", "direct-parallel-and-cycles-permitted-subtypes-may-tighten", "target-current-or-exact-under-shared-contract", "descriptive-status-no-workflow-effect"}
	case SupersededBy:
		return []string{"retiring-source-to-any-memory-subtype-replacement", "no-delete-status-approval-or-history-rewrite"}
	case Requires:
		return []string{"necessity-transitive-including-defines", "requires-cycles-and-parallel-permitted-not-evidence", "requires-alone-no-defining-parent"}
	case Defines:
		return []string{"parent-to-child-independent-identity", "no-cascade-or-order-numbering", "defines-family-only-tree-restrictions"}
	case SubjectTo:
		return []string{"any-bead-subject", "subject-to-nontransitive-obligations-via-target-requires-defines", "retain-paths-terminate-repeats-no-inferred-stored-links", "applicability-not-assessment-or-binding-authority-proof"}
	case Conformance:
		return []string{"neutral-claim-no-positive-negative-partial-or-verified-conclusion", "independent-parallel-evidence-claims-preserved", "evaluation-owned-claims-change-evaluation-not-subjects-or-target", "claim-actor-distinct-from-evaluation-and-subjects", "all-claims-concern-full-collective-group", "no-general-conformance-transitivity-or-automatic-coverage", "circular-claims-supply-no-evidence"}
	case Addresses:
		return []string{"contribution-unspecified-completeness", "no-automatic-addresses-transitivity"}
	case Satisfies:
		return []string{"complete-satisfaction-claim-not-verified-proof-or-aspiration", "compatible-claim-composition-through-evaluation-subject-bindings", "singleton-intermediate-constraint-only-no-collective-distribution", "no-native-satisfies-link-transitivity", "conditions-and-necessary-children-accounted-for", "n-a-label-not-machine-verdict", "necessary-violation-conflict-preserve-claims-no-inferred-violates-or-addresses"}
	case Violates:
		return []string{"breach-claim-not-contribution-or-verification", "no-implicit-violation-transitivity", "missing-assessment-or-evidence-unresolved-not-violation", "necessary-violation-conflict-preserve-claims-no-inferred-violates-or-addresses"}
	}
	return nil
}

func cloneProperties(properties []Property) []Property {
	result := append([]Property(nil), properties...)
	for i := range result {
		result[i].ShapeFragment = append(json.RawMessage(nil), result[i].ShapeFragment...)
		result[i].AdditionalChecks = append([]string(nil), result[i].AdditionalChecks...)
	}
	return result
}

func clonePairs(pairs []typecatalog.EndpointPair) []typecatalog.EndpointPair {
	result := append([]typecatalog.EndpointPair(nil), pairs...)
	for i := range result {
		result[i].Source.Types.AnyOf = append([]typecatalog.Reference(nil), result[i].Source.Types.AnyOf...)
		result[i].Target.Types.AnyOf = append([]typecatalog.Reference(nil), result[i].Target.Types.AnyOf...)
	}
	return result
}

func cloneEndpointRules(rules []EndpointRule) []EndpointRule {
	result := append([]EndpointRule(nil), rules...)
	for i := range result {
		result[i].Pairs = clonePairs(result[i].Pairs)
	}
	return result
}

func cloneDefinition(d Definition) Definition {
	d.Parents = append([]typecatalog.Reference(nil), d.Parents...)
	d.Ancestors = append([]Symbol(nil), d.Ancestors...)
	d.LocalProperties, d.Properties = cloneProperties(d.LocalProperties), cloneProperties(d.Properties)
	d.LocalOwnership, d.Ownership = append([]Ownership(nil), d.LocalOwnership...), append([]Ownership(nil), d.Ownership...)
	d.LocalEndpointPairs, d.EndpointRules = clonePairs(d.LocalEndpointPairs), cloneEndpointRules(d.EndpointRules)
	d.GraphRules = append([]GraphRule(nil), d.GraphRules...)
	d.Meanings = append([]Meaning(nil), d.Meanings...)
	return d
}
