package main

import (
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/memoryops"
)

type primeSnapshotStore struct {
	storage.DoltStorage
	memories memoryops.Memories
}

func (s primeSnapshotStore) Memories() (memoryops.Memories, error) { return s.memories, nil }

func TestPrimeSnapshotPreservesContextWithoutReadingCodeBlobs(t *testing.T) {
	oldStore, oldGraph, oldProxy := store, graphPreviewActive, proxiedServerMode
	t.Cleanup(func() { store, graphPreviewActive, proxiedServerMode = oldStore, oldGraph, oldProxy })
	graphPreviewActive, proxiedServerMode = false, false
	for _, manifest := range []bool{true, false} {
		m := &indexedMemoryProbe{maintenanceMemories: maintenanceMemories{rows: map[string]string{"objective": "keep solved module", "@knowledge/code/blob/retained": "retained old AST", "@knowledge/code/blob/current": "current AST"}}}
		if manifest {
			if _, err := codeindex.Save(t.Context(), m, m.rows, codeindex.Index{Version: 3, Languages: []string{"go"}}); err != nil {
				t.Fatal(err)
			}
		}
		before := make(map[string]string)
		for k, v := range m.rows {
			before[k] = v
		}
		want := renderPrimeMemoryPlane(before, false)
		m.writes = 0
		store = primeSnapshotStore{memories: m}
		got := formatMemoriesForPrime(false)
		if got != want {
			t.Fatal("prime context changed")
		}
		wantLists := 1
		if !manifest {
			wantLists = 2
		}
		if m.snapshots != 1 || len(m.lists) != wantLists || m.lists[0].ExcludeKeyPrefix != codeindex.Prefix {
			t.Fatal("prime still enumerates retained code generations")
		}
		if m.writes != 0 || !reflect.DeepEqual(m.rows, before) {
			t.Fatal("prime altered retained memories")
		}
	}
}

func TestPrimeSnapshotFailureWithholdsContext(t *testing.T) {
	oldStore, oldGraph, oldProxy, oldRequire, oldError := store, graphPreviewActive, proxiedServerMode, primeRequireMemoryLoad, primeMemoryLoadError
	t.Cleanup(func() {
		store, graphPreviewActive, proxiedServerMode, primeRequireMemoryLoad, primeMemoryLoadError = oldStore, oldGraph, oldProxy, oldRequire, oldError
	})
	graphPreviewActive, proxiedServerMode, primeRequireMemoryLoad, primeMemoryLoadError = false, false, true, nil
	failure := errors.New("snapshot failed")
	m := &indexedMemoryProbe{maintenanceMemories: maintenanceMemories{rows: map[string]string{"objective": "private solved module"}}, snapshotError: failure}
	store = primeSnapshotStore{memories: m}
	got := formatMemoriesForPrime(false)
	if !errors.Is(primeMemoryLoadError, failure) || strings.Contains(got, "private solved module") {
		t.Fatal("snapshot failure exposed partial context")
	}
}
