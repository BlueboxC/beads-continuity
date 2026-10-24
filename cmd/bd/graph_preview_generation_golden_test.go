//go:build cgo

package main

import (
	"slices"
	"testing"

	"github.com/steveyegge/beads/internal/storage/graphstore"
)

// graphPreviewGoldenTypes maps each marker generation that a fresh workspace can
// be written with to the Type names a fresh init installs for it, in installation
// order. link-preview-v5 has no entry: it is the legacy generation (four
// installed Types, or six in a workspace made after the example Link Types were
// added), no bd writes it any more, and newer binaries require explicit copy conversion.
var graphPreviewGoldenTypes = map[string][]string{
	"link-preview-v13\n": {"memory", "evaluation", "constraint", "policy", "decision", "procedure", "requirement", "scenario", "specification", "references", "superseded-by", "requires", "defines", "subject-to", "conformance", "addresses", "satisfies", "violates", "issue", "dependency", "native-issue/627567", "native-issue/66656174757265", "native-issue/7461736b", "native-issue/65706963", "native-issue/63686f7265", "native-issue/6465636973696f6e", "native-issue/6d657373616765", "native-issue/6d6f6c6563756c65", "native-issue/67617465", "native-issue/7370696b65", "native-issue/73746f7279", "native-issue/6d696c6573746f6e65", "native-issue/6576656e74", "native-dependency/626c6f636b73", "native-dependency/706172656e742d6368696c64", "native-dependency/636f6e646974696f6e616c2d626c6f636b73", "native-dependency/77616974732d666f72", "native-dependency/72656c61746564", "native-dependency/646973636f76657265642d66726f6d", "native-dependency/7265706c6965732d746f", "native-dependency/72656c617465732d746f", "native-dependency/6475706c696361746573", "native-dependency/73757065727365646573", "native-dependency/617574686f7265642d6279", "native-dependency/61737369676e65642d746f", "native-dependency/617070726f7665642d6279", "native-dependency/61747465737473", "native-dependency/747261636b73", "native-dependency/756e74696c", "native-dependency/6361757365642d6279", "native-dependency/76616c696461746573", "native-dependency/64656c6567617465642d66726f6d", "preview-related-v2", "example-follows", "example-cites"},
	"link-preview-v6\n":  {"memory", "issue", "dependency", "related", "example-follows", "example-cites"},
	"link-preview-v7\n":  {"memory", "issue", "dependency", "related", "example-follows", "example-cites", "parent-child", "discovered-from", "native-related"},
}

// An older bd checks only the marker (and a schema version that names the
// storage layout, which adding a Type does not change), so the marker is the one
// signal that a workspace holds a Type set the reader does not know. Changing
// what a fresh init installs without changing the marker lets an older bd open
// the workspace and fail later, at its first list or inventory read.
func TestGraphPreviewGenerationPinsFreshInstallTypes(t *testing.T) {
	golden, ok := graphPreviewGoldenTypes[graphPreviewGeneration]
	if !ok {
		t.Fatalf("graphPreviewGeneration is %q but graphPreviewGoldenTypes has no entry for it. "+
			"A new generation needs all three of: the new generation named in graphPreviewGeneration, "+
			"an explicit decision about older-generation admission/conversion, "+
			"and a golden entry here listing the Types it installs", graphPreviewGeneration)
	}
	if got := graphstore.FreshTypeNames(); !slices.Equal(got, golden) {
		t.Fatalf("a fresh init now installs %v, but generation %q pins %v. "+
			"Adding, removing or reordering an installed Type is a workspace format change: "+
			"bump graphPreviewGeneration to the next number, add that generation to the accepted set "+
			"(graphPreviewSupportedGenerations) only for compatible readers, and add a golden entry for it "+
			"here. Do not edit this entry in place", got, graphPreviewGeneration, golden)
	}
	if !graphPreviewGenerationSupported([]byte(graphPreviewGeneration)) {
		t.Fatalf("the generation a fresh init writes (%q) is not in the accepted set, so every new "+
			"workspace would be refused at its first command: add it to graphPreviewSupportedGenerations",
			graphPreviewGeneration)
	}
}
