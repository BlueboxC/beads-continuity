package issueops

import (
	"context"
	"fmt"
	"time"
)

type AsOfRestriction string

const (
	AsOfRestrictionLive               AsOfRestriction = "live"
	AsOfRestrictionGoneRetention      AsOfRestriction = "gone_retention"
	AsOfRestrictionGoneErasure        AsOfRestriction = "gone_erasure"
	AsOfRestrictionGoneReorganization AsOfRestriction = "gone_reorganization"
	AsOfRestrictionUnknown            AsOfRestriction = "unknown"
)

func NormalizeRemovedRestriction(stored string) AsOfRestriction {
	restriction := AsOfRestriction(stored)
	switch restriction {
	case AsOfRestrictionGoneRetention, AsOfRestrictionGoneErasure,
		AsOfRestrictionGoneReorganization, AsOfRestrictionUnknown:
		return restriction
	default:
		return AsOfRestrictionUnknown
	}
}
func MarkLatestVersionRemovedInTx(ctx context.Context, tx DBTX, issueID string, restriction AsOfRestriction, reason string) error {
	var revision int64
	if err := tx.QueryRowContext(ctx,
		`SELECT revision FROM issue_versions WHERE issue_id = ? ORDER BY revision DESC LIMIT 1`, issueID,
	).Scan(&revision); err != nil {
		return fmt.Errorf("as-of read: find latest version for %s: %w", issueID, err)
	}
	if _, err := tx.ExecContext(ctx,
		`UPDATE issue_versions SET removed_at = ?, removed_reason = ?, removed_restriction = ? WHERE issue_id = ? AND revision = ?`,
		time.Now().UTC(), reason, string(restriction), issueID, revision,
	); err != nil {
		return fmt.Errorf("as-of read: mark version removed for %s: %w", issueID, err)
	}
	return nil
}
