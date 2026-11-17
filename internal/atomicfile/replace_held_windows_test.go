//go:build windows

package atomicfile

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

// Contributor Windows replacement regression from #8199.
func TestWriteFile_TargetHeldOpenByAReader(t *testing.T) {
	t.Parallel()
	dir := t.TempDir()
	path := filepath.Join(dir, "held.txt")

	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	reader, err := os.Open(path)
	if err != nil {
		t.Fatal(err)
	}
	closed := make(chan error, 1)
	go func() {
		time.Sleep(100 * time.Millisecond)
		closed <- reader.Close()
	}()

	if err := WriteFile(path, []byte("new"), 0o644); err != nil {
		t.Fatalf("WriteFile while a reader held the target open: %v", err)
	}
	if err := <-closed; err != nil {
		t.Fatal(err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if string(got) != "new" {
		t.Errorf("got %q, want %q", got, "new")
	}
}
