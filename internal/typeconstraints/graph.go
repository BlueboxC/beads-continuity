// Package typeconstraints checks declaration-driven restrictions against a
// complete prospective live Link population. It performs no reads or writes.
package typeconstraints

import (
	"fmt"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/typecatalog"
)

// Identity is canonical resource identity, independent of a revision selector.
type Identity struct {
	Scope string
	Path  string
}

// Endpoint retains a selected revision only for pin-policy checks. Counts,
// cycles and self checks always use Identity, even when revisions differ.
type Endpoint struct {
	Identity    Identity
	ResourceURI string // opaque external resource; exclusive with Identity
	Revision    string
}

// Link is one prospective live Link. Deleted and historical versions must not
// be included. The transaction owner establishes endpoint existence, authority,
// category and Type facts separately using the same prospective state.
type Link struct {
	Identity Identity
	Type     typecatalog.Reference
	Source   Endpoint
	Target   Endpoint
}

// Limit distinguishes an absent maximum from a maximum of zero.
type Limit struct {
	Set   bool
	Value uint64
}

// Rule applies to a Link Type family, including every selected refinement.
// Multiple declarations compose by intersection; false adds no restriction.
type Rule struct {
	Family             typecatalog.Reference
	SourceMax          Limit
	TargetMax          Limit
	Acyclic            bool
	Unique             bool
	ForbidIdentitySelf bool
	SameScope          bool
	FloatingSource     bool
	FloatingTarget     bool
}

// Validator holds an immutable selected catalog and copied rule declarations.
// It has no Memory-specific Type names, implicit limits or storage policy.
type Validator struct {
	catalog *typecatalog.Catalog
	rules   []Rule
}

func invalid(format string, args ...any) error {
	return fmt.Errorf("%w: Type graph constraints: %s", graph.ErrValidation, fmt.Sprintf(format, args...))
}

func selectedLink(catalog *typecatalog.Catalog, ref typecatalog.Reference) bool {
	d, ok := catalog.Definition(ref.URI)
	return ok && d.Kind == graph.KindLink && catalog.Conforms(ref, ref)
}

// New refuses unresolved, stale or non-Link family references. The catalog
// already resolves parent closure, including multiple inheritance.
func New(catalog *typecatalog.Catalog, rules []Rule) (*Validator, error) {
	if catalog == nil {
		return nil, invalid("selected catalog is required")
	}
	for _, rule := range rules {
		if !selectedLink(catalog, rule.Family) {
			return nil, invalid("unresolved Link family %s", rule.Family.URI)
		}
	}
	return &Validator{catalog: catalog, rules: append([]Rule(nil), rules...)}, nil
}

func validateIdentity(id Identity, kind graph.ResourceKind) error {
	if err := graph.ValidateScopeURL(id.Scope); err != nil {
		return invalid("invalid Scope: %v", err)
	}
	if err := graph.ValidatePath(id.Path, kind); err != nil {
		return invalid("invalid %s path: %v", kind, err)
	}
	return nil
}

func (e Endpoint) key() string {
	if e.ResourceURI != "" {
		return e.ResourceURI
	}
	return graph.CanonicalURL(e.Identity.Scope, e.Identity.Path)
}

func validateEndpoint(scope string, endpoint Endpoint) error {
	if !utf8.ValidString(endpoint.Revision) {
		return invalid("endpoint revision must be UTF-8")
	}
	if endpoint.ResourceURI == "" {
		return validateIdentity(endpoint.Identity, graph.KindBead)
	}
	if endpoint.Identity != (Identity{}) {
		return invalid("opaque resource cannot also assert Bead identity")
	}
	ref, err := graph.ParsePublicRef(scope, endpoint.ResourceURI, endpoint.Revision)
	if err != nil || ref.InScope() {
		return invalid("opaque resource requires an external absolute URI")
	}
	return nil
}

// Validate checks the entire prospective population. The caller must acquire
// all relevant live Links in the same transaction as the write, overlay every
// proposed change and pass the final state. A partial query cannot establish a
// family maximum or absence of a cycle. This method neither repairs Links nor
// changes its inputs; refusal can therefore precede any publication.
func (v *Validator) Validate(links []Link) error {
	if v == nil || v.catalog == nil {
		return invalid("selected validator is required")
	}
	seen := make(map[Identity]bool, len(links))
	for _, link := range links {
		if err := validateIdentity(link.Identity, graph.KindLink); err != nil {
			return err
		}
		if seen[link.Identity] {
			return invalid("duplicate live Link %s%s", link.Identity.Scope, link.Identity.Path)
		}
		seen[link.Identity] = true
		if !selectedLink(v.catalog, link.Type) {
			return invalid("unresolved declared Link Type %s", link.Type.URI)
		}
		for _, endpoint := range []Endpoint{link.Source, link.Target} {
			if err := validateEndpoint(link.Identity.Scope, endpoint); err != nil {
				return err
			}
		}
	}
	for _, rule := range v.rules {
		if err := v.validateRule(rule, links); err != nil {
			return err
		}
	}
	return nil
}

type pair struct {
	source string
	target string
}

func (v *Validator) validateRule(rule Rule, links []Link) error {
	sources := map[string]uint64{}
	targets := map[string]uint64{}
	pairs := map[pair]bool{}
	adjacent := map[string][]string{}
	indegree := map[string]int{}
	for _, link := range links {
		if !v.catalog.Conforms(link.Type, rule.Family) {
			continue
		}
		source, target := link.Source.key(), link.Target.key()
		if rule.ForbidIdentitySelf && source == target {
			return invalid("%s forbids identity-self Links", rule.Family.URI)
		}
		if rule.SameScope && (link.Source.ResourceURI != "" || link.Target.ResourceURI != "" || link.Source.Identity.Scope != link.Identity.Scope || link.Target.Identity.Scope != link.Identity.Scope) {
			return invalid("%s requires endpoints in the Link Scope", rule.Family.URI)
		}
		if (rule.FloatingSource && link.Source.Revision != "") || (rule.FloatingTarget && link.Target.Revision != "") {
			return invalid("%s requires floating endpoints", rule.Family.URI)
		}
		sources[source]++
		targets[target]++
		if (rule.SourceMax.Set && sources[source] > rule.SourceMax.Value) || (rule.TargetMax.Set && targets[target] > rule.TargetMax.Value) {
			return invalid("%s exceeds a family participation maximum", rule.Family.URI)
		}
		key := pair{source: source, target: target}
		if rule.Unique && pairs[key] {
			return invalid("%s forbids repeated ordered endpoint pairs", rule.Family.URI)
		}
		pairs[key] = true
		if rule.Acyclic {
			adjacent[source] = append(adjacent[source], target)
			if _, ok := indegree[source]; !ok {
				indegree[source] = 0
			}
			indegree[target]++
		}
	}
	if rule.Acyclic && hasCycle(adjacent, indegree) {
		return invalid("%s requires an acyclic family", rule.Family.URI)
	}
	return nil
}

// Kahn's algorithm avoids recursion over caller-sized graphs. Parallel Links
// are separate edges and do not themselves create a cycle.
func hasCycle(adjacent map[string][]string, indegree map[string]int) bool {
	queue := make([]string, 0, len(indegree))
	for id, count := range indegree {
		if count == 0 {
			queue = append(queue, id)
		}
	}
	for i := 0; i < len(queue); i++ {
		for _, next := range adjacent[queue[i]] {
			indegree[next]--
			if indegree[next] == 0 {
				queue = append(queue, next)
			}
		}
	}
	return len(queue) != len(indegree)
}
