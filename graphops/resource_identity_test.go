package graphops_test

import (
	"strings"
	"testing"

	"github.com/steveyegge/beads/graphops"
)

func TestCanonicalResourceIdentityWithoutKindPrefixes(t *testing.T) {
	const scope = "https://example.invalid/work/"
	for _, id := range []string{"x", "beads/x", "links/x", "types/x", "foo/bar", "snow/%E9%9B%AA", "a%252Fb", "a@b", "Case", strings.Repeat("x/", 2048) + "x"} {
		uri, err := graphops.CanonicalPublicResourceURL(scope, id)
		if err != nil || uri != scope+id {
			t.Errorf("canonical composition changed %q: %q %v", id, uri, err)
		}
		if err := graphops.ValidateTypeURL(uri); err != nil {
			t.Errorf("composition violated canonical HTTP URL law: %q %v", uri, err)
		}
		got, err := graphops.ScopeLocalPublicResourceID(scope, uri)
		if err != nil || got != id {
			t.Errorf("inverse changed %q: %q %v", id, got, err)
		}
		mounted, err := graphops.MountCanonicalResourceID("foo/bar", id)
		if err != nil || mounted != "foo/bar/"+id {
			t.Errorf("explicit root changed source spelling %q: %q %v", id, mounted, err)
		}
		if err := graphops.ValidateCanonicalResourceID(mounted); err != nil {
			t.Errorf("mount violated canonical composition law: %q %v", mounted, err)
		}
		unmounted, err := graphops.MountCanonicalResourceID("", id)
		if err != nil || unmounted != id {
			t.Errorf("omitted root changed %q: %q %v", id, unmounted, err)
		}
	}
	_, outsideErr := graphops.ScopeLocalPublicResourceID(scope, "https://example.invalid/work-other/x")
	wantValidation(t, outsideErr, "different Scope")
	_, emptyErr := graphops.ScopeLocalPublicResourceID(scope, scope)
	wantValidation(t, emptyErr, "bare Scope URL")
}

func TestCanonicalResourceIdentityRetainsSegmentGrammar(t *testing.T) {
	for _, id := range []string{"", ".", "..", "a//b", "\x00", "%00", "a%2Fb", "a%5Cb", "%2E", "%2e", "a?b", "a#b", "/x", "//host/x", "x/", "a\\b", "雪", "%61", "a%", "%FF"} {
		wantValidation(t, graphops.ValidateCanonicalResourceID(id), "noncanonical ID")
		_, composeErr := graphops.CanonicalPublicResourceURL("https://example.invalid/", id)
		wantValidation(t, composeErr, "noncanonical ID composition")
		if id != "" {
			_, mountErr := graphops.MountCanonicalResourceID(id, "valid")
			wantValidation(t, mountErr, "invalid explicit root")
		}
	}
}

func TestCanonicalResourceConstructorsDoNotInferKind(t *testing.T) {
	const scope = "https://example.invalid/work/"
	revision, err := graphops.NewRevision("opaque")
	if err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"rootless", "beads/x", "links/x", "types/x", "alias/x", "a@b", "urn:local-id"} {
		bead, err := graphops.NewPublicBead(graphops.BeadSpec{Path: id, TypeURL: scope + "types/memory", Revision: revision})
		if err != nil || bead.Path() != id {
			t.Fatalf("Bead %q: %+v %v", id, bead, err)
		}
		ref, err := graphops.NewPublicInScopeRef(id, "pinned")
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := graphops.ParsePublicRef(scope, scope+id, "pinned")
		if err != nil || !parsed.Equal(ref) {
			t.Fatalf("canonical reference changed %q: %+v %v", id, parsed, err)
		}
		link, err := graphops.NewPublicLink(graphops.LinkSpec{Path: id, TypeURL: scope + "types/references", Revision: revision, Source: ref, Target: ref})
		if err != nil || link.Path() != id {
			t.Fatalf("Link %q: %+v %v", id, link, err)
		}
	}
}
