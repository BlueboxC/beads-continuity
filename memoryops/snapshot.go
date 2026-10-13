package memoryops

import "context"

// MemoryReader exposes only the reads of a borrowed snapshot. Use it within
// ReadSnapshot's callback; it cannot publish or prune a generation.
type MemoryReader interface {
	Recall(context.Context, RecallRequest) (RecallResult, error)
	List(context.Context, ListRequest) (ListResult, error)
}

// SnapshotMemories is optional. The synchronous callback reads one snapshot
// using only its supplied reader. Errors abort the read; no writes occur.
// Other store calls inside the callback can deadlock the embedded engine.
type SnapshotMemories interface {
	ReadSnapshot(context.Context, func(MemoryReader) error) error
}
