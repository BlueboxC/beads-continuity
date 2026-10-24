package graphstore

import (
	"fmt"
	"time"

	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// normalizeIssueDue captures the existing DATETIME(0) representation before
// equality, SQL and retention. Preview writes round to the nearest second;
// half seconds round forward, as in the native engine. This is a reversible
// adapter rule, not a new date parser or a public BDP timestamp precision rule.
func normalizeIssueDue(value *time.Time) (*time.Time, error) {
	if value == nil {
		return nil, nil
	}
	// Reuse the shared optional-date UTC fix from upstream #5771. It copies
	// the pointer so caller-owned times are never mutated.
	issue := types.Issue{DueAt: value}
	issue.NormalizeOptionalTimestampsToUTC()
	result := issue.DueAt.Round(time.Second)
	// Year zero cannot cross the ordinary MySQL driver; rounding may also
	// overflow the maximum year. Refuse before any graph/domain write.
	if result.Year() < 1 || result.Year() > 9999 {
		return nil, fmt.Errorf("%w: Issue due date year must be between 1 and 9999 after rounding", storage.ErrValidation)
	}
	return &result, nil
}

func sameIssueDue(a, b *time.Time) bool {
	return (a == nil && b == nil) || (a != nil && b != nil && a.Equal(*b))
}
