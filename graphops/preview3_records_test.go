package graphops_test

import (
	"errors"
	"github.com/steveyegge/beads/graphops"
	"testing"
)

func TestPreview3Ref(t *testing.T) {
	const scope = "https://beads.example/acme/"
	in, err := graphops.NewPublicInScopeRef("beads/task-42", "")
	if err != nil {
		t.Fatal(err)
	}
	if !in.InScope() || in.Path() != "beads/task-42" || in.URI() != "" || in.Pinned() || in.Pin() != "" || in.IsZero() ||
		in.URL(scope) != "https://beads.example/acme/beads/task-42" {
		t.Fatalf("in-Scope ref: %+v", in)
	}
	if ref, err := graphops.NewPublicInScopeRef("links/x", ""); err != nil || ref.Path() != "links/x" {
		t.Fatalf("endpoint spelling cannot establish kind: %+v %v", ref, err)
	}
	if _, err := graphops.NewPublicInScopeRef("beads/caf%c3%a9", ""); !errors.Is(err, graphops.ErrValidation) {
		t.Fatalf("a noncanonical path is refused, not trimmed: %v", err)
	}
	if !(graphops.Ref{}).IsZero() {
		t.Fatal("zero Ref is absent")
	}

	for _, tc := range []struct {
		ref     string
		inScope bool
		path    string
		uri     string
		reject  bool
	}{
		{"beads/task-42", true, "beads/task-42", "", false},
		{"https://beads.example/acme/beads/task-42", true, "beads/task-42", "", false},
		{"https://beads.example/acme/beads/Task-42", true, "beads/Task-42", "", false},
		{"https://beads.example/acme/beads/a/b/c", true, "beads/a/b/c", "", false},
		// Aliases of the Scope URL claim the Scope and must be canonical.
		{"https://BEADS.example/acme/beads/task-42", false, "", "", true},
		{"HTTPS://beads.example/acme/beads/task-42", false, "", "", true},
		{"https://beads.example:443/acme/beads/task-42", false, "", "", true},
		{"https://beads.example/acme/./beads/task-42", false, "", "", true},
		{"https://beads.example/x/../acme/beads/task-42", false, "", "", true},
		{"https://beads.example/acme/beads/task%2D42", false, "", "", true},
		{"https://beads.example/acme/beads/caf%c3%a9", false, "", "", true},
		{"https://beads.example/acme/beads/task-42?x=1", false, "", "", true},
		{"https://beads.example/acme/beads/task-42#f", false, "", "", true},
		// In-Scope public identity is independent of kind-root spelling.
		{"https://beads.example/acme/links/l1", true, "links/l1", "", false},
		{"https://beads.example/acme/alias/x", true, "alias/x", "", false},
		{"https://beads.example/acme/", false, "", "", true},
		{"https://beads.example/acme/other/x", true, "other/x", "", false},
		{"https://beads.example/acme/beads/", false, "", "", true},
		// Local public identity spelling does not establish kind.
		{"links/l1", true, "links/l1", "", false},
		{"alias/x", true, "alias/x", "", false},
		{"beads/x?y", false, "", "", true},
		{"beads/", false, "", "", true},
		{"", false, "", "", true},
		{"not a uri", false, "", "", true},
		{"//beads.example/acme/beads/x", false, "", "", true},
		{"../beads/x", false, "", "", true},
		{"https://beads.example/acme/beads/a b", false, "", "", true},
		{"urn:isbn:0451450523 x", false, "", "", true},
		{"ht_tp://x", false, "", "", true},
		{"1abc:xyz", true, "1abc:xyz", "", false},
		{"urn:x%2", false, "", "", true},
		{"urn:x%zz", false, "", "", true},
		// Aliases through an empty port and through dot segments at the end.
		{"https://beads.example:/acme/beads/x", false, "", "", true},
		{"https://beads.example/acme/beads/x/.", false, "", "", true},
		{"https://beads.example/acme/beads/x/..", false, "", "", true},
		{"https://beads.example/../acme/beads/x", false, "", "", true},
		// External: preserved byte-identically.
		{"https://github.example/issues/123", false, "", "https://github.example/issues/123", false},
		{"https://beads.example/acmeX/beads/x", false, "", "https://beads.example/acmeX/beads/x", false},
		{"https://beads.example/acme", false, "", "https://beads.example/acme", false},
		{"https://beads.example:8443/acme/beads/x", false, "", "https://beads.example:8443/acme/beads/x", false},
		{"http://beads.example/acme/beads/x", false, "", "http://beads.example/acme/beads/x", false},
		{"https://other.example/acme/beads/x", false, "", "https://other.example/acme/beads/x", false},
		{"urn:isbn:0451450523", false, "", "urn:isbn:0451450523", false},
		{"mailto:a@b.example", false, "", "mailto:a@b.example", false},
		{"HTTPS://Other.Example/X", false, "", "HTTPS://Other.Example/X", false},
		{"https://[::1]/beads/x", false, "", "https://[::1]/beads/x", false},
		{"https://beads.example", false, "", "https://beads.example", false},
		{"https://[zzz]/x", false, "", "https://[zzz]/x", false},
		// Aliases of the Scope's ORIGIN under the WHATWG parser: a zero-padded
		// default port, a percent-encoded host character, mixed case in the
		// escape and the host. Each claims the Scope and is not canonical.
		{"https://beads.example:0443/acme/beads/x", false, "", "", true},
		{"https://beads.example:00443/acme/beads/x", false, "", "", true},
		{"https://beads%2Eexample/acme/beads/x", false, "", "", true},
		{"https://beads%2eexample/acme/beads/x", false, "", "", true},
		{"https://%42eads.example/acme/beads/x", false, "", "", true},
		// Not aliases: a trailing-dot host is a different host to the parser
		// (kept as written, so the external spelling survives byte for byte);
		// a host or port the parser cannot parse claims nothing.
		{"https://beads.example./acme/beads/x", false, "", "https://beads.example./acme/beads/x", false},
		{"https://BEADS.example./acme/beads/x", false, "", "https://BEADS.example./acme/beads/x", false},
		{"https://beads.example:99999/acme/beads/x", false, "", "https://beads.example:99999/acme/beads/x", false},
		{"https://beads.example:4x3/acme/beads/x", false, "", "https://beads.example:4x3/acme/beads/x", false},
		{"https://beads%2fexample/acme/beads/x", false, "", "https://beads%2fexample/acme/beads/x", false},
		{"https://beads.example:8443/acme/beads/x", false, "", "https://beads.example:8443/acme/beads/x", false},
		{"https://beads.123/acme/beads/x", false, "", "https://beads.123/acme/beads/x", false},
		{"https://beads.example%3A443/acme/beads/x", false, "", "https://beads.example%3A443/acme/beads/x", false},
	} {
		got, err := graphops.ParsePublicRef(scope, tc.ref, "")
		if tc.reject {
			if !errors.Is(err, graphops.ErrValidation) {
				t.Errorf("ParsePublicRef(%q): want ErrValidation, got %v (%+v)", tc.ref, err, got)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParsePublicRef(%q): %v", tc.ref, err)
			continue
		}
		if got.InScope() != tc.inScope || got.Path() != tc.path || got.URI() != tc.uri {
			t.Errorf("ParsePublicRef(%q) = inScope %v path %q uri %q", tc.ref, got.InScope(), got.Path(), got.URI())
		}
		if got.URL(scope) != tc.uri && got.URL(scope) != scope+tc.path {
			t.Errorf("ParsePublicRef(%q).URL = %q", tc.ref, got.URL(scope))
		}
	}
	// Aliases of numeric Scope hosts: every IPv4 spelling the parser
	// rewrites to dotted decimal, and every IPv6 spelling it compresses,
	// claims the Scope; only the serializer's spelling is the Bead's.
	for _, tc := range []struct{ scope, ref string }{
		{"https://[::1]:8443/s/", "https://[0:0:0:0:0:0:0:1]:8443/s/beads/x"},
		{"https://[::1]:8443/s/", "https://[::0001]:8443/s/beads/x"},
		{"https://[::1]:8443/s/", "https://[::1]:08443/s/beads/x"},
		{"https://[::ffff:102:304]/s/", "https://[::ffff:1.2.3.4]/s/beads/x"},
		{"https://[::ffff:102:304]/s/", "https://[::FFFF:102:304]/s/beads/x"},
		{"https://127.0.0.1/s/", "https://0x7f000001/s/beads/x"},
		{"https://127.0.0.1/s/", "https://0X7F000001/s/beads/x"},
		{"https://127.0.0.1/s/", "https://127.1/s/beads/x"},
		{"https://127.0.0.1/s/", "https://2130706433/s/beads/x"},
		{"https://127.0.0.1/s/", "https://0177.0.0.1/s/beads/x"},
		{"https://127.0.0.1/s/", "https://0x7f.0.0.1/s/beads/x"},
		{"https://127.0.0.1/s/", "https://127.0.0.1./s/beads/x"},
		{"https://127.0.0.1/s/", "https://127.0.0.1:443/s/beads/x"},
		{"https://127.0.0.1/s/", "https://127.0.0.1:0443/s/beads/x"},
		{"https://beads.example./s/", "https://BEADS.EXAMPLE./s/beads/x"},
		{"https://beads.example./s/", "https://beads%2Eexample./s/beads/x"},
	} {
		got, err := graphops.ParsePublicRef(tc.scope, tc.ref, "")
		if !errors.Is(err, graphops.ErrValidation) {
			t.Errorf("ParsePublicRef(%q, %q): an alias of the Scope origin must be refused as noncanonical, got %v %+v", tc.scope, tc.ref, err, got)
		}
	}
	for _, tc := range []struct{ scope, ref string }{
		{"https://[::1]:8443/s/", "https://[::1]:8443/s/beads/x"},
		{"https://[::ffff:102:304]/s/", "https://[::ffff:102:304]/s/beads/x"},
		{"https://127.0.0.1/s/", "https://127.0.0.1/s/beads/x"},
		{"https://beads.example./s/", "https://beads.example./s/beads/x"},
	} {
		got, err := graphops.ParsePublicRef(tc.scope, tc.ref, "")
		if err != nil || !got.InScope() || got.Path() != "beads/x" {
			t.Errorf("ParsePublicRef(%q, %q): canonical in-Scope reference refused: %v %+v", tc.scope, tc.ref, err, got)
		}
	}
	// The undotted host is external to a trailing-dot Scope, and vice versa.
	if got, err := graphops.ParsePublicRef("https://beads.example./s/", "https://beads.example/s/beads/x", ""); err != nil || got.InScope() {
		t.Errorf("undotted host against a trailing-dot Scope: %v %+v", err, got)
	}
	// An invalid Scope is the caller's error.
	if _, err := graphops.ParsePublicRef("https://beads.example/acme", "beads/x", ""); !errors.Is(err, graphops.ErrValidation) {
		t.Fatalf("invalid scope: %v", err)
	}
	// Pins are provenance, not identity.
	pinned, _ := graphops.ParsePublicRef(scope, "beads/x", "rev1")
	unpinned, _ := graphops.ParsePublicRef(scope, "https://beads.example/acme/beads/x", "")
	if !pinned.Pinned() || pinned.Pin() != "rev1" || !pinned.SameURI(unpinned) || pinned.Equal(unpinned) || !pinned.Equal(pinned) {
		t.Fatal("pins must be ignored by SameURI and honored by Equal")
	}
	ext, _ := graphops.ParsePublicRef(scope, "https://github.example/issues/123", "8f0e2b")
	if !ext.Pinned() || ext.Pin() != "8f0e2b" || ext.SameURI(pinned) || ext.URL(scope) != "https://github.example/issues/123" {
		t.Fatal("external pin must be carried byte-identically")
	}
}

func TestPreview3BeadAndLink(t *testing.T) {
	const scope = "https://beads.example/acme/"
	rev := graphops.MintRevision()
	attr, _ := graphops.NewAttribution("agent:planner", graphops.AttributionWriterSupplied)
	props, _ := graphops.NewProperties([]byte(`{"title":"Specify BDP mutation","status":"open"}`))
	bead, err := graphops.NewPublicBead(graphops.BeadSpec{Path: "beads/task-42", TypeURL: "https://work.example/types/task", Revision: rev, Attribution: attr, Properties: props})
	if err != nil {
		t.Fatal(err)
	}
	gotAttr, present := bead.Attribution()
	if bead.Path() != "beads/task-42" || bead.TypeURL() != "https://work.example/types/task" || !bead.Revision().Equal(rev) ||
		!present || gotAttr != attr || !bead.Properties().Equal(props) || bead.URL(scope) != scope+"beads/task-42" || bead.IsZero() {
		t.Fatalf("bead accessors: %+v", bead)
	}
	plain, _ := graphops.NewPublicBead(graphops.BeadSpec{Path: "beads/x", TypeURL: "https://work.example/types/task", Revision: rev})
	if _, present := plain.Attribution(); present || !plain.Properties().IsEmpty() {
		t.Fatal("absent attribution and empty properties are the defaults")
	}
	if !(graphops.Bead{}).IsZero() {
		t.Fatal("zero Bead")
	}
	for name, spec := range map[string]graphops.BeadSpec{
		"bad path":      {Path: "beads/x/", TypeURL: "https://work.example/types/task", Revision: rev},
		"bad type":      {Path: "beads/x", TypeURL: "https://Work.example/types/task", Revision: rev},
		"zero revision": {Path: "beads/x", TypeURL: "https://work.example/types/task"},
	} {
		if _, err := graphops.NewPublicBead(spec); !errors.Is(err, graphops.ErrValidation) {
			t.Errorf("bead %s: want ErrValidation, got %v", name, err)
		}
	}

	source, _ := graphops.NewPublicInScopeRef("beads/task-42", "")
	target, _ := graphops.ParsePublicRef(scope, "https://beads.example/acme/beads/person-7", "")
	external, _ := graphops.ParsePublicRef(scope, "https://github.example/issues/123", "8f0e2b")
	link, err := graphops.NewPublicLink(graphops.LinkSpec{Path: "links/assigned-to-81", TypeURL: "https://work.example/types/assigned-to", Revision: rev, Source: source, Target: target})
	if err != nil {
		t.Fatal(err)
	}
	if link.Path() != "links/assigned-to-81" || link.TypeURL() != "https://work.example/types/assigned-to" || !link.Revision().Equal(rev) ||
		!link.Source().SameURI(source) || !link.Target().SameURI(target) || link.URL(scope) != scope+"links/assigned-to-81" || link.IsZero() {
		t.Fatalf("link accessors: %+v", link)
	}
	if _, present := link.Attribution(); present || !link.Properties().IsEmpty() {
		t.Fatal("absent attribution and empty properties are the defaults")
	}
	ext, err := graphops.NewPublicLink(graphops.LinkSpec{Path: "links/cites/1", TypeURL: "https://work.example/types/cites", Revision: rev, Source: source, Target: external, Attribution: attr, Properties: props})
	if err != nil || !ext.Target().Pinned() || ext.Target().URI() != "https://github.example/issues/123" {
		t.Fatalf("external target: %v", err)
	}
	if a, present := ext.Attribution(); !present || a != attr || !ext.Properties().Equal(props) {
		t.Fatal("link attribution and properties")
	}
	if !(graphops.Link{}).IsZero() {
		t.Fatal("zero Link")
	}
	// The endpoints are symmetric: an external source with an in-Scope target
	// is a Link this Scope owns.
	extSource, err := graphops.NewPublicLink(graphops.LinkSpec{Path: "links/x", TypeURL: "https://work.example/types/cites", Revision: rev, Source: external, Target: target})
	if err != nil || extSource.Source().InScope() || extSource.Source().URI() != "https://github.example/issues/123" || !extSource.Target().InScope() {
		t.Fatalf("external source with an in-Scope target: %v %+v", err, extSource)
	}
	for name, spec := range map[string]graphops.LinkSpec{
		"bad path":      {Path: "x//y", TypeURL: "https://work.example/types/cites", Revision: rev, Source: source, Target: target},
		"bad type":      {Path: "links/x", TypeURL: "work.example/types/cites", Revision: rev, Source: source, Target: target},
		"zero revision": {Path: "links/x", TypeURL: "https://work.example/types/cites", Source: source, Target: target},
		"both external": {Path: "links/x", TypeURL: "https://work.example/types/cites", Revision: rev, Source: external, Target: external},
		"zero source":   {Path: "links/x", TypeURL: "https://work.example/types/cites", Revision: rev, Target: target},
		"zero target":   {Path: "links/x", TypeURL: "https://work.example/types/cites", Revision: rev, Source: source},
		"both zero":     {Path: "links/x", TypeURL: "https://work.example/types/cites", Revision: rev},
	} {
		if _, err := graphops.NewPublicLink(spec); !errors.Is(err, graphops.ErrValidation) {
			t.Errorf("link %s: want ErrValidation, got %v", name, err)
		}
	}
}
