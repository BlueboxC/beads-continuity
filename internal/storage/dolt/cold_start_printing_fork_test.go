package dolt

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DATA-DOG/go-sqlmock"
)

func TestForkColdStartDiagnosticEscapesPersistedValues(t *testing.T) {
	clearPredicateEnv(t)
	db, mock, err := sqlmock.New(sqlmock.QueryMatcherOption(sqlmock.QueryMatcherEqual))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	mock.ExpectQuery("SELECT name, url FROM dolt_remotes").WillReturnRows(sqlmock.NewRows([]string{"name", "url"}))
	const port = 3310
	beadsDir := managedWorkspace(t, port)
	s := &DoltStore{
		serverMode: true,
		beadsDir:   beadsDir,
		dbPath:     filepath.Join(beadsDir, "dolt"),
		database:   "fixture",
		cfg:        &Config{ServerHost: "127.0.0.1", ServerPort: port},
		db:         db,
	}
	markCLIDirServed(t, s.CLIDir(), port)
	stateDir := filepath.Join(s.CLIDir(), ".dolt")
	const name = "remote-$(printf should-not-run)"
	const url = "https://example.com/$(printf should-not-run)\n\x1b[2J"
	data, err := json.Marshal(map[string]interface{}{"remotes": map[string]interface{}{name: map[string]string{"url": url}}})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(stateDir, "repo_state.json")
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	useCLI, err := s.prepareCLIRouteForGitProtocol(context.Background(), name)
	if useCLI || err == nil {
		t.Fatalf("cold SQL route was not withheld: %v", err)
	}
	if !strings.Contains(err.Error(), "persisted on disk") {
		t.Fatalf("expected the cold-start diagnostic, got %q", err)
	}
	if strings.ContainsAny(err.Error(), "\n\x1b$") {
		t.Fatalf("persisted value printed live: %q", err)
	}
	after, readErr := os.ReadFile(path)
	if readErr != nil || string(after) != string(data) {
		t.Fatal("diagnostic changed persisted state")
	}
	if err := mock.ExpectationsWereMet(); err != nil {
		t.Fatal(err)
	}
}
