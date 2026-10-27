//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"strings"
	"testing"

	"github.com/steveyegge/beads/memoryops"
)

// Real Dolt must maintain the derived bytes for raw/older writers, rollback and
// UTF-8, rather than trusting a cache maintained only by the current adapter.
func TestContinuityUsageIndexTracksAllWriters(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s := openVersionsStore(t, ctx, o)
	assertUsage := func(indexed bool) {
		t.Helper()
		err := s.withTx(ctx, false, func(tx *sql.Tx) error {
			got, err := hasContinuityUsageIndex(ctx, tx)
			if err != nil {
				return err
			}
			if got != indexed {
				t.Fatalf("index=%t want %t", got, indexed)
			}
			rows, bytes, err := continuityReadUsage(ctx, tx)
			if err != nil {
				return err
			}
			var wantRows, wantBytes uint64
			if err = tx.QueryRowContext(ctx, "SELECT COUNT(*),CAST(COALESCE(SUM(256+OCTET_LENGTH(`key`)+COALESCE(OCTET_LENGTH(value),0)),0) AS UNSIGNED) FROM config WHERE BINARY LEFT(`key`,10)=BINARY 'kv.memory.'").Scan(&wantRows, &wantBytes); err != nil {
				return err
			}
			if rows != wantRows || bytes != wantBytes {
				t.Fatalf("usage=(%d,%d) want (%d,%d)", rows, bytes, wantRows, wantBytes)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	assertUsage(false)
	for _, q := range ContinuityUsageIndexDDL {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	assertUsage(true)
	for _, q := range []string{
		"INSERT INTO config (`key`,value) VALUES ('kv.memory.électrique','日本語'),('kv.memory.empty',''),('kv.memoryXdecoy','excluded'),('KV.MEMORY.case','excluded')",
		"UPDATE config SET value='é' WHERE `key`='kv.memory.empty'",
		"UPDATE config SET `key`='outside' WHERE `key`='kv.memory.électrique'",
		"DELETE FROM config WHERE `key`='kv.memory.empty'",
	} {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
		assertUsage(true)
	}
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = tx.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES ('kv.memory.rolled-back','absent')"); err != nil {
		t.Fatal(err)
	}
	if err = tx.Rollback(); err != nil {
		t.Fatal(err)
	}
	assertUsage(true)
	if _, err = s.db.ExecContext(ctx, "DROP INDEX beads_continuity_usage ON config"); err != nil {
		t.Fatal(err)
	}
	assertUsage(false)
}

func TestContinuityUsageIndexPreservesCapacityRefusal(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s := openVersionsStore(t, ctx, o)
	for _, q := range ContinuityUsageIndexDDL {
		if _, err := s.db.ExecContext(ctx, q); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE config MODIFY COLUMN value LONGBLOB"); err != nil {
		t.Fatal(err)
	}
	if _, err := s.db.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES ('kv.memory.@knowledge/code/retained',?)", strings.Repeat("x", PreviewContinuityReadByteLimit+1)); err != nil {
		t.Fatal(err)
	}
	if _, err := s.Memories("index qualification").Remember(ctx, memoryops.RememberRequest{Key: "must-not-grow", Content: "refused"}); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("indexed oversized growth: %v", err)
	}
	var count int
	if err := s.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM config WHERE `key`='kv.memory.must-not-grow'").Scan(&count); err != nil || count != 0 {
		t.Fatalf("refused write persisted: %d %v", count, err)
	}
}

func TestContinuityUsageIndexRejectsUntrustedColumn(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s := openVersionsStore(t, ctx, o)
	if _, err := s.db.ExecContext(ctx, "ALTER TABLE config ADD COLUMN __beads_continuity_bytes BIGINT UNSIGNED DEFAULT 0"); err != nil {
		t.Fatal(err)
	}
	err := s.withTx(ctx, false, func(tx *sql.Tx) error { _, _, err := continuityReadUsage(context.Background(), tx); return err })
	if !errors.Is(err, ErrInvalidStore) {
		t.Fatalf("trusted an ordinary size column: %v", err)
	}
}
