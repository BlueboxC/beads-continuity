package typecatalog

import (
	"bytes"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/santhosh-tekuri/jsonschema/v6"
	graph "github.com/steveyegge/beads/graphops"
)

// EndpointConstraint represents one side of one correlated alternative.
// ResourceKind is bead or resource (opaque external resource, never a local
// Link). Beads require same/cross/either Scope and a Type selector. An opaque
// resource constraint has no Scope or Types members.
type EndpointConstraint struct {
	ResourceKind string
	Scope        string
	Types        Selector
}

// EndpointPair requires both endpoints to match this same row together.
type EndpointPair struct {
	Source EndpointConstraint
	Target EndpointConstraint
}

// EndpointFacts are established by the caller for the exact state being
// validated. Scope is same/cross for a resolved Bead. Resource is an opaque
// external resource with no asserted Type or Scope. This predicate establishes
// neither existence, visibility, retention nor endpoint revision/pin policy.
type EndpointFacts struct {
	ResourceKind string
	Scope        string
	Type         Reference
}

func (c *Catalog) checkEndpointConstraint(e EndpointConstraint) error {
	switch e.ResourceKind {
	case "resource":
		if e.Scope != "" || e.Types.Any || len(e.Types.AnyOf) != 0 {
			return invalid("resource endpoint has no Scope or Type restriction")
		}
	case "bead":
		if e.Scope != "same" && e.Scope != "cross" && e.Scope != "either" {
			return invalid("Bead endpoint needs same/cross/either Scope")
		}
		return c.checkSelector(e.Types, graph.KindBead)
	default:
		return invalid("endpoint category must be bead or resource")
	}
	return nil
}

func (c *Catalog) checkEndpointFacts(e EndpointFacts) error {
	switch e.ResourceKind {
	case "bead":
		if e.Scope != "same" && e.Scope != "cross" {
			return invalid("resolved Bead needs same/cross Scope evidence")
		}
		return c.require(e.Type, graph.KindBead)
	case "resource":
		if e.Scope != "" || e.Type != (Reference{}) {
			return invalid("opaque resource cannot assert Bead Scope or Type evidence")
		}
		return nil
	default:
		return invalid("unresolved/Link endpoint is not Bead or opaque resource evidence")
	}
}

func (c *Catalog) endpointMatches(rule EndpointConstraint, facts EndpointFacts) bool {
	if rule.ResourceKind != facts.ResourceKind {
		return false
	}
	if rule.ResourceKind == "resource" {
		return true
	}
	if rule.Scope != "either" && rule.Scope != facts.Scope {
		return false
	}
	if rule.Types.Any {
		return true
	}
	for _, ref := range rule.Types.AnyOf {
		if c.Conforms(facts.Type, ref) {
			return true
		}
	}
	return false
}

// ValidateEndpoints checks endpoint eligibility only, against every inherited
// Link definition. Each definition contributes a union of correlated pairs;
// all inherited definitions must pass. It does not check family populations,
// self identity, uniqueness, acyclicity, pin policies or participation counts.
func (c *Catalog) ValidateEndpoints(link Reference, source, target EndpointFacts) error {
	if err := c.require(link, graph.KindLink); err != nil {
		return err
	}
	for _, facts := range []EndpointFacts{source, target} {
		if err := c.checkEndpointFacts(facts); err != nil {
			return err
		}
	}
	for _, id := range c.entries[link.URI].closure {
		matched := false
		for _, pair := range c.entries[id].definition.EndpointPairs {
			if c.endpointMatches(pair.Source, source) && c.endpointMatches(pair.Target, target) {
				matched = true
				break
			}
		}
		if !matched {
			return invalid("endpoints match no correlated pair of %s", id)
		}
	}
	return nil
}

// ValidateProperties checks the complete resulting object independently against
// self and every ancestor's local schema. It never merges, defaults, converts,
// drops properties or infers nominal membership. Historical callers must use
// the catalog selected for that historical Type, not this method with a newer
// current definition.
func (c *Catalog) ValidateProperties(ref Reference, raw json.RawMessage) error {
	if err := c.require(ref, ""); err != nil {
		return err
	}
	canonical, err := graph.CanonicalizeJSON(raw)
	if err != nil {
		return err
	}
	if len(canonical) == 0 || canonical[0] != '{' {
		return invalid("properties must be an object")
	}
	value, err := jsonschema.UnmarshalJSON(bytes.NewReader(canonical))
	if err != nil {
		return invalid("properties JSON: %v", err)
	}
	for _, id := range c.entries[ref.URI].closure {
		if c.entries[id].definition.NominalOnly {
			return invalid("properties of %s require its native domain validator", id)
		}
		if err := c.entries[id].schema.Validate(value); err != nil {
			return invalid("properties violate %s: %v", id, err)
		}
	}
	return nil
}

type offlineLoader struct{}

func (offlineLoader) Load(uri string) (any, error) {
	return nil, fmt.Errorf("schema resource is not retained: %s", uri)
}

