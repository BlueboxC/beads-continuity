package main

import (
	"context"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/memoryops"
)

func readIndexedMemories(ctx context.Context, memories memoryops.Memories) (memoryops.ListResult, codeindex.Index, error) {
	snapshot, ok := memories.(memoryops.SnapshotMemories)
	if !ok {
		plane, err := memories.List(ctx, memoryops.ListRequest{})
		if err != nil {
			return memoryops.ListResult{}, codeindex.Index{}, err
		}
		index, err := codeindex.Load(plane.Memories)
		return plane, index, err
	}
	var plane memoryops.ListResult
	var index codeindex.Index
	err := snapshot.ReadSnapshot(ctx, func(reader memoryops.MemoryReader) error {
		var err error
		plane, err = reader.List(ctx, memoryops.ListRequest{ExcludeKeyPrefix: codeindex.Prefix})
		if err != nil {
			return err
		}
		var current map[string]string
		index, current, err = codeindex.ReadCurrent(ctx, reader)
		if err != nil {
			return err
		}
		if plane.Memories == nil {
			plane.Memories = make(map[string]string)
		}
		for key, value := range current {
			plane.Memories[key] = value
		}
		return nil
	})
	if err != nil {
		return memoryops.ListResult{}, codeindex.Index{}, err
	}
	return plane, index, nil
}
