package main

import (
	"context"
	"errors"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/internal/knowledge"
	"github.com/steveyegge/beads/internal/memoryapi"
	"github.com/steveyegge/beads/memoryops"
	"reflect"
	"testing"
)

type indexedMemoryProbe struct {
	maintenanceMemories
	lists         []memoryops.ListRequest
	snapshots     int
	snapshotError error
}

func (m *indexedMemoryProbe) ReadSnapshot(_ context.Context, fn func(memoryops.MemoryReader) error) error {
	m.snapshots++
	if err := fn(m); err != nil {
		return err
	}
	return m.snapshotError
}

func (m *indexedMemoryProbe) List(_ context.Context, req memoryops.ListRequest) (memoryops.ListResult, error) {
	m.lists = append(m.lists, req)
	return memoryops.ListResult{Memories: memoryapi.SelectMemories(m.rows, req)}, nil
}

func TestIndexedMemoriesExcludeRetainedCodeAndPreserveHumanPlane(t *testing.T) {
	ctx := t.Context()
	m := &indexedMemoryProbe{maintenanceMemories: maintenanceMemories{rows: map[string]string{"plain": "preserve objective", "@activity/observed": "preserve observed event"}}}
	index := codeindex.Index{Version: 3, Languages: []string{"go"}, Roots: []string{"src"}, Files: []codeindex.File{{Path: "src/a.go", Language: "go", SHA256: "old", Symbols: []codeindex.Symbol{{ID: "src/a.go::Solved", Name: "Solved", Kind: "function"}}}}}
	if _, err := codeindex.Save(ctx, m, m.rows, index); err != nil {
		t.Fatal(err)
	}
	if err := knowledge.SaveRecord(ctx, m, knowledge.Record{ID: "solved", Kind: "solution", Summary: "Preserve prior solution", Scope: "tested", Evidence: "historical evidence", Sources: []knowledge.Source{{Path: "src/a.go", SHA256: "old"}}}); err != nil {
		t.Fatal(err)
	}
	m.rows[codeindex.Prefix+"blob/unused-old"] = "corrupt unused old generation"
	before := map[string]string{}
	for k, v := range m.rows {
		before[k] = v
	}
	m.writes = 0
	expected, err := codeindex.Load(m.rows)
	if err != nil {
		t.Fatal(err)
	}
	plane, got, err := readIndexedMemories(ctx, m)
	if err != nil {
		t.Fatal(err)
	}
	if len(m.lists) != 1 || m.lists[0].ExcludeKeyPrefix != codeindex.Prefix {
		t.Fatal("project reads still enumerate retained code generations")
	}
	if _, ok := plane.Memories[codeindex.Prefix+"blob/unused-old"]; ok {
		t.Fatal("unused retained blob entered snapshot")
	}
	if !reflect.DeepEqual(got, expected) {
		t.Fatal("current index lost files, symbols or provenance")
	}
	for _, key := range []string{"plain", "@activity/observed", "@knowledge/record/solved"} {
		if plane.Memories[key] != before[key] {
			t.Fatal("human/history plane changed", key)
		}
	}
	if m.writes != 0 || !reflect.DeepEqual(before, m.rows) {
		t.Fatal("snapshot changed stored rows")
	}
	loaded, err := codeindex.Load(plane.Memories)
	if err != nil || !reflect.DeepEqual(got, loaded) {
		t.Fatal("snapshot lacks current generation for guarded publication", err)
	}
}

func TestIndexedMemoriesSnapshotFailureWithholdsPartialResult(t *testing.T) {
	failure := errors.New("read snapshot failed")
	m := &indexedMemoryProbe{maintenanceMemories: maintenanceMemories{rows: map[string]string{"plain": "keep"}}, snapshotError: failure}
	plane, index, err := readIndexedMemories(t.Context(), m)
	if !errors.Is(err, failure) || plane.Memories != nil || index.Version != 0 || m.snapshots != 1 {
		t.Fatal("snapshot failure published partial state", err)
	}
}

func TestIndexedMemoriesWithoutSnapshotPreservesLegacyRead(t *testing.T) {
	m := &maintenanceMemories{rows: map[string]string{"plain": "keep"}}
	plane, index, err := readIndexedMemories(t.Context(), m)
	if err != nil || index.Version != 0 || plane.Memories["plain"] != "keep" {
		t.Fatal("basic memory adapter lost legacy behavior", err)
	}
}
