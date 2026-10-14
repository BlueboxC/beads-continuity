package proxy

import (
	"bytes"
	"fmt"
	"log"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func TestProxyLogBoundsRuntimeAndOversizedStartup(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	const limit int64 = 256
	original := strings.Repeat("older\n", 200) + strings.Repeat("tail", 64)
	if err := os.WriteFile(path, []byte(original), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := openProxyLog(path, limit)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	tail, err := os.ReadFile(path + ".1")
	if err != nil {
		t.Fatal(err)
	}
	if string(tail) != original[len(original)-int(limit):] {
		t.Fatal("startup did not retain bounded recent tail")
	}
	logger := log.New(w, "", 0)
	for i := 0; i < 150; i++ {
		if err := logger.Output(1, fmt.Sprintf("event-%03d %s", i, strings.Repeat("x", 40))); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{path, path + ".1"} {
		info, err := os.Stat(name)
		if err != nil {
			t.Fatal(err)
		}
		if info.Size() > limit {
			t.Fatalf("%s grew to %d, cap %d", name, info.Size(), limit)
		}
	}
	current, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(current), "event-149") {
		t.Fatal("latest diagnostic missing after rotation")
	}
	if n, err := w.Write(bytes.Repeat([]byte("z"), 4096)); err != nil || n != 4096 {
		t.Fatalf("oversized record: %d %v", n, err)
	}
	info, err := os.Stat(path)
	if err != nil || info.Size() > limit {
		t.Fatalf("oversized record bypassed cap: %v %v", info, err)
	}
}

func TestProxyLogConcurrentWritersAndRotationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "proxy.log")
	w, err := openProxyLog(path, 256)
	if err != nil {
		t.Fatal(err)
	}
	var group sync.WaitGroup
	for i := 0; i < 8; i++ {
		group.Go(func() {
			for j := 0; j < 40; j++ {
				if _, err := w.Write([]byte("concurrent event\n")); err != nil {
					t.Error(err)
					return
				}
			}
		})
	}
	group.Wait()
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{path, path + ".1"} {
		info, err := os.Stat(name)
		if err != nil || info.Size() > 256 {
			t.Fatalf("bound: %v %v", info, err)
		}
	}
	if _, err := w.Write([]byte("closed")); err == nil {
		t.Fatal("closed writer silently accepted data")
	}
	// A failed archive replacement leaves the active bytes and no temp files.
	path = filepath.Join(t.TempDir(), "proxy.log")
	w, err = openProxyLog(path, 32)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	if _, err := w.Write([]byte(strings.Repeat("a", 32))); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path+".1", 0o700); err != nil {
		t.Fatal(err)
	}
	if _, err := w.Write([]byte("more")); err == nil {
		t.Fatal("rotation failure swallowed")
	}
	got, err := os.ReadFile(path)
	if err != nil || string(got) != strings.Repeat("a", 32) {
		t.Fatalf("failed rotation changed active file: %q %v", got, err)
	}
	files, err := os.ReadDir(filepath.Dir(path))
	if err != nil || len(files) != 2 {
		t.Fatalf("temporary rotation file leaked: %v %v", files, err)
	}
}

func TestProxyLogRefusesSymlink(t *testing.T) {
	root := t.TempDir()
	foreign := filepath.Join(root, "foreign")
	if err := os.WriteFile(foreign, []byte("keep"), 0o600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "proxy.log")
	if err := os.Symlink(foreign, path); err != nil {
		t.Skipf("symlinks unavailable: %v", err)
	}
	if w, err := openProxyLog(path, 1); err == nil {
		_ = w.Close()
		t.Fatal("accepted symlink for truncating log")
	}
	data, err := os.ReadFile(foreign)
	if err != nil || string(data) != "keep" {
		t.Fatalf("foreign file changed: %q %v", data, err)
	}
}
