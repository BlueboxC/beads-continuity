package codeindex

import (
	"context"
	"errors"
	"github.com/steveyegge/beads/memoryops"
	"reflect"
	"testing"
)

func TestCurrentIndexPreservesVersionsFragmentsAndRetainedGenerations(t *testing.T) {
	for _, version := range []int{1, 2, 3} {
		t.Run(string(rune('0'+version)), func(t *testing.T) {
			store := &selectedStore{publicationStore: publicationStore{plane: map[string]string{"plain": "preserve"}}}
			original := largeFileFixture()
			original.Version = version
			if _, err := Save(t.Context(), store, store.plane, original); err != nil {
				t.Fatal(err)
			}
			store.plane[Prefix+"blob/old-unused"] = "corrupt unused retained generation"
			before := map[string]string{}
			for k, v := range store.plane {
				before[k] = v
			}
			expected, err := Load(store.plane)
			if err != nil {
				t.Fatal(err)
			}
			got, current, err := ReadCurrent(t.Context(), store)
			if err != nil || !reflect.DeepEqual(got, expected) {
				t.Fatal("current reader changed complete generation", err)
			}
			if _, ok := current[Prefix+"blob/old-unused"]; ok {
				t.Fatal("read unused generation")
			}
			if len(store.reads) != len(current) {
				t.Fatal("blob recalled repeatedly")
			}
			recovered, err := Load(current)
			if err != nil || !reflect.DeepEqual(got, recovered) {
				t.Fatal("publication plane incomplete", err)
			}
			if !reflect.DeepEqual(before, store.plane) {
				t.Fatal("retained generation or human row mutated")
			}
		})
	}
}

type failedCurrentReader struct {
	memoryops.MemoryReader
	failure error
}

func (r failedCurrentReader) Recall(_ context.Context, _ memoryops.RecallRequest) (memoryops.RecallResult, error) {
	return memoryops.RecallResult{}, r.failure
}

func TestCurrentIndexFailureWithholdsGeneration(t *testing.T) {
	failure := errors.New("store read failed")
	index, plane, err := ReadCurrent(t.Context(), failedCurrentReader{failure: failure})
	if !errors.Is(err, failure) || index.Version != 0 || plane != nil {
		t.Fatal("failed read leaked generation", err)
	}
	store := selectedFixture(t, 1)
	var entry manifest
	_ = unpack(store.plane[ManifestKey], &entry)
	delete(store.plane, Prefix+"blob/"+entry.Parts[0])
	index, plane, err = ReadCurrent(t.Context(), store)
	if err == nil || index.Version != 0 || plane != nil {
		t.Fatal("corrupt current references accepted")
	}
}
