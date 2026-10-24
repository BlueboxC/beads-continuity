package graphstore

import (
	"context"
	"fmt"

	"github.com/steveyegge/beads/internal/memoryquery"
)

// Queries are package constants; only values are bound. Size/row checks happen
// on the SQL side before persisted payloads/path feeds are acquired in Go.
// Conservative copy factors cover repeated catalog/payload validation;
// they are not an exact bound on engine memory or query-plan execution work.
func (r *memoryQueryReader) charge(ctx context.Context, query string, args ...any) (uint64, error) {
	var size, count uint64
	values := append([]any{r.request.MaxBytes - r.used + 1}, args...)
	if err := r.tx.QueryRowContext(ctx, query, values...).Scan(&size, &count); err != nil {
		return 0, err
	}
	if size > r.request.MaxBytes-r.used {
		return 0, fmt.Errorf("%w: scoped Memory byte acquisition", ErrLimitExceeded)
	}
	r.used += size
	return count, nil
}

func (r *memoryQueryReader) chargeCurrentLinks(ctx context.Context, path string) error {
	// showMemory validates current authoritative owned Links against their
	// retained copies. Charge the source-local feed including corrupt orphans,
	// including the endpoint catalog facts read by Link validation, then bound
	// row enumeration before its existing ownership classifier runs.
	count, err := r.charge(ctx, `SELECT CAST(LEAST(COALESCE(SUM(3*(512+OCTET_LENGTH(l.path)+OCTET_LENGTH(l.source_path)+OCTET_LENGTH(l.target_path)+OCTET_LENGTH(l.source_revision)+OCTET_LENGTH(l.target_revision)+OCTET_LENGTH(l.properties)+COALESCE(OCTET_LENGTH(l.metadata),0)+OCTET_LENGTH(l.attribution)+COALESCE(OCTET_LENGTH(c.type_url),0)+COALESCE(OCTET_LENGTH(c.revision),0)+COALESCE(OCTET_LENGTH(c.resource_kind),0)+COALESCE(OCTET_LENGTH(c.allocation_state),0)+COALESCE(OCTET_LENGTH(c.backing),0)+COALESCE(OCTET_LENGTH(c.backing_key),0)+COALESCE(OCTET_LENGTH(c.public_id),0)+COALESCE(OCTET_LENGTH(sc.resource_kind),0)+COALESCE(OCTET_LENGTH(sc.type_url),0)+COALESCE(OCTET_LENGTH(sc.allocation_state),0)+COALESCE(OCTET_LENGTH(sc.backing),0)+COALESCE(OCTET_LENGTH(sc.backing_key),0)+COALESCE(OCTET_LENGTH(sc.public_id),0)+COALESCE(OCTET_LENGTH(tc.resource_kind),0)+COALESCE(OCTET_LENGTH(tc.type_url),0)+COALESCE(OCTET_LENGTH(tc.allocation_state),0)+COALESCE(OCTET_LENGTH(tc.backing),0)+COALESCE(OCTET_LENGTH(tc.backing_key),0)+COALESCE(OCTET_LENGTH(tc.public_id),0)+COALESCE(OCTET_LENGTH(v.snapshot),0)+COALESCE(OCTET_LENGTH(v.actor),0))),0),?) AS UNSIGNED),COUNT(*) FROM graph_preview_links l LEFT JOIN graph_preview_catalog c ON c.path=l.path LEFT JOIN graph_preview_versions v ON v.path=c.path AND v.version=c.revision LEFT JOIN graph_preview_catalog sc ON sc.path=l.source_path LEFT JOIN graph_preview_catalog tc ON tc.path=l.target_path WHERE l.source_path=?`, path)
	if err != nil {
		return err
	}
	if count > uint64(r.request.Limits.MaxInspected-r.inspected) {
		return fmt.Errorf("%w: source-local Link acquisition", memoryquery.ErrLimitExceeded)
	}
	r.inspected += int(count)
	return nil
}
