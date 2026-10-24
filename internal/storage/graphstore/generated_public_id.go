package graphstore

import (
	"context"
	"database/sql"
	"fmt"

	graph "github.com/steveyegge/beads/graphops"
)

const generatedIDAttemptsPerLength = 8

// selectGeneratedPublicIDInTx chooses a public name inside the caller's fenced
// write transaction. The caller MUST insert its allocation in that same
// transaction before releasing the writer fence. A successful return alone is
// not a reservation. The existing collision predicate checks the complete
// public name across kinds, tombstones, installed Types and discovery URLs.
func selectGeneratedPublicIDInTx(ctx context.Context, tx *sql.Tx, root string) (string, error) {
	return selectGeneratedPublicIDWithCandidateInTx(ctx, tx, root, graph.GeneratedResourceIDCandidate)
}

func selectGeneratedPublicIDWithCandidateInTx(ctx context.Context, tx *sql.Tx, root string, candidate func(string, int) (string, error)) (string, error) {
	for length := graph.GeneratedResourceIDInitialLength; length <= graph.GeneratedResourceIDMaxLength; length++ {
		for attempt := 0; attempt < generatedIDAttemptsPerLength; attempt++ {
			if err := ctx.Err(); err != nil {
				return "", err
			}
			id, err := candidate(root, length)
			if err != nil {
				return "", err
			}
			exists, err := publicResourceIDExistsInTx(ctx, tx, id)
			if err != nil {
				return "", err
			}
			if !exists {
				return id, nil
			}
		}
	}
	return "", fmt.Errorf("%w: generated public ID candidates exhausted", ErrAlreadyExists)
}
