package graphstore

import (
	"encoding/json"
	"fmt"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memorycatalog"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/typecatalog"
	"github.com/steveyegge/beads/internal/typeconstraints"
	"github.com/steveyegge/beads/internal/types"
)

// selectedTypeCatalog combines complete payload validation, native-domain
// nominal facts and declaration-driven graph checks. It is internal state,
// never a public Type installation or authored executable-schema facility.
type selectedTypeCatalog struct {
	projection  *memorycatalog.Projection
	types       *typecatalog.Catalog
	graph       *typeconstraints.Validator
	definitions map[string]memorycatalog.Definition
}

func (s *Store) selectedTypes() (*selectedTypeCatalog, error) {
	s.catalogOnce.Do(func() { s.catalog, s.catalogErr = newSelectedTypeCatalog(s.ScopeURL()) })
	return s.catalog, s.catalogErr
}

func (s *Store) isMemoryType(id string) bool {
	c, err := s.selectedTypes()
	return err == nil && c.memoryType(id)
}

func recordType(record any) string {
	switch value := record.(type) {
	case Record:
		return value.Type
	case IssueRecord:
		return value.Type
	}
	return ""
}

func (s *Store) ownsLink(source any, linkType string) (bool, error) {
	c, err := s.selectedTypes()
	if err != nil {
		return false, err
	}
	return c.types.Owns(typecatalog.Reference{URI: nativeFamilyReference(s.ScopeURL(), recordType(source))}, typecatalog.Reference{URI: nativeFamilyReference(s.ScopeURL(), linkType)})
}

// SelectedOwnedLinkTypes supplies effective ownership for complete record
// projection. Partial inventory descriptors cannot carry family ownership.
// This is a pure lookup in the fixed selected catalog, not a storage read or
// a new declaration of finite semantic maxima.
func (s *Store) SelectedOwnedLinkTypes(beadType string) ([]string, error) {
	return s.SelectedOwnedLinkTypesWithObserved(beadType)
}

// SelectedOwnedLinkTypesWithObserved adds native concrete ownership from an
// already captured snapshot; it performs no storage reads.
func (s *Store) SelectedOwnedLinkTypesWithObserved(beadType string, observedLinkTypes ...string) ([]string, error) {
	c, err := s.selectedTypes()
	if err != nil {
		return nil, err
	}
	family := nativeFamilyReference(s.ScopeURL(), beadType)
	definition, ok := c.types.Definition(family)
	if !ok || definition.Kind != graph.KindBead {
		return nil, fmt.Errorf("%w: unknown selected Bead ownership contract", ErrInvalidStore)
	}
	result := []string{}
	for _, link := range c.types.Public(graph.KindLink) {
		if link.Abstract {
			continue
		}
		owns, err := c.types.Owns(typecatalog.Reference{URI: family}, link.Identity)
		if err != nil {
			return nil, err
		}
		if owns {
			result = append(result, link.Identity.URI)
		}
	}
	if family == IssueTypeURL(s.ScopeURL()) {
		seen := map[string]bool{}
		for _, id := range result {
			seen[id] = true
		}
		for _, kind := range types.WellKnownDependencyTypes() {
			id, _ := NativeDependencyTypeURL(s.ScopeURL(), string(kind))
			if !seen[id] {
				result = append(result, id)
				seen[id] = true
			}
		}
		for _, id := range observedLinkTypes {
			if isConcreteNativeDependency(s.ScopeURL(), id) && !seen[id] {
				result = append(result, id)
				seen[id] = true
			}
		}
	}
	return result, nil
}

func newSelectedMemoryProjection(scope string) (*memorycatalog.Projection, error) {
	if err := graph.ValidateScopeURL(scope); err != nil {
		return nil, err
	}
	input := memorycatalog.Inputs{
		Bindings: map[memorycatalog.Symbol]memorycatalog.Binding{},
		Issue:    typecatalog.Reference{URI: IssueTypeURL(scope)},
	}
	// Bindings are application-local identities. They do not claim that the
	// shared specification assigns global Type IDs or definition revisions.
	for _, symbol := range []memorycatalog.Symbol{
		memorycatalog.Memory, memorycatalog.Evaluation, memorycatalog.Constraint,
		memorycatalog.Policy, memorycatalog.Decision, memorycatalog.Procedure,
		memorycatalog.Requirement, memorycatalog.Scenario, memorycatalog.Specification,
		memorycatalog.References, memorycatalog.SupersededBy, memorycatalog.Requires,
		memorycatalog.Defines, memorycatalog.SubjectTo, memorycatalog.Conformance,
		memorycatalog.Addresses, memorycatalog.Satisfies, memorycatalog.Violates,
	} {
		input.Bindings[symbol] = memorycatalog.Binding{
			Type:       typecatalog.Reference{URI: scope + "types/" + string(symbol)},
			SchemaBase: scope + "schemas/" + string(symbol),
		}
	}
	return memorycatalog.New(input)
}

