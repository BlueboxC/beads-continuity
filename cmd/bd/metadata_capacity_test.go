package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func metadataCapacityDocument(size int) []byte {
	return []byte(`{"value":"` + strings.Repeat("x", size-len(`{"value":""}`)) + `"}`)
}

func TestMetadataInputCapacity(t *testing.T) {
	const limit = 4 << 20
	for _, size := range []int{(1 << 20) + 128, limit, limit + 1} {
		data := metadataCapacityDocument(size)
		path := filepath.Join(t.TempDir(), "metadata.json")
		if err := os.WriteFile(path, data, 0o600); err != nil {
			t.Fatal(err)
		}
		for _, input := range []string{string(data), "@" + path} {
			got, err := readMetadataFlag(input)
			if size > limit {
				if err == nil || len(got) != 0 || !strings.Contains(err.Error(), "4194304 bytes") {
					t.Fatalf("oversize input: bytes=%d, error=%v", len(got), err)
				}
			} else if err != nil || !bytes.Equal(got, data) {
				t.Fatalf("input of %d bytes: returned=%d, error=%v", size, len(got), err)
			}
		}
		retained, err := os.ReadFile(path)
		if err != nil || !bytes.Equal(retained, data) {
			t.Fatalf("metadata source changed: %v", err)
		}
	}
}
