package dolt

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/doltserver"
	"go.opentelemetry.io/otel"
	sdkmetric "go.opentelemetry.io/otel/sdk/metric"
	"go.opentelemetry.io/otel/sdk/metric/metricdata"
)

// A copied database can have a valid-looking sql-server.info next to an
// unlocked engine directory. The workspace's live endpoint alone does not
// establish that Dolt CLI subprocesses operate on this database.
func TestCLIDatabaseRequiresThatServerToServeThePhysicalRoot(t *testing.T) {
	clearPredicateEnv(t)
	const port = 3310
	beadsDir := managedWorkspace(t, port)
	dbPath := filepath.Join(beadsDir, "dolt")
	cliDir := filepath.Join(dbPath, "beads")
	local := filepath.Join(cliDir, ".dolt")
	if err := os.MkdirAll(filepath.Join(local, "noms"), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "noms", "LOCK"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	// A recorded process can still be live while this copy's lock is free.
	pid, err := os.ReadFile(filepath.Join(doltserver.ResolveServerDir(beadsDir), doltserver.PIDFileName))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(local, "sql-server.info"), []byte(strings.TrimSpace(string(pid))+":3310:fixture"), 0o600); err != nil {
		t.Fatal(err)
	}
	s := &DoltStore{
		serverMode: true, beadsDir: beadsDir, dbPath: dbPath, database: "beads",
		cfg: &Config{ServerHost: "127.0.0.1", ServerPort: port},
	}
	if s.hasCLIDatabase() {
		t.Error("CLI transfer admitted an unlocked copy instead of the server's verified physical root")
	}
}

func TestPoolGaugesTrackOpenStoreSet(t *testing.T) {
	reader := sdkmetric.NewManualReader()
	provider := sdkmetric.NewMeterProvider(sdkmetric.WithReader(reader))
	previous := otel.GetMeterProvider()
	otel.SetMeterProvider(provider)
	t.Cleanup(func() {
		otel.SetMeterProvider(previous)
		_ = provider.Shutdown(context.Background())
	})

	store := func(max int) *DoltStore {
		db, err := sql.Open("mysql", "root@tcp(127.0.0.1:1)/x")
		if err != nil {
			t.Fatal(err)
		}
		db.SetMaxOpenConns(max)
		s := &DoltStore{db: db}
		t.Cleanup(func() { _ = s.Close() })
		s.registerPoolGauges()
		return s
	}
	maxOpen := func() int64 {
		var samples metricdata.ResourceMetrics
		if err := reader.Collect(t.Context(), &samples); err != nil {
			t.Fatal(err)
		}
		var total int64
		for _, scope := range samples.ScopeMetrics {
			for _, instrument := range scope.Metrics {
				if instrument.Name != "bd.db.pool_max_open" {
					continue
				}
				gauge, ok := instrument.Data.(metricdata.Gauge[int64])
				if !ok {
					t.Fatalf("pool gauge has type %T", instrument.Data)
				}
				for _, sample := range gauge.DataPoints {
					total += sample.Value
				}
			}
		}
		return total
	}

	first := store(3)
	if n := maxOpen(); n != 3 {
		t.Errorf("one pool limit = %d, want 3", n)
	}
	_ = store(5)
	if n := maxOpen(); n != 8 {
		t.Errorf("two pools limit = %d, want 8", n)
	}
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if n := maxOpen(); n != 5 {
		t.Errorf("after closing first pool limit = %d, want 5", n)
	}
}