func newSelectedTypeCatalog(scope string) (*selectedTypeCatalog, error) {
	projection, err := newSelectedMemoryProjection(scope)
	if err != nil {
		return nil, err
	}
	selected := &selectedTypeCatalog{projection: projection, definitions: map[string]memorycatalog.Definition{}}
	var definitions []typecatalog.Definition
	var rules []typeconstraints.Rule
	for _, declaration := range projection.Definitions() {
		definition := typecatalog.Definition{
			Identity: declaration.Binding.Type, Name: string(declaration.Symbol),
			Kind: declaration.Kind, Public: true, Abstract: declaration.Abstract,
			Parents: declaration.Parents, SchemaBase: declaration.Binding.SchemaBase,
			EndpointPairs: declaration.LocalEndpointPairs,
		}
		definition.PropertiesSchema, err = declarationPropertiesSchema(declaration.LocalProperties)
		if err != nil {
			return nil, err
		}
		for _, owns := range declaration.LocalOwnership {
			definition.OwnsOutgoing = append(definition.OwnsOutgoing, typecatalog.Selector{AnyOf: []typecatalog.Reference{owns.Type}})
		}
		definitions = append(definitions, definition)
		selected.definitions[definition.Identity.URI] = declaration
		if declaration.Kind == graph.KindLink {
			rule := declaration.LocalGraphRule
			rules = append(rules, typeconstraints.Rule{
				Family:    rule.Family,
				SourceMax: typeconstraints.Limit{Set: rule.SourceMax.Present, Value: rule.SourceMax.Value},
				TargetMax: typeconstraints.Limit{Set: rule.TargetMax.Present, Value: rule.TargetMax.Value},
				Acyclic:   rule.Acyclic, Unique: rule.Unique,
				ForbidIdentitySelf: rule.ForbidIdentitySelf, SameScope: rule.SameScope,
				FloatingSource: rule.FloatingSource, FloatingTarget: rule.FloatingTarget,
			})
		}
	}
	// Existing native aggregates retain their actual validators. Nominal facts
	// let Memory Links reference Issues without replacing native validation.
	issue := typecatalog.Definition{
		Identity: typecatalog.Reference{URI: IssueTypeURL(scope)}, Name: "issue", Kind: graph.KindBead, Public: true, Abstract: true, NominalOnly: true,
		OwnsOutgoing: []typecatalog.Selector{{AnyOf: []typecatalog.Reference{{URI: DependencyTypeURL(scope)}}}},
	}
	for _, owns := range projection.ExistingIssue().OwnershipRequired {
		issue.OwnsOutgoing = append(issue.OwnsOutgoing, typecatalog.Selector{AnyOf: []typecatalog.Reference{owns.Type}})
	}
	nativeEndpoint := typecatalog.EndpointConstraint{ResourceKind: "bead", Scope: "same", Types: typecatalog.Selector{AnyOf: []typecatalog.Reference{{URI: IssueTypeURL(scope)}}}}
	definitions = append(definitions, issue, typecatalog.Definition{
		Identity: typecatalog.Reference{URI: DependencyTypeURL(scope)}, Name: "dependency",
		Kind: graph.KindLink, Public: true, Abstract: true, NominalOnly: true,
		EndpointPairs: []typecatalog.EndpointPair{{Source: nativeEndpoint, Target: nativeEndpoint}},
	})
	definitions, rules = schema8CatalogDefinitions(scope, definitions, rules)
	selected.types, err = typecatalog.New(definitions)
	if err != nil {
		return nil, err
	}
	selected.graph, err = typeconstraints.New(selected.types, rules)
	if err != nil {
		return nil, err
	}
	return selected, nil
}

