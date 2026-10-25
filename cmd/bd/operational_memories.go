package main

import (
	"context"
	"github.com/steveyegge/beads/internal/activity"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/memoryops"
)

type codeContinuityReader interface {
	ReadCodeContinuity(context.Context) (memoryops.ListResult, codeindex.Index, error)
}

func readCodeMemories(ctx context.Context, m memoryops.Memories) (memoryops.ListResult, codeindex.Index, error) {
	if selected, ok := m.(codeContinuityReader); ok {
		return selected.ReadCodeContinuity(ctx)
	}
	return readIndexedMemories(ctx, m)
}

func readKnowledgeMemories(ctx context.Context, m memoryops.Memories, ids []string) (memoryops.ListResult, error) {
	if selected, ok := m.(interface {
		ReadKnowledgeContinuity(context.Context, []string) (memoryops.ListResult, error)
	}); ok {
		return selected.ReadKnowledgeContinuity(ctx, ids)
	}
	return m.List(ctx, knowledgeMemorySelection())
}

func readLearningMemories(ctx context.Context, m memoryops.Memories, session, turn string, ids []string) (memoryops.ListResult, *activity.View, error) {
	if selected, ok := m.(interface {
		ReadLearningContinuity(context.Context, string, string, []string) (memoryops.ListResult, activity.View, error)
	}); ok {
		plane, observed, err := selected.ReadLearningContinuity(ctx, session, turn, ids)
		return plane, &observed, err
	}
	plane, err := m.List(ctx, knowledgeMemorySelection())
	return plane, nil, err
}
