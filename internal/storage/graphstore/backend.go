package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sync"
	"time"
	"unicode/utf8"

	mysql "github.com/go-sql-driver/mysql"
	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage/doltutil"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/schema"
)

var databaseName = regexp.MustCompile(`^[a-zA-Z_][a-zA-Z0-9_]{0,63}$`)
var authorityID = regexp.MustCompile(`^[a-f0-9]{32}$`)

// Store owns an ordinary database connector, never an authority grant. Each
// operation uses one transaction. Callers must check Close before reporting
// success; no physical server terminality is inferred from closing a socket.
type Store struct {
	db          *sql.DB
	options     Options
	cleanup     func() error
	closeOnce   sync.Once
	closeErr    error
	catalogOnce sync.Once
	catalog     *selectedTypeCatalog
	catalogErr  error
	// afterWrite is an internal test seam for real-engine rollback controls.
	// No CLI or production configuration can enable it.
	afterWrite func(string) error
}

// Close releases this store's database and, for embedded mode, its connector.
func (s *Store) Close() error {
	if s == nil {
		return nil
	}
	s.closeOnce.Do(func() { s.closeErr = s.cleanup() })
	return s.closeErr
}

func validateOptions(o Options) error {
	if o.Backend != "embedded" && o.Backend != "server" {
		return fmt.Errorf("%w: backend must be embedded or server", ErrInvalidStore)
	}
	if !databaseName.MatchString(o.Database) || (o.Branch != "" && o.Branch != "main") {
		return fmt.Errorf("%w: invalid database or unsupported branch", ErrInvalidStore)
	}
	if o.Binding.SchemaVersion != SchemaVersion || !authorityID.MatchString(o.Binding.AuthorityID) {
		return fmt.Errorf("%w: invalid preview version or authority ID", ErrInvalidStore)
	}
	if o.TypeGeneration != 0 && o.TypeGeneration != TypeGeneration {
		return fmt.Errorf("%w: unsupported workspace Type generation", ErrInvalidStore)
	}
	if err := graph.ValidatePersistedScopeURL(o.Binding.ScopeURL); err != nil {
		return fmt.Errorf("%w: invalid Scope: %v", ErrInvalidStore, err)
	}
	if !utf8.ValidString(o.Binding.WorkspaceID) || !filepath.IsAbs(o.Binding.WorkspaceID) {
		return fmt.Errorf("%w: workspace must be an absolute canonical path", ErrInvalidStore)
	}
	actual, err := filepath.EvalSymlinks(o.Binding.WorkspaceID)
	if err != nil || actual != o.Binding.WorkspaceID {
		return fmt.Errorf("%w: workspace realpath mismatch", ErrInvalidStore)
	}
	if o.Backend == "embedded" {
		if !filepath.IsAbs(o.DataDir) || filepath.Clean(o.DataDir) != o.DataDir {
			return fmt.Errorf("%w: embedded data path must be absolute", ErrInvalidStore)
		}
	} else if o.ServerSocket == "" && (o.ServerHost == "" || o.ServerPort < 1 || o.ServerPort > 65535) {
		return fmt.Errorf("%w: explicit server address required", ErrInvalidStore)
	}
	return nil
}

func openBackend(ctx context.Context, o Options, database string) (*Store, error) {
	var db *sql.DB
	var cleanup func() error
	var err error
	if o.Backend == "embedded" {
		db, cleanup, err = embeddeddolt.OpenSQL(ctx, o.DataDir, database, "main")
	} else {
		base := doltutil.ServerDSN{Host: o.ServerHost, Port: o.ServerPort, Socket: o.ServerSocket,
			User: o.ServerUser, Password: o.ServerPassword, TLS: o.ServerTLS, Database: database}
		var cfg *mysql.Config
		cfg, err = mysql.ParseDSN(base.String())
		if err == nil {
			// Bound wire I/O as well as caller cancellation. These are ordinary
			// client deadlines, not acknowledgments of server-side termination.
			cfg.ReadTimeout = 30 * time.Second
			cfg.WriteTimeout = 30 * time.Second
			db, err = sql.Open("mysql", cfg.FormatDSN())
		}
		if err == nil {
			cleanup = db.Close
			err = db.PingContext(ctx)
			if err != nil {
				err = errors.Join(err, cleanup())
			}
		}
	}
	if err != nil {
		return nil, err
	}
	db.SetMaxOpenConns(1)
	db.SetMaxIdleConns(1)
	return &Store{db: db, options: o, cleanup: cleanup}, nil
}

// Init creates only a new database. It never adopts or heals an existing one.
// CLI admission owns exclusive .beads creation and pending/ready publication.
// Partial DDL failure leaves that workspace pending for explicit disposal.
func Init(ctx context.Context, o Options) (err error) {
	if err = validateOptions(o); err != nil {
		return err
	}
	if o.TypeGeneration != 0 && o.TypeGeneration != TypeGeneration {
		return fmt.Errorf("%w: fresh graph init writes only Type generation %d", ErrInvalidStore, TypeGeneration)
	}
	if o.Backend == "embedded" {
		// Only the engine child directory is provisioned here; CLI owns the
		// exclusive workspace directory before this method is called.
		if _, err = os.Stat(filepath.Dir(o.DataDir)); err != nil {
			return err
		}
		if err = os.Mkdir(o.DataDir, 0o700); err != nil && !errors.Is(err, os.ErrExist) {
			return err
		}
	}
	s, err := openBackend(ctx, o, "")
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, s.Close()) }()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, conn.Close()) }()
	// Identifier validation above is mandatory; values elsewhere are bound.
	if _, err = conn.ExecContext(ctx, "CREATE DATABASE `"+o.Database+"`"); err != nil {
		return err
	}
	if _, err = conn.ExecContext(ctx, "USE `"+o.Database+"`"); err != nil {
		return err
	}
	if _, err = schema.MigrateUp(ctx, conn); err != nil {
		return fmt.Errorf("initialize standard schema: %w", err)
	}
	return installPreview(ctx, conn, o)
}

