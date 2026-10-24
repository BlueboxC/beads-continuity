package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"unicode/utf8"
)

// Native actor/status/time remain in issue_versions. This optional graph-side
// context carries only the independent facts native Issue JSON cannot express.
type issueRevisionContext struct {
	Inception        string                   `json:"inception,omitempty"`
	HistoricalUpdate *HistoricalUpdateContext `json:"historicalUpdate,omitempty"`
}

func decodeIssueRevisionContext(raw []byte) (issueRevisionContext, error) {
	var value issueRevisionContext
	if raw == nil {
		return value, nil
	}
	if json.Unmarshal(raw, &value) != nil || !sameVersionJSON(raw, value) || !utf8.ValidString(value.Inception) {
		return issueRevisionContext{}, fmt.Errorf("%w: malformed native revision context", ErrInvalidStore)
	}
	if h := value.HistoricalUpdate; h != nil {
		if validateVersionToken(h.FromRevision) != nil || validateVersionToken(h.PreviousRevision) != nil {
			return issueRevisionContext{}, fmt.Errorf("%w: invalid native historical revision context", ErrInvalidStore)
		}
	}
	return value, nil
}

func encodeIssueRevisionContext(value issueRevisionContext) ([]byte, error) {
	if value.Inception == "" && value.HistoricalUpdate == nil {
		return nil, nil
	}
	return canonicalJSON(value)
}

func applyIssueRevisionContext(attribution Attribution, raw []byte) (Attribution, error) {
	context, err := decodeIssueRevisionContext(raw)
	if err != nil {
		return Attribution{}, err
	}
	attribution.Inception, attribution.HistoricalUpdate = context.Inception, context.HistoricalUpdate
	return attribution, nil
}

func currentIssueRevisionContextInTx(ctx context.Context, tx *sql.Tx, path string) (issueRevisionContext, error) {
	var size sql.NullInt64
	if err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(m.revision_context) FROM graph_preview_catalog c JOIN graph_preview_issue_versions m ON m.path=c.path AND m.version=c.revision WHERE c.path=?`, path).Scan(&size); err != nil {
		return issueRevisionContext{}, err
	}
	if size.Valid && size.Int64 > PreviewCurrentReadByteLimit {
		return issueRevisionContext{}, fmt.Errorf("%w: native revision context exceeds acquisition budget", ErrLimitExceeded)
	}
	var raw []byte
	if err := tx.QueryRowContext(ctx, `SELECT m.revision_context FROM graph_preview_catalog c JOIN graph_preview_issue_versions m ON m.path=c.path AND m.version=c.revision WHERE c.path=?`, path).Scan(&raw); err != nil {
		return issueRevisionContext{}, err
	}
	return decodeIssueRevisionContext(raw)
}
