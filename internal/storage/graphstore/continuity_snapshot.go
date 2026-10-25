package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"sync"
	"sync/atomic"

	"github.com/steveyegge/beads/internal/memoryapi"
	txmemory "github.com/steveyegge/beads/internal/storage/memoryops"
	"github.com/steveyegge/beads/memoryops"
)

// ReadSnapshot lends selected continuity reads from one SQL snapshot.
func (m *ContinuityMemories) ReadSnapshot(ctx context.Context, fn func(memoryops.MemoryReader) error) error {
	if fn == nil {
		return fmt.Errorf("%w: snapshot callback required", memoryops.ErrValidation)
	}
	return m.transaction(ctx, false, func(tx *sql.Tx) error {
		r := &continuityReader{memories: m, tx: tx}
		r.active.Store(true)
		defer r.active.Store(false)
		return fn(r)
	})
}

type continuityReader struct {
	memories    *ContinuityMemories
	tx          *sql.Tx
	active      atomic.Bool
	mu          sync.Mutex
	rows, bytes uint64
}

func (r *continuityReader) charge(ctx context.Context, req memoryops.ListRequest, exact bool, excluded ...string) error {
	if !r.active.Load() {
		return sql.ErrTxDone
	}
	n, b, err := r.memories.selectionUsageInTx(ctx, r.tx, req, exact, excluded...)
	if err != nil {
		return err
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if n > PreviewContinuityReadRowLimit-r.rows || b > PreviewContinuityReadByteLimit-r.bytes {
		return ErrLimitExceeded
	}
	r.rows += n
	r.bytes += b
	return nil
}
func (r *continuityReader) Recall(ctx context.Context, req memoryops.RecallRequest) (memoryops.RecallResult, error) {
	key, err := memoryapi.ValidateKey(req.Key)
	if err != nil {
		return memoryops.RecallResult{}, err
	}
	if derivedContinuityKey(key) {
		return r.recallDerived(ctx, key)
	}
	if err = r.charge(ctx, memoryops.ListRequest{KeyPrefix: key}, true); err != nil {
		return memoryops.RecallResult{}, err
	}
	value, found, err := r.memories.resolve(ctx, r.tx, key)
	if err != nil {
		return memoryops.RecallResult{}, err
	}
	return memoryops.RecallResult{Key: key, Value: value, Found: found}, nil
}

// Selected index blobs need only their own keyed row. The SQL CASE enforces
// the remaining callback budget before transferring a payload, in one lookup.
func (r *continuityReader) recallDerived(ctx context.Context, key string) (memoryops.RecallResult, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active.Load() {
		return memoryops.RecallResult{}, sql.ErrTxDone
	}
	if r.rows >= PreviewContinuityReadRowLimit {
		return memoryops.RecallResult{}, ErrLimitExceeded
	}
	remaining := PreviewContinuityReadByteLimit - r.bytes
	stored := txmemory.StorageKey(key)
	var size uint64
	var value sql.NullString
	err := r.tx.QueryRowContext(ctx, "SELECT 256+OCTET_LENGTH(`key`)+OCTET_LENGTH(value),CASE WHEN 256+OCTET_LENGTH(`key`)+OCTET_LENGTH(value)<=? THEN value ELSE NULL END FROM config WHERE `key`=? AND BINARY `key`=BINARY ?", remaining, stored, stored).Scan(&size, &value)
	if errors.Is(err, sql.ErrNoRows) {
		return memoryops.RecallResult{Key: key}, nil
	}
	if err != nil {
		return memoryops.RecallResult{}, err
	}
	if size > remaining {
		return memoryops.RecallResult{}, ErrLimitExceeded
	}
	if !value.Valid {
		return memoryops.RecallResult{}, ErrInvalidStore
	}
	r.rows++
	r.bytes += size
	return memoryops.RecallResult{Key: key, Value: value.String, Found: true}, nil
}
func (r *continuityReader) List(ctx context.Context, req memoryops.ListRequest) (memoryops.ListResult, error) {
	if err := r.charge(ctx, req, false); err != nil {
		return memoryops.ListResult{}, err
	}
	return r.memories.listInTx(ctx, r.tx, req)
}
func continuitySelection(req memoryops.ListRequest, exact bool, excludedPrefixes ...string) (string, []any) {
	prefix := txmemory.StorageKey(req.KeyPrefix)
	where := "BINARY LEFT(k.`key`,CHAR_LENGTH(?))=BINARY ?"
	args := []any{prefix, prefix}
	if exact {
		where = "k.`key`=? AND BINARY k.`key`=BINARY ?"
		args = []any{prefix, prefix}
	}
	exclusions := append([]string(nil), excludedPrefixes...)
	if req.ExcludeKeyPrefix != "" {
		exclusions = append(exclusions, req.ExcludeKeyPrefix)
	}
	for _, value := range exclusions {
		excluded := txmemory.StorageKey(value)
		where += " AND BINARY LEFT(k.`key`,CHAR_LENGTH(?))<>BINARY ?"
		args = append(args, excluded, excluded)
	}
	return where, args
}

// SQL sizes include selected config values and canonical current/retained
// bodies. No obsolete generation is loaded for a current namespace read.
func (m *ContinuityMemories) selectionUsageInTx(ctx context.Context, tx *sql.Tx, req memoryops.ListRequest, exact bool, excluded ...string) (uint64, uint64, error) {
	where, args := continuitySelection(req, exact, excluded...)
	var count, size uint64
	q := "SELECT COUNT(*),CAST(LEAST(COALESCE(SUM(256+OCTET_LENGTH(k.`key`)+OCTET_LENGTH(k.value)+COALESCE(2*OCTET_LENGTH(p.properties)+2*OCTET_LENGTH(p.metadata)+OCTET_LENGTH(v.snapshot),0)),0),67108865) AS UNSIGNED) FROM config k LEFT JOIN graph_preview_payloads p ON BINARY k.value=CONCAT('graph-continuity:v1:',CAST(p.path AS CHAR)) LEFT JOIN graph_preview_catalog c ON c.path=p.path LEFT JOIN graph_preview_versions v ON v.path=c.path AND v.version=c.revision WHERE " + where
	if err := tx.QueryRowContext(ctx, q, args...).Scan(&count, &size); err != nil {
		return 0, 0, err
	}
	if count > PreviewContinuityReadRowLimit || size > PreviewContinuityReadByteLimit {
		return 0, 0, ErrLimitExceeded
	}
	return count, size, nil
}
func (m *ContinuityMemories) checkSelectionInTx(ctx context.Context, tx *sql.Tx, req memoryops.ListRequest, exact bool, excluded ...string) error {
	_, _, err := m.selectionUsageInTx(ctx, tx, req, exact, excluded...)
	return err
}

func (m *ContinuityMemories) listSelectionInTx(ctx context.Context, tx *sql.Tx, req memoryops.ListRequest, excluded ...string) (memoryops.ListResult, error) {
	result := memoryops.ListResult{Memories: map[string]string{}}
	if err := m.checkSelectionInTx(ctx, tx, req, false, excluded...); err != nil {
		return memoryops.ListResult{}, err
	}
	where, args := continuitySelection(req, false, excluded...)
	//nolint:gosec // G202: continuitySelection emits only fixed SQL predicates; all key prefixes are bound arguments.
	rows, err := tx.QueryContext(ctx, "SELECT k.`key`,k.value FROM config k WHERE "+where, args...)
	if err != nil {
		return memoryops.ListResult{}, err
	}
	values := map[string]string{}
	for rows.Next() {
		var key, value string
		if err := rows.Scan(&key, &value); err != nil {
			_ = rows.Close()
			return memoryops.ListResult{}, err
		}
		values[strings.TrimPrefix(key, txmemory.StorageKey(""))] = value
	}
	if err := rows.Err(); err != nil {
		_ = rows.Close()
		return memoryops.ListResult{}, err
	}
	if err := rows.Close(); err != nil {
		return memoryops.ListResult{}, err
	}
	for key, raw := range values {
		if derivedContinuityKey(key) {
			result.Memories[key] = raw
			continue
		}
		value, _, err := m.resolveRawInTx(ctx, tx, key, raw)
		if err != nil {
			return memoryops.ListResult{}, err
		}
		result.Memories[key] = value
	}
	result.Memories = memoryapi.FilterMemories(result.Memories, req.Search)
	return result, nil
}

var _ memoryops.SnapshotMemories = (*ContinuityMemories)(nil)
