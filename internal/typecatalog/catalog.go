// Package typecatalog resolves a fixed, application-selected Type catalog.
// It does not install Types, infer affiliation from properties, or read storage.
package typecatalog

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/santhosh-tekuri/jsonschema/v6"
	graph "github.com/steveyegge/beads/graphops"
)

// Reference selects an identity, or an exact revision when Revision is present.
// A fixed catalog retains exactly one selected definition per identity. An
// identity-only reference never requests a lookup of the latest definition.
type Reference struct {
	URI      string
	Revision string
}

// Selector is either Any or a nonempty AnyOf, never both.
type Selector struct {
	Any   bool
	AnyOf []Reference
}

// Definition is trusted application input, not a public descriptor wire shape.
// Kind is the category of instances, not the definition Bead's affiliation.
// Each local schema validates the complete properties object independently;
// a child cannot relax a parent's schema by replacing it.
type Definition struct {
	Identity         Reference
	Name             string
	Kind             graph.ResourceKind
	Public           bool
	Abstract         bool // forbids direct instances; refinements remain independently concrete
	NominalOnly      bool // native domain owns payload validation; no substitute schema
	Parents          []Reference
	PropertiesSchema json.RawMessage
	SchemaBase       string // explicit retrieval URI; this module chooses no anonymous-schema base
	SchemaResources  map[string]json.RawMessage
	OwnsOutgoing     []Selector
	EndpointPairs    []EndpointPair // Link Types only; union locally, intersection through parents
}

type entry struct {
	definition Definition
	closure    []string // includes self once, including through diamonds
	schema     *jsonschema.Schema
}

// Catalog is immutable after construction. It contains no mutable global state
// and has no installation, promotion or storage API.
type Catalog struct {
	entries map[string]*entry
	ids     []string
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: type catalog: %s", graph.ErrValidation, fmt.Sprintf(format, args...))
}

// New checks the complete selected graph and compiles retained schemas offline.
// Unknown parents/references, cross-category conformance and cycles are refused.
// Inputs and all returned descriptor data are copied.
func New(definitions []Definition) (*Catalog, error) {
	c := &Catalog{entries: make(map[string]*entry, len(definitions))}
	for _, input := range definitions {
		d := cloneDefinition(input)
		if err := validReference(d.Identity); err != nil {
			return nil, err
		}
		id := d.Identity.URI
		if _, exists := c.entries[id]; exists {
			return nil, invalid("duplicate identity %s", id)
		}
		if d.Name == "" || !utf8.ValidString(d.Name) || !d.Kind.Valid() {
			return nil, invalid("%s needs a UTF-8 name and bead/link category", id)
		}
		if d.Public && intrinsicName(d.Name) {
			return nil, invalid("intrinsic metatype %s cannot be public", d.Name)
		}
		if d.Kind == graph.KindBead && len(d.EndpointPairs) != 0 {
			return nil, invalid("Bead Type %s cannot have endpoint pairs", id)
		}
		if d.Kind == graph.KindLink && (len(d.EndpointPairs) == 0 || len(d.OwnsOutgoing) != 0) {
			return nil, invalid("Link Type %s needs endpoint pairs and cannot own Links", id)
		}
		var schema *jsonschema.Schema
		if d.NominalOnly {
			if len(d.PropertiesSchema) != 0 || d.SchemaBase != "" || len(d.SchemaResources) != 0 {
				return nil, invalid("%s nominal-only definition cannot supply a properties schema", id)
			}
		} else {
			var err error
			schema, err = compileProperties(d)
			if err != nil {
				return nil, invalid("%s properties schema: %v", id, err)
			}
		}
		c.entries[id] = &entry{definition: d, schema: schema}
		c.ids = append(c.ids, id)
	}
	codeUnitSort(c.ids)
	for _, id := range c.ids {
		d := c.entries[id].definition
		seen := map[string]bool{}
		for _, parent := range d.Parents {
			if err := c.require(parent, d.Kind); err != nil {
				return nil, err
			}
			if seen[parent.URI] {
				return nil, invalid("%s repeats parent %s", id, parent.URI)
			}
			seen[parent.URI] = true
		}
		for _, selector := range d.OwnsOutgoing {
			if err := c.checkSelector(selector, graph.KindLink); err != nil {
				return nil, err
			}
		}
		for _, pair := range d.EndpointPairs {
			for _, endpoint := range []EndpointConstraint{pair.Source, pair.Target} {
				if err := c.checkEndpointConstraint(endpoint); err != nil {
					return nil, err
				}
			}
		}
	}
	visiting := map[string]bool{}
	var resolve func(string) error
	resolve = func(id string) error {
		e := c.entries[id]
		if e.closure != nil {
			return nil
		}
		if visiting[id] {
			return invalid("conformance cycle at %s", id)
		}
		visiting[id] = true
		set := map[string]bool{id: true}
		for _, parent := range e.definition.Parents {
			if err := resolve(parent.URI); err != nil {
				return err
			}
			for _, ancestor := range c.entries[parent.URI].closure {
				set[ancestor] = true
			}
		}
		for ancestor := range set {
			e.closure = append(e.closure, ancestor)
		}
		codeUnitSort(e.closure)
		delete(visiting, id)
		return nil
	}
	for _, id := range c.ids {
		if err := resolve(id); err != nil {
			return nil, err
		}
	}
	return c, nil
}