// OpenExisting performs no DDL or migration. Persisted graph binding and its
// installed descriptor must match exactly before the Store is returned.
func OpenExisting(ctx context.Context, o Options) (*Store, error) {
	if err := validateOptions(o); err != nil {
		return nil, err
	}
	if o.Backend == "embedded" {
		info, err := os.Stat(filepath.Join(o.DataDir, o.Database, ".dolt"))
		if err != nil || !info.IsDir() {
			return nil, fmt.Errorf("%w: embedded database is missing", ErrInvalidStore)
		}
	}
	s, err := openBackend(ctx, o, o.Database)
	if err != nil {
		return nil, err
	}
	if err = s.withTx(ctx, false, func(tx *sql.Tx) error { return checkBinding(ctx, tx, o) }); err != nil {
		return nil, errors.Join(err, s.Close())
	}
	return s, nil
}

// withTx never replays fn. All graph writes, including conflict coordination
// and retained bytes, use this single transaction. Only an explicit decoded
// serialization rollback is a known conflict; other commit errors are unknown.
func (s *Store) withTx(ctx context.Context, write bool, fn func(*sql.Tx) error) error {
	return s.withTxPolicy(ctx, txPolicy{mayWrite: write, commit: write}, fn)
}

// withTypeValidationTx validates the final selected graph before publishing a
// mutation that can change its Types, properties, endpoints or liveness.
// Native payload-only writers keep their existing validation through withTx.
func (s *Store) withTypeValidationTx(ctx context.Context, fn func(*sql.Tx) error) error {
	return s.withTxPolicy(ctx, txPolicy{mayWrite: true, commit: true, validateTypes: true}, fn)
}

// txPolicy separates staged writes from publication: an import dry-run writes
// for validation, then rolls back. Selected graph mutations opt into validation;
// native payload-only readers and writers retain withTx behavior.
type txPolicy struct {
	mayWrite      bool
	commit        bool
	validateTypes bool // mutations affecting the selected graph, including import dry-runs
	conversion    bool // separately restored conversion candidate, never an ordinary writer
}

func (s *Store) withTxPolicy(ctx context.Context, policy txPolicy, fn func(*sql.Tx) error) (err error) {
	if ctx == nil {
		return errors.New("graph operation requires a context")
	}
	if err = ctx.Err(); err != nil {
		return err
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return err
	}
	committed := false
	defer func() {
		closeErr := conn.Close()
		if committed && closeErr != nil {
			closeErr = errors.Join(ErrOutcomeUnknown, closeErr)
		}
		err = errors.Join(err, closeErr)
	}()
	// Pin the branch and conservative ordinary engine settings before BEGIN.
	for _, stmt := range []string{"SET @@dolt_transaction_commit = 0", "SET @@dolt_force_transaction_commit = 0", "SET @@dolt_allow_commit_conflicts = 0", "SET @@time_zone = '+00:00'"} {
		if _, err = conn.ExecContext(ctx, stmt); err != nil {
			return err
		}
	}
	var branch string
	if err = conn.QueryRowContext(ctx, "SELECT active_branch()").Scan(&branch); err != nil {
		return err
	}
	if branch != "main" {
		return fmt.Errorf("%w: unexpected database branch", ErrInvalidStore)
	}
	tx, err := conn.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	if !policy.mayWrite {
		defer bindReadSnapshot(tx)()
	}
	rollback := func() error {
		rollbackErr := tx.Rollback()
		if policy.mayWrite && rollbackErr != nil {
			return errors.Join(ErrOutcomeUnknown, rollbackErr)
		}
		return rollbackErr
	}
	finished := false
	defer func() {
		if !finished {
			err = errors.Join(err, rollback())
		}
	}()
	var before acquisitionUsage
	if policy.mayWrite && !policy.conversion {
		if err = checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		before, err = readAcquisitionUsage(ctx, tx)
		if err != nil {
			return err
		}
	}
	if err = fn(tx); err != nil {
		return err
	}
	if policy.mayWrite && !policy.conversion {
		after, usageErr := readAcquisitionUsage(ctx, tx)
		if usageErr != nil {
			return usageErr
		}
		if err = checkAcquisitionGrowth(before, after); err != nil {
			return err
		}
		if err = checkCurrentIssueAuthority(ctx, tx); err != nil {
			return err
		}
	}
	if policy.validateTypes {
		if err = s.validateTypeStateInTx(ctx, tx); err != nil {
			return err
		}
	}
	if !policy.commit {
		err = rollback()
		finished = true
		return err
	}
	err = tx.Commit()
	finished = true
	if err != nil {
		return classifyCommitError(err)
	}
	committed = true
	return nil
}

func classifyCommitError(err error) error {
	if err == nil {
		return nil
	}
	// Same decoded-server precedent as dolt.isSerializationError, deliberately
	// narrower: a lock-wait timeout or arbitrary typed error is not proof that
	// this transaction rolled back. Never classify transport text as a conflict.
	var serverErr *mysql.MySQLError
	if errors.As(err, &serverErr) && serverErr.Number == 1213 && string(serverErr.SQLState[:]) == "40001" {
		return errors.Join(ErrConflict, err)
	}
	return errors.Join(ErrOutcomeUnknown, err)
}
