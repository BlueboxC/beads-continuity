package storage

import (
	"context"
	"github.com/steveyegge/beads/internal/types"
	"github.com/steveyegge/beads/memoryops"
)

// ProjectReader lends only the reads needed by a project graph.
type ProjectReader interface {
	memoryops.MemoryReader
	ProjectTaskReader
}

// ProjectTaskReader is the existing task/dependency read boundary.
type ProjectTaskReader interface {
	SearchIssues(context.Context, string, types.IssueFilter) ([]*types.Issue, error)
	GetDependencyRecordsForIssues(context.Context, []string) (map[string][]*types.Dependency, error)
}

// ProjectSnapshotter releases its engine and transaction after the callback.
type ProjectSnapshotter interface {
	ReadProjectSnapshot(context.Context, func(ProjectReader) error) error
}