func compileProperties(d Definition) (*jsonschema.Schema, error) {
	if err := checkSchemaRetrievalURI(d.SchemaBase); err != nil {
		return nil, fmt.Errorf("explicit schema retrieval URI: %w", err)
	}
	compiler := jsonschema.NewCompiler()
	compiler.DefaultDraft(jsonschema.Draft2020)
	compiler.AssertFormat()
	compiler.UseLoader(offlineLoader{})
	// This is an internal compiler admission hook, not an authored vocabulary
	// or an extension of the descriptor contract. The keyword walk below also
	// checks unused schema positions. This hook additionally checks every object
	// the compiler actually treats as a schema, including JSON-pointer targets
	// outside those keyword positions. Literal data not used as a schema is
	// never visited by the compiler hook.
	compiler.RegisterVocabulary(&jsonschema.Vocabulary{
		URL: "urn:beads:typecatalog:baseline-admission",
		Compile: func(ctx *jsonschema.CompilerContext, object map[string]any) (jsonschema.SchemaExt, error) {
			current := ctx.Enqueue(nil)
			if current.DraftVersion != 2020 {
				return nil, invalid("referenced schema must use Draft 2020-12")
			}
			location, err := url.Parse(current.Location)
			if err != nil {
				return nil, invalid("unsupported compiled schema location: %v", err)
			}
			return nil, checkSchemaNode(object, location.Fragment == "")
		},
	})
	compiler.AssertVocabs()
	resources := make(map[string]json.RawMessage, len(d.SchemaResources)+1)
	for uri, raw := range d.SchemaResources {
		resources[uri] = raw
	}
	if _, conflict := resources[d.SchemaBase]; conflict {
		return nil, invalid("schema resource replaces the properties schema retrieval URI")
	}
	resources[d.SchemaBase] = d.PropertiesSchema
	var uris []string
	for uri := range resources {
		uris = append(uris, uri)
	}
	codeUnitSort(uris)
	for _, uri := range uris {
		if err := checkSchemaRetrievalURI(uri); err != nil {
			return nil, err
		}
		canonical, err := graph.CanonicalizeJSON(resources[uri])
		if err != nil {
			return nil, err
		}
		value, err := jsonschema.UnmarshalJSON(bytes.NewReader(canonical))
		if err != nil {
			return nil, err
		}
		if _, object := value.(map[string]any); !object {
			if _, boolean := value.(bool); !boolean {
				return nil, invalid("%s schema must be an object or boolean", uri)
			}
		}
		if err := checkSchemaBaseline(value, true); err != nil {
			return nil, err
		}
		if err := compiler.AddResource(uri, value); err != nil {
			return nil, err
		}
	}
	// Compile every retained resource, including unused ones: malformed extra
	// resources must not hide until a later definition starts referencing them.
	for _, uri := range uris {
		if _, err := compiler.Compile(uri); err != nil {
			return nil, err
		}
	}
	return compiler.Compile(d.SchemaBase)
}

func checkSchemaRetrievalURI(uri string) error {
	if err := graph.ValidateTypeURL(uri); err != nil {
		return err
	}
	// Pinned v6.0.2 roots.addRoot skips meta-schema validation for these
	// exact retrieval prefixes, including nonstandard paths. Never allow an
	// application resource to take that trusted-library path. This restriction
	// is on supplied retrieval keys, not the legitimate standard $schema URI.
	if strings.HasPrefix(uri, "http://json-schema.org/") || strings.HasPrefix(uri, "https://json-schema.org/") {
		return invalid("application schema retrieval URI uses compiler-reserved namespace: %s", uri)
	}
	return nil
}

// Walk schema positions only: const/default/enum/examples and unknown annotation
// objects are data, so their $schema/format keys do not declare constraints.
func checkSchemaBaseline(value any, root bool) error {
	object, ok := value.(map[string]any)
	if !ok {
		return nil // boolean or malformed shape; the meta-schema checks the latter
	}
	if err := checkSchemaNode(object, root); err != nil {
		return err
	}
	// The pinned compiler recognizes legacy definitions as schema positions
	// even in Draft 2020-12, so they cannot bypass baseline admission either.
	for _, keyword := range []string{"properties", "patternProperties", "$defs", "definitions", "dependentSchemas"} {
		if children, ok := object[keyword].(map[string]any); ok {
			for _, child := range children {
				if err := checkSchemaBaseline(child, false); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{"allOf", "anyOf", "oneOf", "prefixItems"} {
		if children, ok := object[keyword].([]any); ok {
			for _, child := range children {
				if err := checkSchemaBaseline(child, false); err != nil {
					return err
				}
			}
		}
	}
	for _, keyword := range []string{"items", "contains", "additionalProperties", "unevaluatedProperties", "unevaluatedItems", "propertyNames", "not", "if", "then", "else", "contentSchema"} {
		if child, present := object[keyword]; present {
			if err := checkSchemaBaseline(child, false); err != nil {
				return err
			}
		}
	}
	return nil
}

func checkSchemaNode(object map[string]any, root bool) error {
	if id, ok := object["$id"].(string); ok && id != "" {
		root = true
	}
	if dialect, present := object["$schema"]; present {
		if !root || dialect != "https://json-schema.org/draft/2020-12/schema" {
			return invalid("$schema is allowed only at a Draft 2020-12 resource root")
		}
	}
	if format, present := object["format"]; present {
		name, text := format.(string)
		if !text || !supportedFormat(name) {
			return invalid("unsupported schema format %v", format)
		}
	}
	return nil
}

// Explicitly bounded to standard formats actually implemented by the pinned
// library. In particular idn-email/idn-hostname are not silently accepted, and
// library-specific extensions are not part of this foundation. Format support
// still requires exact-source qualification before a product conformance claim.
func supportedFormat(name string) bool {
	switch name {
	case "date", "time", "date-time", "duration", "email", "hostname", "ipv4", "ipv6",
		"uri", "iri", "uri-reference", "iri-reference", "uri-template", "uuid",
		"json-pointer", "relative-json-pointer", "regex":
		return true
	}
	return false
}
