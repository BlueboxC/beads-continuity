package issueops

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// RecoverRetainedIssueInTx materializes one absent durable Issue row from an
// already authorized, composed retained baseline. It never resolves a source,
// allocates an ID, upserts, restores relations/leases or changes shipping restore.
// The caller owns the allocation guard and must supply the retained current
// participation generation; RecordVersionInTx keeps its ordinary mint fence.
func RecoverRetainedIssueInTx(ctx context.Context, tx *sql.Tx, accepted *types.Issue, generation int64, actor string) error {
	if accepted == nil || accepted.ID == "" || IsWisp(accepted) || accepted.NoHistory || len(accepted.Labels) > 0 || len(accepted.Dependencies) > 0 || len(accepted.Comments) > 0 || accepted.LeaseExpiresAt != nil || accepted.HeartbeatAt != nil || accepted.LeaseGrantedNode != "" {
		return fmt.Errorf("%w: native retained recovery requires an absent durable row without restored structural or lease state", storage.ErrValidation)
	}
	if accepted.CreatedAt.IsZero() || (accepted.Status == types.StatusClosed && accepted.ClosedAt == nil) {
		return fmt.Errorf("%w: native retained recovery lacks protected initialization/lifecycle facts", storage.ErrValidation)
	}
	batch, err := NewBatchContext(ctx, tx, storage.BatchCreateOptions{CreateOnly: true})
	if err != nil {
		return err
	}
	next := *accepted
	next.UpdatedAt = time.Now().UTC()
	next.ContentHash = ""
	next.RowVersion = 0
	if err := PrepareIssueForInsert(&next, batch.CustomStatuses, batch.CustomTypes); err != nil {
		return err
	}
	if err := EnsureIssueIDAvailableInTx(ctx, tx, next.ID); err != nil {
		return err
	}
	if err := InsertIssueStrictInTx(ctx, tx, "issues", &next); err != nil {
		return err
	}
	// This is retained admission evidence, never mintCreate's automatic admission
	// of a previously legacy row. The caller refused a NULL generation first.
	// This write is still private to recovery; its final token is read below
	// before any result or retained revision is published.
	if _, err := tx.ExecContext(ctx, `UPDATE issues SET participation_generation=?, row_lock=? WHERE id=?`, generation, freshRowLock(), next.ID); err != nil {
		return err
	}
	stored, err := GetIssueInTx(ctx, tx, next.ID)
	if err != nil {
		return err
	}
	// Operation time follows the ordinary SQL representation. All accepted
	// editable content and protected durable facts must otherwise round-trip.
	next.UpdatedAt = stored.UpdatedAt
	// Native hydration omits its empty metadata object. Use the same
	// established equivalence as ordinary native Update; do not discard a
	// changed metadata value while checking all remaining durable members.
	changedMetadata, err := metadataChanged(next.Metadata, stored.Metadata)
	if err != nil {
		return err
	}
	if changedMetadata {
		return fmt.Errorf("%w: retained native recovery cannot faithfully represent composed metadata", storage.ErrValidation)
	}
	next.Metadata = stored.Metadata
	intended, err := canonicalDurableState(&next)
	if err != nil {
		return err
	}
	actual, err := canonicalDurableState(stored)
	if err != nil {
		return err
	}
	if !bytes.Equal(intended, actual) {
		var wanted, got map[string]json.RawMessage
		_ = json.Unmarshal(intended, &wanted)
		_ = json.Unmarshal(actual, &got)
		var names []string
		for name, value := range wanted {
			if !bytes.Equal(value, got[name]) {
				names = append(names, name)
			}
		}
		for name := range got {
			if _, ok := wanted[name]; !ok {
				names = append(names, name)
			}
		}
		sort.Strings(names)
		return fmt.Errorf("%w: retained native recovery cannot faithfully represent composed members: %s", storage.ErrValidation, strings.Join(names, ", "))
	}
	// Native journal/audit vocabulary describes materializing the missing row.
	// Graph context separately records same-allocation Inception=revert.
	if err := RecordEventInTable(ctx, tx, "events", next.ID, types.EventCreated, actor, ""); err != nil {
		return err
	}
	if err := RecordEventInTx(ctx, tx, EventCreate, next.ID, actor); err != nil {
		return err
	}
	if err := RecordVersionInTx(ctx, tx, next.ID, actor); err != nil {
		return err
	}
	var recordedActor sql.NullString
	var recordedStatus string
	if err := tx.QueryRowContext(ctx, `SELECT v.change_actor,v.attribution_status FROM issues i JOIN issue_versions v ON v.issue_id=i.id AND v.revision=i.current_revision WHERE i.id=?`, next.ID).Scan(&recordedActor, &recordedStatus); err != nil {
		return err
	}
	if recordedActor.String != actor || recordedStatus != attributionStatusForActor(actor) {
		return fmt.Errorf("%w: native recovery actor cannot be represented faithfully", storage.ErrValidation)
	}
	return nil
}
