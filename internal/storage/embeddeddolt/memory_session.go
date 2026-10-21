//go:build cgo

package embeddeddolt

import (
	"context"
	"errors"

	"github.com/steveyegge/beads/internal/memoryapi"
	"github.com/steveyegge/beads/internal/storage/issueops"
	storagememoryops "github.com/steveyegge/beads/internal/storage/memoryops"
	"github.com/steveyegge/beads/internal/storage/schema"
	"github.com/steveyegge/beads/memoryops"
)

type MemorySessionOptions struct {
	Write                bool
	EventsJournalEnabled bool
	CommitMessage        string
}

// WithExistingMemories lends one current-schema memory transaction and engine.
// It never initializes or migrates a workspace. A changed writable callback
// publishes through the existing commit helper; the borrowed role then expires.
func WithExistingMemories(ctx context.Context, beadsDir, database, branch string, opts MemorySessionOptions, fn func(memoryops.Memories) (bool, error)) (err error) {
	if fn == nil {
		return errors.New("memory callback required")
	}
	s, err := existingStore(beadsDir, database, branch)
	if err != nil {
		return err
	}
	db, cleanup, err := OpenSQL(ctx, s.dataDir, s.database, s.branch)
	if err != nil {
		return err
	}
	committed := false
	defer func() { err = joinTransactionCleanupError(err, cleanup(), committed) }()
	if err = schema.CheckForwardDrift(ctx, db); err != nil {
		return err
	}
	if err = schema.CheckBehindDrift(ctx, db); err != nil {
		return err
	}
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	clearJournal := issueops.ScopeEventsJournalTransaction(tx, opts.EventsJournalEnabled)
	defer clearJournal()
	changed, err := fn(transactionMemories{snapshotReader: snapshotReader{tx: tx}, write: opts.Write})
	if err != nil {
		return err
	}
	if !opts.Write {
		return nil
	}
	versioned := false
	if changed {
		versioned, err = commitAllInTx(ctx, tx, opts.CommitMessage, true)
		if err != nil {
			return err
		}
	}
	err = tx.Commit()
	committed = err == nil
	if err != nil && versioned {
		return wrapCommitIndeterminate("memory session after version commit", err)
	}
	return err
}

type transactionMemories struct {
	snapshotReader
	write bool
}

func (m transactionMemories) Remember(ctx context.Context, req memoryops.RememberRequest) (memoryops.RememberResult, error) {
	key, err := memoryapi.ResolveKey(req.Key, req.Content)
	if err != nil {
		return memoryops.RememberResult{}, err
	}
	if !m.write {
		return memoryops.RememberResult{}, ErrReadOnly
	}
	replaced, err := storagememoryops.RememberInTx(ctx, m.tx, key, req.Content)
	if err != nil {
		return memoryops.RememberResult{}, err
	}
	return memoryops.RememberResult{Key: key, Value: req.Content, Replaced: replaced}, nil
}
func (m transactionMemories) Forget(ctx context.Context, req memoryops.ForgetRequest) (memoryops.ForgetResult, error) {
	key, err := memoryapi.ValidateKey(req.Key)
	if err != nil {
		return memoryops.ForgetResult{}, err
	}
	if !m.write {
		return memoryops.ForgetResult{}, ErrReadOnly
	}
	previous, found, err := storagememoryops.ForgetInTx(ctx, m.tx, key)
	if err != nil {
		return memoryops.ForgetResult{}, err
	}
	return memoryops.ForgetResult{Key: key, Value: previous, Found: found}, nil
}

var _ memoryops.Memories = transactionMemories{}
