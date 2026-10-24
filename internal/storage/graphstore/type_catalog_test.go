package graphstore

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memorycatalog"
	"github.com/steveyegge/beads/internal/storage"
)

func TestSelectedTypeInventoryMatchesCompleteCatalog(t *testing.T) {
	const scope = "https://example.test/selected/"
	catalog, err := newSelectedTypeCatalog(scope)
	if err != nil {
		t.Fatal(err)
	}
	byID := map[string]graph.TypeDescriptor{}
	for _, definition := range previewTypeDefinitions() {
		descriptor, err := definition.build(scope)
		if err != nil {
			t.Fatalf("%s: %v", definition.name, err)
		}
		if _, duplicate := byID[descriptor.ID()]; duplicate {
			t.Fatalf("duplicate installed identity %s", descriptor.ID())
		}
		if (strings.HasPrefix(definition.name, "example-") || strings.HasPrefix(definition.name, "preview-")) && !schema8Informational(scope, scope+"types/"+definition.name) {
			t.Fatalf("obsolete selected Type: %s", definition.name)
		}
		if definition.name == "related" {
			t.Fatal("legacy Related spelling installed a separate Type")
		}
		byID[descriptor.ID()] = descriptor
	}
	if TypeGeneration != 13 || len(byID) != 55 {
		t.Fatalf("generation%d installed%d Types, want generation13 ordered catalog55", TypeGeneration, len(byID))
	}
	for _, family := range []struct {
		path, base string
		kind       graph.ResourceKind
		names      []string
	}{
		{"native-issue", IssueTypeURL(scope), graph.KindBead, []string{"bug", "feature", "task", "epic", "chore", "decision", "message", "molecule", "gate", "spike", "story", "milestone", "event"}},
		{"native-dependency", DependencyTypeURL(scope), graph.KindLink, []string{"blocks", "parent-child", "conditional-blocks", "waits-for", "related", "discovered-from", "replies-to", "relates-to", "duplicates", "supersedes", "authored-by", "assigned-to", "approved-by", "attests", "tracks", "until", "caused-by", "validates", "delegated-from"}},
	} {
		base, present := catalog.types.Definition(family.base)
		if !present || !base.Abstract || base.Kind != family.kind {
			t.Fatalf("native family lost abstract kind: %+v", base)
		}
		for _, name := range family.names {
			id := scope + "types/" + family.path + "/" + hex.EncodeToString([]byte(name))
			descriptor, present := byID[id]
			expected, err := nativeConcreteDescriptor(scope, id)
			if !present || err != nil || descriptor.Name() != name || descriptor.Describes() != family.kind || !bytes.Equal(descriptor.CanonicalJSON(), expected.CanonicalJSON()) {
				t.Fatalf("native descriptor mismatch for %q: %s %v", id, descriptor.CanonicalJSON(), err)
			}
			parents := descriptor.ConformsTo()
			if len(parents) != 1 || parents[0] != family.base {
				t.Fatalf("native %q lost exact family: %v", id, parents)
			}
		}
	}
	for _, declaration := range catalog.projection.Definitions() {
		descriptor, present := byID[declaration.Binding.Type.URI]
		if !present || descriptor.Describes() != declaration.Kind {
			t.Fatalf("missing or wrong category for %s", declaration.Symbol)
		}
		parents := descriptor.ConformsTo()
		if len(parents) != len(declaration.Parents) {
			t.Fatalf("%s parent count differs", declaration.Symbol)
		}
		for _, parent := range declaration.Parents {
			found := false
			for _, id := range parents {
				found = found || id == parent.URI
			}
			if !found {
				t.Fatalf("%s missing parent %s", declaration.Symbol, parent.URI)
			}
		}
		if declaration.Kind == graph.KindLink && !IsInformationalTypeURL(scope, descriptor.ID()) {
			t.Fatalf("selected Link %s is unreachable by generic Link adapter", declaration.Symbol)
		}
		if declaration.Kind == graph.KindBead && len(descriptor.OwnsOutgoing()) != 0 {
			t.Fatalf("%s inventory invented finite ownership limits", declaration.Symbol)
		}
		if declaration.Abstract {
			description, _ := descriptor.Description()
			if !strings.Contains(description, "Abstract") {
				t.Fatalf("abstract %s is not identified in partial details", declaration.Symbol)
			}
		}
	}
	conformance, ok := catalog.projection.Definition(memorycatalog.Conformance)
	if !ok || !conformance.Abstract {
		t.Fatal("Conformance must be present and abstract")
	}
	if _, present := byID[conformance.Binding.Type.URI]; !present {
		t.Fatal("abstract Conformance must remain visible in inventory")
	}
	for _, name := range []string{"type", "bead-type", "link-type", "conforms-to", "refines", "Type", "BeadType", "LinkType", "ConformsTo", "Refines", "preview-memory", "preview-issue", "preview-dependency", "preview-related"} {
		if _, present := byID[scope+"types/"+name]; present {
			t.Fatalf("intrinsic/obsolete %s leaked into external catalog", name)
		}
	}
	if RelatedTypeURL(scope) != scope+"types/references" || IsInformationalTypeURL(scope, DependencyTypeURL(scope)) {
		t.Fatal("legacy Related alias or native Dependency separation differs")
	}
}

func TestSelectedReferencePropertiesUseSharedURISyntax(t *testing.T) {
	const scope = "https://example.test/selected/"
	catalog, err := newSelectedTypeCatalog(scope)
	if err != nil {
		t.Fatal(err)
	}
	for _, authority := range []any{"urn:example:☃", "https://example.test/a|b", map[string]string{"uri": "urn:example:☃", "revision": "r1"}} {
		raw, err := json.Marshal(map[string]any{"title": "Policy", "body": "", "authority": authority})
		if err != nil {
			t.Fatal(err)
		}
		if err := catalog.validateProperties(scope+"types/policy", raw, graph.KindBead); !errors.Is(err, storage.ErrValidation) {
			t.Fatalf("invalid authority admitted: %v", err)
		}
	}
}
