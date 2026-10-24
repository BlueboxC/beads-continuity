//go:build cgo

package graphstore

import (
	"context"
	"database/sql"
	"database/sql/driver"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/legacyimport"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/types"
)

// This tests error wrapping at the SQL boundary without opening an engine.
// The double only supplies COUNT results or the selected query failure;
// it does not model tables, mutations or transaction behavior.
func TestLegacyImportFreshnessQueryErrors(t *testing.T) {
	for _, test := range []struct {
		name, query, plane string
	}{
		{"table", "SELECT COUNT(*) FROM graph_preview_catalog", "graph_preview_catalog"},
		{"journal-sequence", "SELECT COUNT(*) FROM bd_events_seq WHERE id<>0 OR next_seq<>0", "bd_events_seq"},
		{"epoch-address", "SELECT COUNT(*) FROM epoch_minted_addresses", "epoch_minted_addresses"},
		{"interaction", "SELECT COUNT(*) FROM interactions", "interactions"},
		{"epoch-state", legacyFreshnessChangedEpochQuery, "store_epoch"},
		{"config", "SELECT COUNT(*) FROM config WHERE LEFT(`key`,?)=?", "config"},
	} {
		for _, failure := range []struct {
			name         string
			cause        error
			invalidStore bool
		}{
			{"unreadable", errors.New("freshness query unavailable"), true},
			{"canceled", context.Canceled, false},
			{"deadline", context.DeadlineExceeded, false},
		} {
			t.Run(test.name+"/"+failure.name, func(t *testing.T) {
				connector := &legacyFreshnessCountDriver{query: test.query, cause: failure.cause}
				db := sql.OpenDB(connector)
				t.Cleanup(func() {
					if err := db.Close(); err != nil {
						t.Error(err)
					}
				})
				tx, err := db.BeginTx(t.Context(), nil)
				if err != nil {
					t.Fatal(err)
				}
				defer func() {
					if err := tx.Rollback(); err != nil {
						t.Error(err)
					}
				}()
				err = requireEmptyLegacyTarget(t.Context(), tx)
				planeContext := "inspect legacy import target " + test.plane + ":"
				if failure.invalidStore {
					planeContext = "from " + test.plane + ";"
				}
				if !connector.failed || errors.Is(err, ErrInvalidStore) != failure.invalidStore || !errors.Is(err, failure.cause) || !strings.Contains(err.Error(), planeContext) {
					t.Fatalf("freshness error lost classification, plane or cause: reached=%t err=%v", connector.failed, err)
				}
				if repairAdvice := strings.Contains(err.Error(), "use a fully initialized fresh graph workspace"); repairAdvice != failure.invalidStore {
					t.Fatalf("freshness error has wrong repair advice: %v", err)
				}
			})
		}
	}
}

