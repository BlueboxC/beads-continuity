package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strings"
)

// VerifySchema8Candidate checks the conversion boundary, all original revision
// bytes and every authoritative human key. It does not activate a workspace.
func VerifySchema8Candidate(ctx context.Context, o Options, plan ConversionPlan) (state ConversionState, err error) {
	if plan.SourceSchema != 8 || o.Binding != plan.Binding || o.Database != plan.Database {
		return state, ErrInvalidStore
	}
	s, err := OpenExisting(ctx, o)
	if err != nil {
		return state, err
	}
	defer func() {
		err = errors.Join(err, s.Close())
		if err != nil {
			state = ConversionState{}
		}
	}()
	err = s.withTx(ctx, false, func(tx *sql.Tx) error {
		var size uint64
		if err := tx.QueryRowContext(ctx, `SELECT OCTET_LENGTH(manifest) FROM graph_preview_conversion_origin WHERE singleton=1`).Scan(&size); err != nil {
			return err
		}
		if size > PreviewCurrentReadByteLimit {
			return ErrLimitExceeded
		}
		var raw []byte
		var hash string
		if err := tx.QueryRowContext(ctx, `SELECT manifest,sha256 FROM graph_preview_conversion_origin WHERE singleton=1`).Scan(&raw, &hash); err != nil {
			return err
		}
		want, err := canonicalJSON(plan)
		if err != nil {
			return err
		}
		if !bytes.Equal(raw, want) || conversionHash(raw) != hash {
			return ErrInvalidStore
		}
		descriptors, err := schema8Descriptors(s.ScopeURL())
		if err != nil {
			return err
		}
		var typeCount int
		if err = tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_original_types`).Scan(&typeCount); err != nil {
			return err
		}
		if typeCount != len(descriptors) {
			return ErrInvalidStore
		}
		for _, descriptor := range descriptors {
			if _, err = originalSchema8TypeInTx(ctx, tx, s.ScopeURL(), descriptor.ID()); err != nil {
				return err
			}
		}
		for key, want := range plan.HumanMemories {
			value, found, err := s.Memories("").resolve(ctx, tx, key)
			if err != nil {
				return err
			}
			if !found || conversionHash([]byte(value)) != want {
				return fmt.Errorf("%w: converted Memory differs", ErrInvalidStore)
			}
		}
		// Read addresses in bounded pages; each original body has its own byte cap.
		lastPath, lastVersion := "", ""
		for {
			rows, err := tx.QueryContext(ctx, `SELECT path,version FROM graph_preview_original_versions WHERE path>? OR(path=? AND version>?) ORDER BY path,version LIMIT 128`, lastPath, lastPath, lastVersion)
			if err != nil {
				return err
			}
			var page []originalAddress
			for rows.Next() {
				var a originalAddress
				if err = rows.Scan(&a.path, &a.version); err != nil {
					_ = rows.Close()
					return err
				}
				page = append(page, a)
			}
			if err = errors.Join(rows.Err(), rows.Close()); err != nil {
				return err
			}
			if len(page) == 0 {
				break
			}
			for _, a := range page {
				if _, _, found, err := s.originalVersionInTx(ctx, tx, a.path, a.version); err != nil {
					return err
				} else if !found {
					return ErrInvalidStore
				}
			}
			lastPath, lastVersion = page[len(page)-1].path, page[len(page)-1].version
		}
		var archived, source int
		if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM graph_preview_original_versions`).Scan(&archived); err != nil {
			return err
		}
		if err := tx.QueryRowContext(ctx, `SELECT (SELECT COUNT(*) FROM graph_preview_source_graph_preview_versions)+(SELECT COUNT(*) FROM graph_preview_source_graph_preview_issue_versions)`).Scan(&source); err != nil {
			return err
		}
		if archived != source {
			return fmt.Errorf("%w: incomplete original revision inventory", ErrInvalidStore)
		}
		if _, err := s.currentSnapshotWithBoundsInTx(ctx, tx, PreviewContinuityReadRowLimit, PreviewContinuityReadByteLimit); err != nil {
			return err
		}
		state, err = conversionState(ctx, tx)
		if err != nil {
			return err
		}
		if state.Head != plan.State.Head || state.Staged != plan.State.Staged || !reflect.DeepEqual(state.Branches, plan.State.Branches) || !reflect.DeepEqual(state.Tags, plan.State.Tags) {
			return fmt.Errorf("%w: retained Dolt references changed", ErrInvalidStore)
		}
		for name, hash := range plan.State.Tables {
			if !strings.HasPrefix(name, "graph_preview_") && name != "issues" && name != "issue_versions" && state.Tables[name] != hash {
				return fmt.Errorf("%w: original table %s changed", ErrInvalidStore, name)
			}
		}
		return nil
	})
	return state, err
}
