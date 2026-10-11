package graphstore

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"strings"
)

// CheckpointConversion captures a native full backup and restores a separate
// candidate. It refuses stale plans and verifies restoration after reopening.
// Caller-owned selector/binary receipts and activation are separate operations.
func CheckpointConversion(ctx context.Context, o Options, plan ConversionPlan, backup, candidate string) (err error) {
	if err = validateConversionOptions(o); err != nil {
		return err
	}
	if o.DataDir != plan.SourceDataDir || o.Database != plan.Database || o.Binding != plan.Binding {
		return fmt.Errorf("%w: checkpoint identity differs from plan", ErrConflict)
	}
	paths := []string{o.DataDir, backup, candidate}
	for i, path := range paths {
		if !filepath.IsAbs(path) || filepath.Clean(path) != path {
			return fmt.Errorf("%w: checkpoint paths must be absolute and clean", ErrInvalidStore)
		}
		var real string
		if i == 0 {
			real, err = filepath.EvalSymlinks(path)
		} else {
			var parent string
			parent, err = filepath.EvalSymlinks(filepath.Dir(path))
			real = filepath.Join(parent, filepath.Base(path))
		}
		if err != nil || real != path {
			return fmt.Errorf("%w: checkpoint path is not canonical: %s", ErrInvalidStore, path)
		}
		for _, prior := range paths[:i] {
			if path == prior || strings.HasPrefix(path, prior+string(filepath.Separator)) || strings.HasPrefix(prior, path+string(filepath.Separator)) {
				return fmt.Errorf("%w: source/checkpoint/candidate paths overlap", ErrInvalidStore)
			}
		}
	}
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	connClosed := false
	defer func() {
		if !connClosed {
			err = errors.Join(err, conn.Close())
		}
	}()
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	got, err := planConversionWithPruneInTx(ctx, tx, o, plan.PruneObsoleteCode)
	rollbackErr := tx.Rollback()
	if err != nil || rollbackErr != nil {
		return errors.Join(err, rollbackErr)
	}
	if !reflect.DeepEqual(got, plan) {
		return fmt.Errorf("%w: source changed since planning", ErrConflict)
	}
	// Exclusive directories avoid overwriting any existing recovery material.
	if err := os.Mkdir(backup, 0700); err != nil {
		return err
	}
	backupURL := (&url.URL{Scheme: "file", Path: backup}).String()
	if _, err := conn.ExecContext(ctx, "CALL DOLT_BACKUP('sync-url',?)", backupURL); err != nil {
		return err
	}
	after, err := conversionState(ctx, conn)
	if err != nil {
		return err
	}
	if !reflect.DeepEqual(after, plan.State) {
		return fmt.Errorf("%w: capture changed source state", ErrConflict)
	}
	connClosed = true
	if err := conn.Close(); err != nil {
		return err
	}
	if err := s.Close(); err != nil {
		return err
	}
	if err := os.Mkdir(candidate, 0700); err != nil {
		return err
	}
	candidateOptions := o
	candidateOptions.DataDir = candidate
	restored, err := openBackend(ctx, candidateOptions, "")
	if err != nil {
		return err
	}
	if _, err = restored.db.ExecContext(ctx, "CALL DOLT_BACKUP('restore',?,?)", backupURL, o.Database); err != nil {
		return errors.Join(err, restored.Close())
	}
	if err := restored.Close(); err != nil {
		return err
	}
	// The verification open is distinct from the restoring connector/session.
	checked, err := planConversion(ctx, candidateOptions, plan.PruneObsoleteCode)
	if err != nil {
		return err
	}
	checked.SourceDataDir = plan.SourceDataDir
	if !reflect.DeepEqual(checked, plan) {
		return fmt.Errorf("%w: restored checkpoint differs from source", ErrInvalidStore)
	}
	return nil
}
