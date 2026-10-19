//go:build cgo

package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/graphstore"
)

func TestGraphPreviewLargeMetadataRoundTrip(t *testing.T) {
	bd := buildBDUnderTest(t)
	work, home := t.TempDir(), t.TempDir()
	call := func(expected string, args ...string) string {
		t.Helper()
		return graphPatchProcess(t, bd, work, home, nil, expected, 90*time.Second, append(args, "--json")...)
	}
	call("", "init", "--graph-mode", "link", "--scope-url", "https://example.invalid/metadata-capacity/", "--skip-hooks", "--skip-agents", "--non-interactive")
	issue := graphMixedResult[graphstore.IssueRecord](t, call("", "create", "Retained issue", "--id", "retained", "--metadata", `{"retained":true}`))
	data := metadataCapacityDocument((1 << 20) + 128)
	path := filepath.Join(t.TempDir(), "metadata.json")
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	changed := graphMixedResult[graphstore.IssueMutationResult](t, call("", "update", issue.ID, "--metadata", "@"+path, "--if-revision", issue.Revision))
	want := append([]byte(`{"retained":true,`), data[1:]...)
	saved := graphMixedResult[graphstore.IssueRecord](t, call("", "show", issue.ID))
	if !bytes.Equal(saved.Metadata, want) || saved.Revision != changed.Issue.Revision {
		t.Fatal("complete metadata did not survive the CLI/store round trip")
	}
	prior := graphMixedResult[graphstore.IssueRecord](t, call("", "show", issue.ID, "--version", issue.Revision))
	if !bytes.Equal(prior.Metadata, issue.Metadata) {
		t.Fatal("prior metadata version changed")
	}
	call("revision_conflict", "update", issue.ID, "--metadata", "@"+path, "--if-revision", issue.Revision)
	data = metadataCapacityDocument(4 << 20)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatal(err)
	}
	call("", "update", issue.ID, "--metadata", "@"+path, "--if-revision", saved.Revision)
	saved = graphMixedResult[graphstore.IssueRecord](t, call("", "show", issue.ID))
	if !bytes.Equal(saved.Metadata, append([]byte(`{"retained":true,`), data[1:]...)) {
		t.Fatal("metadata at the input limit did not survive the store round trip")
	}
	if err := os.WriteFile(path, metadataCapacityDocument((4<<20)+1), 0o600); err != nil {
		t.Fatal(err)
	}
	call("invalid_properties", "update", issue.ID, "--metadata", "@"+path, "--if-revision", saved.Revision)
	after := graphMixedResult[graphstore.IssueRecord](t, call("", "show", issue.ID))
	if after.Revision != saved.Revision || !bytes.Equal(after.Metadata, saved.Metadata) {
		t.Fatal("refused metadata input changed the issue")
	}
}
