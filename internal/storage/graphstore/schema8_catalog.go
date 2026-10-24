package graphstore

import (
	"bytes"
	_ "embed"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/typecatalog"
	"github.com/steveyegge/beads/internal/typeconstraints"
)

// schema8TypeTemplates are frozen descriptors from the qualified schema8 fork.
// They preserve original Types; they are not remapped to Preview3 References.
//
//go:embed schema8_descriptors.json
var schema8TypeTemplates []byte

var schema8LinkNames = []string{"preview-related-v2", "example-follows", "example-cites"}

func schema8Descriptors(scope string) ([]graph.TypeDescriptor, error) {
	raw := bytes.ReplaceAll(schema8TypeTemplates, []byte("https://schema8.example.invalid/"), []byte(scope))
	var values []json.RawMessage
	if err := json.Unmarshal(raw, &values); err != nil {
		return nil, err
	}
	result := make([]graph.TypeDescriptor, 0, len(values))
	for _, v := range values {
		d, err := graph.ParseTypeDescriptor(v)
		if err != nil {
			return nil, err
		}
		result = append(result, d)
	}
	return result, nil
}
func schema8Descriptor(scope, name string) (graph.TypeDescriptor, error) {
	values, err := schema8Descriptors(scope)
	if err != nil {
		return graph.TypeDescriptor{}, err
	}
	for _, v := range values {
		if v.ID() == scope+"types/"+name {
			return v, nil
		}
	}
	return graph.TypeDescriptor{}, fmt.Errorf("%w: missing frozen schema8 descriptor", ErrInvalidStore)
}
func schema8Informational(scope, id string) bool {
	for _, name := range schema8LinkNames {
		if id == scope+"types/"+name {
			return true
		}
	}
	return false
}
func schema8CatalogDefinitions(scope string, definitions []typecatalog.Definition, rules []typeconstraints.Rule) ([]typecatalog.Definition, []typeconstraints.Rule) {
	endpoint := typecatalog.EndpointConstraint{ResourceKind: "bead", Scope: "same", Types: typecatalog.Selector{Any: true}}
	for _, name := range schema8LinkNames {
		ref := typecatalog.Reference{URI: scope + "types/" + name}
		definitions = append(definitions, typecatalog.Definition{SchemaBase: scope + "schemas/schema8-" + name, Identity: ref, Name: "Schema8 " + name, Kind: graph.KindLink, Public: true,
			EndpointPairs:    []typecatalog.EndpointPair{{Source: endpoint, Target: endpoint}},
			PropertiesSchema: json.RawMessage(`{"type":"object","properties":{"note":{"type":"string"}},"additionalProperties":false}`)})
		for i := range definitions {
			if definitions[i].Identity.URI == MemoryTypeURL(scope) {
				definitions[i].OwnsOutgoing = append(definitions[i].OwnsOutgoing, typecatalog.Selector{AnyOf: []typecatalog.Reference{ref}})
			}
		}
		rules = append(rules, typeconstraints.Rule{Family: ref, SameScope: true, FloatingSource: true, FloatingTarget: true})
	}
	return definitions, rules
}
func validateSchema8LinkProperties(raw json.RawMessage) error {
	var values map[string]json.RawMessage
	if json.Unmarshal(raw, &values) != nil || values == nil {
		return fmt.Errorf("%w: schema8 properties require an object", storage.ErrValidation)
	}
	for name, value := range values {
		var note string
		if name != "note" || bytes.Equal(bytes.TrimSpace(value), []byte("null")) || json.Unmarshal(value, &note) != nil || !utf8.ValidString(note) {
			return fmt.Errorf("%w: schema8 informational properties", storage.ErrValidation)
		}
	}
	return nil
}

func schema8TypeName(id, scope string) string { return strings.TrimPrefix(id, scope+"types/") }