func TestLegacyImportInterruptedInspection(t *testing.T) {
	for _, cause := range []error{context.Canceled, context.DeadlineExceeded} {
		err := legacyTargetInspectionError(t.Context(), "config.issue_prefix", cause)
		if !errors.Is(err, cause) || errors.Is(err, ErrInvalidStore) || !strings.Contains(err.Error(), "config.issue_prefix") || strings.Contains(err.Error(), "repair schema") {
			t.Fatalf("prefix inspection misdiagnosed interrupted query: %v", err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	cause := errors.New("driver interrupted during scan")
	err := legacyTargetInspectionError(ctx, "config.issue_prefix", cause)
	if !errors.Is(err, context.Canceled) || !errors.Is(err, cause) || errors.Is(err, ErrInvalidStore) || strings.ContainsAny(err.Error(), "\r\n") {
		t.Fatalf("interrupted inspection lost context or driver cause: %v", err)
	}
	wrappedContext := fmt.Errorf("scan: %w", context.Canceled)
	err = legacyTargetInspectionError(ctx, "config.issue_prefix", wrappedContext)
	if !errors.Is(err, wrappedContext) || !errors.Is(err, context.Canceled) || errors.Is(err, ErrInvalidStore) || strings.Count(err.Error(), context.Canceled.Error()) != 1 || strings.ContainsAny(err.Error(), "\r\n") {
		t.Fatalf("interrupted inspection duplicated an existing context cause: %v", err)
	}
	if got := legacyTargetInspectionError(t.Context(), "config.issue_prefix", sql.ErrNoRows); !errors.Is(got, ErrInvalidStore) || !errors.Is(got, sql.ErrNoRows) || !strings.Contains(got.Error(), "Issue prefix configuration is missing or unreadable") {
		t.Fatalf("missing prefix lost its configuration diagnostic: %v", got)
	}
}

// Keep additions to native input structs from bypassing original-string
// validation before the JSON clone can replace malformed UTF-8. A new shape
// must deliberately extend admission rather than silently taking the default.
func TestLegacyImportUTF8ShapeCoverage(t *testing.T) {
	seen := map[reflect.Type]bool{}
	var check func(reflect.Type, string)
	check = func(shape reflect.Type, path string) {
		if shape == reflect.TypeOf(json.RawMessage{}) {
			if path != "Batch.Issues[].Metadata" {
				t.Errorf("new raw JSON field needs original-byte validation: %s", path)
			}
			return // Issue.Metadata is explicitly checked before cloning.
		}
		if seen[shape] {
			return
		}
		seen[shape] = true
		switch shape.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
			return
		case reflect.Pointer:
			check(shape.Elem(), path)
		case reflect.Struct:
			for i := 0; i < shape.NumField(); i++ {
				field := shape.Field(i)
				if field.PkgPath == "" {
					check(field.Type, path+"."+field.Name)
				}
			}
		case reflect.Slice:
			if shape.Elem().Kind() == reflect.Uint8 {
				t.Errorf("new byte field needs explicit admission: %s", path)
				return
			}
			check(shape.Elem(), path+"[]")
		default:
			t.Errorf("new input shape needs UTF-8 admission coverage: %s (%s)", path, shape)
		}
	}
	check(reflect.TypeOf(legacyimport.Batch{}), "Batch")
}

// JSON cloning deliberately discards only classified SDK-internal state.
// New omitted fields need an explicit preserve/refuse/derived disposition,
// including fields added to nested input records rather than only Issue.
func TestLegacyImportJSONCloneOmissionCoverage(t *testing.T) {
	policies := map[string]string{
		"Batch.Issues[].ContentHash":       "derived content cache; recomputed locally",
		"Batch.Issues[].RowVersion":        "source-local concurrency counter",
		"Batch.Issues[].SourceRepo":        "refused cross-repository routing",
		"Batch.Issues[].IDPrefix":          "refused allocation override",
		"Batch.Issues[].PrefixOverride":    "refused allocation override",
		"Batch.Issues[].WispPlaneOverride": "refused storage-plane override",
		"Batch.Issues[].IsLitePartial":     "refused partial record",
	}
	found := map[string]bool{}
	active := map[reflect.Type]bool{}
	var check func(reflect.Type, string)
	check = func(shape reflect.Type, path string) {
		if active[shape] {
			t.Errorf("recursive input shape needs clone admission review: %s (%s)", path, shape)
			return
		}
		active[shape] = true
		defer delete(active, shape)
		switch shape.Kind() {
		case reflect.String, reflect.Bool, reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64,
			reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64, reflect.Float32, reflect.Float64:
			return
		case reflect.Pointer:
			check(shape.Elem(), path)
		case reflect.Slice, reflect.Array:
			check(shape.Elem(), path+"[]")
		case reflect.Map:
			check(shape.Elem(), path+"{}")
		case reflect.Struct:
			for i := 0; i < shape.NumField(); i++ {
				field := shape.Field(i)
				if field.PkgPath != "" {
					continue
				}
				fieldPath := path + "." + field.Name
				if field.Tag.Get("json") == "-" {
					found[fieldPath] = true
					if policies[fieldPath] == "" {
						t.Errorf("JSON clone would discard an unclassified input field: %s", fieldPath)
					}
					continue
				}
				check(field.Type, fieldPath)
			}
		default:
			t.Errorf("new input shape needs clone-omission coverage: %s (%s)", path, shape)
		}
	}
	check(reflect.TypeOf(legacyimport.Batch{}), "Batch")
	refusals := legacyImportOmittedStateFixtures()
	for path, policy := range policies {
		if !found[path] {
			t.Errorf("stale clone-omission policy: %s", path)
		}
		if path == "Batch.Issues[].ContentHash" || path == "Batch.Issues[].RowVersion" {
			continue // Deliberate omission control below.
		}
		field := strings.TrimPrefix(path, "Batch.Issues[].")
		input := refusals[field]
		if !strings.HasPrefix(policy, "refused ") || input == nil {
			t.Errorf("omission policy needs a behavioral refusal fixture: %s", path)
			continue
		}
		input.ID, input.Title = "old-a", "Refused SDK state"
		if _, err := prepareLegacyImport(legacyimport.Batch{Issues: []*types.Issue{input}}, "importer"); !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), field) {
			t.Errorf("omitted field %s was not explicitly refused: %v", path, err)
		}
		delete(refusals, field)
	}
	for field := range refusals {
		t.Errorf("refusal fixture lacks a clone-omission policy: %s", field)
	}
	// Source-local caches may be omitted or recomputed, but must not survive
	// as authoritative input. Do not pin the timing of derived computation.
	input := parseLegacyFixture(t, `{"id":"old-a","title":"Derived source state"}`)
	input.Issues[0].ContentHash = "source-cache"
	input.Issues[0].RowVersion = 23
	prepared, err := prepareLegacyImport(input, "importer")
	if err != nil {
		t.Fatal(err)
	}
	clean, err := prepareLegacyImport(parseLegacyFixture(t, `{"id":"old-a","title":"Derived source state"}`), "importer")
	if err != nil {
		t.Fatal(err)
	}
	if prepared.Issues[0].ContentHash != clean.Issues[0].ContentHash || prepared.Issues[0].RowVersion == 23 || input.Issues[0].ContentHash != "source-cache" || input.Issues[0].RowVersion != 23 {
		t.Fatalf("derived state disposition or caller isolation changed: %+v %+v", prepared.Issues[0], input.Issues[0])
	}
}

