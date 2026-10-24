package graphops_test

import (
	"testing"

	"github.com/steveyegge/beads/graphops"
)

func TestPublicResourceMountPreservesLiteralSegments(t *testing.T) {
	const scope = "https://example.invalid/work/"
	for _, root := range []string{"beads", "links", "types", "foo/types/bar"} {
		for _, id := range []string{"x", "beads/x", "links/x", "types/x", "a%20b", "a%252Fb"} {
			mounted, err := graphops.MountCanonicalResourceID(root, id)
			if err != nil || mounted != root+"/"+id {
				t.Errorf("mount changed literal segments root=%q id=%q: %q %v", root, id, mounted, err)
			}
			uri, err := graphops.CanonicalPublicResourceURL(scope, mounted)
			if err != nil || uri != scope+root+"/"+id {
				t.Errorf("URL changed mounted spelling: %q %v", uri, err)
			}
			got, err := graphops.ScopeLocalPublicResourceID(scope, uri)
			if err != nil || got != mounted {
				t.Errorf("inverse changed mount spelling: %q %v", got, err)
			}
		}
	}
}

func TestPublicResourceURLDoesNotEncodeLiteralKeyData(t *testing.T) {
	const scope = "https://example.invalid/work/"
	_, rawErr := graphops.CanonicalPublicResourceURL(scope, "a b")
	wantValidation(t, rawErr, "raw legacy data without selected encoding")
	uri, err := graphops.CanonicalPublicResourceURL(scope, "a%20b")
	if err != nil || uri != scope+"a%20b" {
		t.Errorf("canonical spelling changed: %q %v", uri, err)
	}
	got, err := graphops.ScopeLocalPublicResourceID(scope, uri)
	if err != nil || got != "a%20b" {
		t.Errorf("inverse decoded canonical spelling into a different literal ID: %q %v", got, err)
	}
}

func TestPublicResourceURLValidatesCanonicalScope(t *testing.T) {
	for _, scope := range []string{"https://example.invalid/work", "https://example.invalid/work/../", "https://example.invalid/work/?q=1", "https://user@example.invalid/", "https://example.invalid/%77ork/", "https://example.invalid/work//"} {
		_, scopeErr := graphops.CanonicalPublicResourceURL(scope, "x")
		wantValidation(t, scopeErr, "noncanonical Scope")
		_, inverseErr := graphops.ScopeLocalPublicResourceID(scope, scope+"x")
		wantValidation(t, inverseErr, "inverse with noncanonical Scope")
	}
}
