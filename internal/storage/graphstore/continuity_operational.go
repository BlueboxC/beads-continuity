package graphstore

import (
	"context"
	"database/sql"
	"encoding/hex"

	"github.com/steveyegge/beads/internal/activity"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/internal/knowledge"
	"github.com/steveyegge/beads/memoryops"
)

// Operational reads select current code and cited origins, never retained generations.
func (m *ContinuityMemories) ReadCodeContinuity(ctx context.Context) (memoryops.ListResult, codeindex.Index, error) {
	var plane memoryops.ListResult
	var index codeindex.Index
	err := m.transaction(ctx, false, func(tx *sql.Tx) error {
		r := m.operationalReader(tx)
		defer r.active.Store(false)
		var err error
		plane, err = m.operationalPlane(ctx, tx, r, nil)
		if err != nil {
			return err
		}
		var current map[string]string
		index, current, err = codeindex.ReadCurrent(ctx, r)
		if err != nil {
			return err
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

func (m *ContinuityMemories) ReadKnowledgeContinuity(ctx context.Context, ids []string) (memoryops.ListResult, error) {
	if len(ids) > activity.MaxReferences {
		return memoryops.ListResult{}, ErrLimitExceeded
	}
	var plane memoryops.ListResult
	err := m.transaction(ctx, false, func(tx *sql.Tx) error {
		r := m.operationalReader(tx)
		defer r.active.Store(false)
		var err error
		plane, err = m.operationalPlane(ctx, tx, r, ids)
		return err
	})
	if err != nil {
		return memoryops.ListResult{}, err
	}
	return plane, nil
}

func (m *ContinuityMemories) ReadLearningContinuity(ctx context.Context, session, turn string, ids []string) (memoryops.ListResult, activity.View, error) {
	if len(ids) > activity.MaxReferences {
		return memoryops.ListResult{}, activity.View{}, ErrLimitExceeded
	}
	var plane memoryops.ListResult
	var observed activity.View
	err := m.transaction(ctx, false, func(tx *sql.Tx) error {
		r := m.operationalReader(tx)
		defer r.active.Store(false)
		var err error
		observed, err = readObservedActivityInTx(ctx, tx, session, turn, activity.MaxReferences, false)
		if err != nil {
			return err
		}
		selected := append([]string(nil), ids...)
		if session != "" {
			for _, e := range observed.Events {
				selected = append(selected, e.ID)
			}
		}
		plane, err = m.operationalPlane(ctx, tx, r, selected)
		if err != nil {
			return err
		}
		settings, err := r.Recall(ctx, memoryops.RecallRequest{Key: activity.SettingsKey})
		if err == nil && settings.Found {
			plane.Memories[settings.Key] = settings.Value
		}
		return err
	})
	if err != nil {
		return memoryops.ListResult{}, activity.View{}, err
	}
	return plane, observed, nil
}

func (m *ContinuityMemories) ReadObservedActivity(ctx context.Context, session, turn string, limit int) (activity.View, error) {
	return m.store.ReadObservedActivity(ctx, session, turn, limit, false)
}

func (m *ContinuityMemories) operationalReader(tx *sql.Tx) *continuityReader {
	r := &continuityReader{memories: m, tx: tx}
	r.active.Store(true)
	return r
}

func (m *ContinuityMemories) operationalPlane(ctx context.Context, tx *sql.Tx, r *continuityReader, requested []string) (memoryops.ListResult, error) {
	req := memoryops.ListRequest{ExcludeKeyPrefix: codeindex.Prefix}
	if err := r.charge(ctx, req, false, activity.Prefix); err != nil {
		return memoryops.ListResult{}, err
	}
	plane, err := m.listSelectionInTx(ctx, tx, req, activity.Prefix)
	if err != nil {
		return memoryops.ListResult{}, err
	}
	ids := map[string]bool{}
	for _, p := range knowledge.Decode(plane.Memories).Proposals {
		for _, ref := range p.ActivityEvents {
			ids[ref.ID] = true
		}
	}
	for _, id := range requested {
		b, e := hex.DecodeString(id)
		if e != nil || len(b) != 32 {
			return memoryops.ListResult{}, memoryops.ErrValidation
		}
		ids[id] = true
	}
	for id := range ids {
		value, e := r.Recall(ctx, memoryops.RecallRequest{Key: activity.Prefix + "event/" + id})
		if e != nil {
			return memoryops.ListResult{}, e
		}
		if value.Found {
			plane.Memories[value.Key] = value.Value
		}
	}
	return plane, nil
}