func intrinsicName(name string) bool {
	switch strings.ToLower(name) {
	case "type", "beadtype", "linktype", "conformsto", "refines":
		return true
	}
	return false
}

// ValidateDeclaredType checks whether a selected Type may be declared directly.
// Abstractness belongs to the selected definition itself, not its ancestors.
// Nominal-only domains can have instances; their native writer validates payloads.
func (c *Catalog) ValidateDeclaredType(ref Reference, kind graph.ResourceKind) error {
	if err := c.require(ref, kind); err != nil {
		return err
	}
	if c.entries[ref.URI].definition.Abstract {
		return invalid("%s is abstract and cannot be declared directly", ref.URI)
	}
	return nil
}

func validReference(ref Reference) error {
	if err := graph.ValidateTypeURL(ref.URI); err != nil {
		return err
	}
	if !utf8.ValidString(ref.Revision) {
		return invalid("%s revision must be UTF-8", ref.URI)
	}
	return nil
}

func (c *Catalog) require(ref Reference, kind graph.ResourceKind) error {
	if err := validReference(ref); err != nil {
		return err
	}
	e, ok := c.entries[ref.URI]
	if !ok || (kind != "" && e.definition.Kind != kind) {
		return invalid("%s is not a selected %s Type", ref.URI, kind)
	}
	if ref.Revision != "" && ref.Revision != e.definition.Identity.Revision {
		return invalid("%s revision does not match the selected definition", ref.URI)
	}
	return nil
}

func (c *Catalog) checkSelector(selector Selector, kind graph.ResourceKind) error {
	if selector.Any == (len(selector.AnyOf) != 0) {
		return invalid("selector must have exactly one of any/anyOf")
	}
	seen := map[Reference]bool{}
	for _, ref := range selector.AnyOf {
		if seen[ref] {
			return invalid("selector repeats %s", ref.URI)
		}
		seen[ref] = true
		if err := c.require(ref, kind); err != nil {
			return err
		}
	}
	return nil
}

// Definition returns a copy of the selected definition and whether it exists.
func (c *Catalog) Definition(id string) (Definition, bool) {
	e, ok := c.entries[id]
	if !ok {
		return Definition{}, false
	}
	return cloneDefinition(e.definition), true
}

// Public lists externally selectable definitions in Type-identity code-unit
// order. Visibility never depends on whether an internal definition is retained.
// A zero kind lists both categories.
func (c *Catalog) Public(kind graph.ResourceKind) []Definition {
	var result []Definition
	for _, id := range c.ids {
		d := c.entries[id].definition
		if d.Public && (kind == "" || d.Kind == kind) {
			result = append(result, cloneDefinition(d))
		}
	}
	return result
}

// Conforms reports nominal/transitive membership only within this exact fixed
// selection. Unknown definitions and stale exact references never match.
func (c *Catalog) Conforms(actual, required Reference) bool {
	if c.require(actual, "") != nil || c.require(required, "") != nil {
		return false
	}
	for _, ancestor := range c.entries[actual.URI].closure {
		if ancestor == required.URI {
			return true
		}
	}
	return false
}

// Closure returns self and transitive parents, each once, in code-unit order.
func (c *Catalog) Closure(ref Reference) ([]Reference, error) {
	if err := c.require(ref, ""); err != nil {
		return nil, err
	}
	var result []Reference
	for _, id := range c.entries[ref.URI].closure {
		result = append(result, c.entries[id].definition.Identity)
	}
	return result, nil
}

// Owns reports positive ownership inherited from any parent, including matches
// through a specialized Link Type. Multiple matching declarations return one
// boolean; they must never cause a Link to be retained more than once.
func (c *Catalog) Owns(bead, link Reference) (bool, error) {
	if err := c.require(bead, graph.KindBead); err != nil {
		return false, err
	}
	if err := c.require(link, graph.KindLink); err != nil {
		return false, err
	}
	for _, id := range c.entries[bead.URI].closure {
		for _, selector := range c.entries[id].definition.OwnsOutgoing {
			if selector.Any {
				return true, nil
			}
			for _, required := range selector.AnyOf {
				if c.Conforms(link, required) {
					return true, nil
				}
			}
		}
	}
	return false, nil
}

func codeUnitSort(values []string) {
	sort.Slice(values, func(i, j int) bool { return graph.CompareCodeUnits(values[i], values[j]) < 0 })
}

func cloneDefinition(d Definition) Definition {
	d.Parents = append([]Reference(nil), d.Parents...)
	d.PropertiesSchema = append(json.RawMessage(nil), d.PropertiesSchema...)
	resources := make(map[string]json.RawMessage, len(d.SchemaResources))
	for uri, schema := range d.SchemaResources {
		resources[uri] = append(json.RawMessage(nil), schema...)
	}
	d.SchemaResources = resources
	d.OwnsOutgoing = append([]Selector(nil), d.OwnsOutgoing...)
	for i := range d.OwnsOutgoing {
		d.OwnsOutgoing[i].AnyOf = append([]Reference(nil), d.OwnsOutgoing[i].AnyOf...)
	}
	d.EndpointPairs = append([]EndpointPair(nil), d.EndpointPairs...)
	for i := range d.EndpointPairs {
		d.EndpointPairs[i].Source.Types.AnyOf = append([]Reference(nil), d.EndpointPairs[i].Source.Types.AnyOf...)
		d.EndpointPairs[i].Target.Types.AnyOf = append([]Reference(nil), d.EndpointPairs[i].Target.Types.AnyOf...)
	}
	return d
}