func declarationPropertiesSchema(properties []memorycatalog.Property) (json.RawMessage, error) {
	fields := map[string]json.RawMessage{}
	var required []string
	for _, property := range properties {
		if property.Shape == "reserved-undefined" {
			// Deferred executable predicates are refused, not treated as an
			// arbitrary extension value or silently interpreted as code.
			fields[property.Name] = json.RawMessage(`false`)
		} else {
			if len(property.ShapeFragment) == 0 {
				return nil, fmt.Errorf("%w: missing selected shape for %s", storage.ErrValidation, property.Name)
			}
			fields[property.Name] = property.ShapeFragment
		}
		if property.Required {
			required = append(required, property.Name)
		}
	}
	schema := map[string]any{"type": "object", "properties": fields}
	if len(required) != 0 {
		schema["required"] = required
	}
	return canonicalJSON(schema)
}

func (c *selectedTypeCatalog) memoryType(id string) bool {
	return c.types.Conforms(typecatalog.Reference{URI: id}, typecatalog.Reference{URI: c.projectionBinding(memorycatalog.Memory)})
}

func (c *selectedTypeCatalog) evaluationType(id string) bool {
	return c.types.Conforms(typecatalog.Reference{URI: id}, typecatalog.Reference{URI: c.projectionBinding(memorycatalog.Evaluation)})
}

func (c *selectedTypeCatalog) projectionBinding(symbol memorycatalog.Symbol) string {
	d, _ := c.projection.Definition(symbol)
	return d.Binding.Type.URI
}

func (c *selectedTypeCatalog) validateProperties(id string, raw json.RawMessage, kind graph.ResourceKind) error {
	ref := typecatalog.Reference{URI: id}
	if err := c.types.ValidateDeclaredType(ref, kind); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	if schema8Informational(strings.TrimSuffix(c.projectionBinding(memorycatalog.Memory), "types/memory"), id) {
		return validateSchema8LinkProperties(raw)
	}
	declaration, ok := c.definitions[id]
	if !ok {
		return fmt.Errorf("%w: payload requires its native domain validator", storage.ErrValidation)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return fmt.Errorf("%w: invalid properties: %v", storage.ErrValidation, err)
	}
	for _, property := range declaration.Properties {
		value, present := fields[property.Name]
		if !present {
			continue
		}
		if property.Shape == "reserved-undefined" {
			return fmt.Errorf("%w: %s has no selected executable representation", ErrCapabilityUnavailable, property.Name)
		}
		if property.Nonblank {
			var text string
			if json.Unmarshal(value, &text) != nil || strings.TrimSpace(text) == "" {
				return fmt.Errorf("%w: %s requires nonblank text", storage.ErrValidation, property.Name)
			}
		}
		if property.Shape == "reference" {
			if err := validateAuthoredReference(value, false); err != nil {
				return err
			}
		}
		if property.Shape == "exact-bead-references" {
			var references []json.RawMessage
			if json.Unmarshal(value, &references) != nil || len(references) == 0 {
				return fmt.Errorf("%w: subjects requires nonempty exact Bead references", storage.ErrValidation)
			}
			for _, reference := range references {
				if err := validateAuthoredReference(reference, true); err != nil {
					return err
				}
			}
		}
	}
	if err := c.types.ValidateProperties(ref, raw); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	return nil
}

func validateAuthoredReference(raw json.RawMessage, exact bool) error {
	var uri string
	if !exact && len(raw) != 0 && raw[0] == '"' {
		if err := json.Unmarshal(raw, &uri); err != nil {
			return fmt.Errorf("%w: invalid Reference", storage.ErrValidation)
		}
	} else {
		var reference struct {
			URI      string `json:"uri"`
			Revision string `json:"revision"`
		}
		if json.Unmarshal(raw, &reference) != nil || reference.Revision == "" {
			return fmt.Errorf("%w: exact Reference requires uri and nonempty revision", storage.ErrValidation)
		}
		uri = reference.URI
	}
	if err := graph.ValidateReferenceURI(uri); err != nil {
		return fmt.Errorf("%w: Reference requires an absolute URI", storage.ErrValidation)
	}
	return nil
}

// IsMemoryTypeURL recognizes only the installed fixed Memory family.
func IsMemoryTypeURL(scope, id string) bool {
	for _, name := range selectedMemoryBeadNames {
		if id == scope+"types/"+name {
			return true
		}
	}
	return false
}
