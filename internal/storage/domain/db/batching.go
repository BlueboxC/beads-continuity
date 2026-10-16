package db

// forEachIDBatch calls fn once per queryBatchSize-sized slice of ids, in
// order, stopping at the first error. The bulk readers use it to keep every
// IN (...) list bounded without transferring unrelated rows.
//
// A given id lands in exactly one batch, so per-id result groups — and the
// ORDER BY within each group — survive being merged across batches.
//
// Read paths only: the DeleteAllForIDs loops keep their own deleteBatchSize
// and are deliberately left open-coded.
func forEachIDBatch(ids []string, fn func(batch []string) error) error {
	// SQL IN treats repeated IDs as a set, including across batch boundaries.
	seen := make(map[string]struct{}, len(ids))
	unique := make([]string, 0, len(ids))
	for _, id := range ids {
		if _, exists := seen[id]; exists {
			continue
		}
		seen[id] = struct{}{}
		unique = append(unique, id)
	}
	ids = unique
	for start := 0; start < len(ids); start += queryBatchSize {
		end := start + queryBatchSize
		if end > len(ids) {
			end = len(ids)
		}
		if err := fn(ids[start:end]); err != nil {
			return err
		}
	}
	return nil
}
