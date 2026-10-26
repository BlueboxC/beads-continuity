package fix

import (
	"io"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

func TestForkStubCleanupKeepsProjectMaterial(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "crew", "alice", ".beads")
	kept := map[string]string{
		"redirect": "../../missing/.beads\n", "README.md": "edited README",
		"config.yaml": "issue-prefix: mine\n", ".gitignore": "private\n",
		"formulas/release.toml": "formula = 'release'\n", "notes/custom.yaml": "mine",
		"interactions.jsonl": "audit", "routes.jsonl": "routes", ".local_version": "1.3.1\n",
		"last-touched": "mine-1", "bd.sock/notes": "not a socket",
	}
	writeBeadsTree(t, dir, kept)
	want := snapshotBeadsTree(t, dir)
	writeBeadsTree(t, dir, map[string]string{"daemon.pid": "1", "daemon.log": "leftover"})
	if err := ClassicArtifacts(root, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got := snapshotBeadsTree(t, dir); !reflect.DeepEqual(got, want) {
		t.Fatalf("cleanup changed project material: got %q, want %q", got, want)
	}
}

func TestForkStubCleanupKeepsSymlinkNamedLikeRuntime(t *testing.T) {
	root := t.TempDir()
	dir := filepath.Join(root, "polecats", "p1", ".beads")
	writeBeadsTree(t, dir, map[string]string{"redirect": "../../missing/.beads\n"})
	target := filepath.Join(root, "private")
	if err := os.WriteFile(target, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, filepath.Join(dir, "daemon.pid")); err != nil {
		t.Skipf("symlink unavailable: %v", err)
	}
	if err := ClassicArtifacts(root, io.Discard); err != nil {
		t.Fatal(err)
	}
	if got, err := os.Readlink(filepath.Join(dir, "daemon.pid")); err != nil || got != target {
		t.Fatalf("symlink changed: %q, %v", got, err)
	}
}

func TestForkStubVersionWitnessNeedsFollowedRedirectAndNoConfig(t *testing.T) {
	for _, tc := range []struct {
		name, config                 string
		redirect, target, wantRemove bool
	}{
		{name: "followed redirect", redirect: true, target: true, wantRemove: true},
		{name: "missing target", redirect: true},
		{name: "no redirect", target: true},
		{name: "own YAML", redirect: true, target: true, config: "config.yaml"},
		{name: "own metadata", redirect: true, target: true, config: "metadata.json"},
		{name: "own legacy metadata", redirect: true, target: true, config: "config.json"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			dir := filepath.Join(root, "crew", "alice", ".beads")
			files := map[string]string{".local_version": "0.62.0", "last-touched": "mine-1"}
			if tc.redirect {
				files["redirect"] = "../../mayor/rig/.beads\n"
			}
			if tc.config != "" {
				files[tc.config] = "owned configuration"
			}
			writeBeadsTree(t, dir, files)
			if tc.target {
				writeBeadsTree(t, filepath.Join(root, "mayor", "rig", ".beads"), map[string]string{"metadata.json": `{"backend":"dolt"}`})
			}
			remove, _, err := PlanRedirectStub(dir)
			if err != nil {
				t.Fatal(err)
			}
			if got := len(remove) == 2; got != tc.wantRemove {
				t.Fatalf("version/last-touched removal = %v, want %v: %v", got, tc.wantRemove, remove)
			}
		})
	}
}
