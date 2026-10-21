//go:build cgo

package embeddeddolt

import (
	"context"
	"database/sql"
	"fmt"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/issueops"
	"github.com/steveyegge/beads/internal/types"
)

var _ storage.ProjectSnapshotter = (*EmbeddedDoltStore)(nil)

// ReadProjectSnapshot shares one readonly transaction across all graph planes.
func (s *EmbeddedDoltStore) ReadProjectSnapshot(ctx context.Context, fn func(storage.ProjectReader) error) error {
	if fn == nil {
		return fmt.Errorf("project snapshot callback required")
	}
	return s.withConn(ctx, false, func(tx *sql.Tx) error { return fn(snapshotReader{tx: tx}) })
}

func (r snapshotReader) SearchIssues(ctx context.Context, q string, filter types.IssueFilter) ([]*types.Issue, error) {
	return issueops.SearchIssuesInTx(ctx, r.tx, q, filter)
}
func (r snapshotReader) GetDependencyRecordsForIssues(ctx context.Context, ids []string) (map[string][]*types.Dependency, error) {
	return issueops.GetDependencyRecordsForIssuesInTx(ctx, r.tx, ids)
}