const legacyFreshnessChangedEpochQuery = "SELECT COUNT(*) FROM store_epoch WHERE id<>1 OR epoch<>1 OR bumped_at IS NOT NULL OR bumped_reason IS NOT NULL"

// These are prefiltered SQL COUNT results. The engine matrix separately tests
// the actual absent/pristine singleton and each bumped-field predicate.
func TestLegacyImportFreshnessPlaneAdmission(t *testing.T) {
	for _, test := range []struct {
		name, occupiedQuery, plane string
	}{
		{"absent-or-pristine", "", ""},
		{"minted-address", "SELECT COUNT(*) FROM epoch_minted_addresses", "epoch_minted_addresses"},
		{"interaction", "SELECT COUNT(*) FROM interactions", "interactions"},
		{"changed-epoch", legacyFreshnessChangedEpochQuery, "store_epoch"},
	} {
		t.Run(test.name, func(t *testing.T) {
			connector := &legacyFreshnessCountDriver{counts: map[string]int64{}, seen: map[string]bool{}}
			if test.occupiedQuery != "" {
				connector.counts[test.occupiedQuery] = 1
			}
			db := sql.OpenDB(connector)
			defer func() {
				if err := db.Close(); err != nil {
					t.Error(err)
				}
			}()
			tx, err := db.BeginTx(t.Context(), nil)
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := tx.Rollback(); err != nil {
					t.Error(err)
				}
			}()
			err = requireEmptyLegacyTarget(t.Context(), tx)
			if test.occupiedQuery == "" {
				if err != nil || !connector.seen["SELECT COUNT(*) FROM epoch_minted_addresses"] || !connector.seen[legacyFreshnessChangedEpochQuery] {
					t.Fatalf("fresh epoch baseline refused or unchecked: %v", err)
				}
			} else if !connector.seen[test.occupiedQuery] || !errors.Is(err, storage.ErrValidation) || !strings.Contains(err.Error(), "; "+test.plane+" is occupied") {
				t.Fatalf("occupied epoch plane admitted or misclassified: %v", err)
			}
		})
	}
}

type legacyFreshnessCountDriver struct {
	query  string
	cause  error
	failed bool
	counts map[string]int64
	seen   map[string]bool
}

func (d *legacyFreshnessCountDriver) Connect(context.Context) (driver.Conn, error) {
	return &legacyFreshnessCountConn{source: d}, nil
}
func (d *legacyFreshnessCountDriver) Driver() driver.Driver { return d }
func (d *legacyFreshnessCountDriver) Open(string) (driver.Conn, error) {
	return d.Connect(context.Background())
}

type legacyFreshnessCountConn struct{ source *legacyFreshnessCountDriver }

func (*legacyFreshnessCountConn) Prepare(string) (driver.Stmt, error) {
	return nil, errors.New("freshness count double does not prepare statements")
}
func (*legacyFreshnessCountConn) Close() error              { return nil }
func (*legacyFreshnessCountConn) Begin() (driver.Tx, error) { return legacyFreshnessCountTx{}, nil }
func (c *legacyFreshnessCountConn) QueryContext(_ context.Context, query string, _ []driver.NamedValue) (driver.Rows, error) {
	if c.source.seen != nil {
		c.source.seen[query] = true
	}
	if query == c.source.query {
		c.source.failed = true
		return nil, c.source.cause
	}
	if !strings.HasPrefix(query, "SELECT COUNT(*) FROM ") {
		return nil, fmt.Errorf("unexpected freshness query %q", query)
	}
	return &legacyFreshnessCountRows{count: c.source.counts[query]}, nil
}

type legacyFreshnessCountTx struct{}

func (legacyFreshnessCountTx) Commit() error   { return nil }
func (legacyFreshnessCountTx) Rollback() error { return nil }

type legacyFreshnessCountRows struct {
	read  bool
	count int64
}

func (*legacyFreshnessCountRows) Columns() []string { return []string{"count"} }
func (*legacyFreshnessCountRows) Close() error      { return nil }
func (r *legacyFreshnessCountRows) Next(values []driver.Value) error {
	if r.read {
		return io.EOF
	}
	r.read = true
	values[0] = r.count
	return nil
}
