//go:build cgo

package embeddeddolt

import (
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/storage/versioncontrolops"
	"github.com/steveyegge/beads/internal/types"
	"os"
	"path/filepath"
	"testing"
)

// TestSetCommitIdentity pins GH#7628's resolution contract directly against
// the package-level state buildDSN and commitAuthor read, without needing a
// real embedded database: SetCommitIdentity overrides both, an empty name or
// email is a no-op (never adopting a half-resolved identity), and
// commitAuthor composes the current pair fresh on every call.
func TestSetCommitIdentity(t *testing.T) {
	t.Cleanup(func() { SetCommitIdentity("beads", "beads@local") })

	if got := commitAuthor(); got != "beads <beads@local>" {
		t.Fatalf("commitAuthor() before any override = %q, want the historical beads <beads@local>", got)
	}

	SetCommitIdentity("Alice Author", "alice@example.com")
	if got := commitAuthor(); got != "Alice Author <alice@example.com>" {
		t.Fatalf("commitAuthor() after SetCommitIdentity = %q, want %q", got, "Alice Author <alice@example.com>")
	}
	if defaultAuthorName != "Alice Author" || defaultAuthorEmail != "alice@example.com" {
		t.Fatalf("defaultAuthorName/Email = %q/%q, want the overridden pair", defaultAuthorName, defaultAuthorEmail)
	}

	SetCommitIdentity("", "bob@example.com")
	if got := commitAuthor(); got != "Alice Author <alice@example.com>" {
		t.Fatalf("commitAuthor() after an empty-name call = %q, want the prior override unchanged (empty name must no-op)", got)
	}

	SetCommitIdentity("Bob Author", "")
	if got := commitAuthor(); got != "Alice Author <alice@example.com>" {
		t.Fatalf("commitAuthor() after an empty-email call = %q, want the prior override unchanged (empty email must no-op)", got)
	}

	SetCommitIdentity("Carol Author", "carol@example.com")
	if got := commitAuthor(); got != "Carol Author <carol@example.com>" {
		t.Fatalf("commitAuthor() after a second full override = %q, want %q", got, "Carol Author <carol@example.com>")
	}
}

func TestForkEmbeddedCommitIdentity(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1 for real embedded identity persistence")
	}
	SetCommitIdentity("Alice Author", "alice@example.com")
	t.Cleanup(func() { SetCommitIdentity("beads", "beads@local") })
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), ".beads"), "identity", "main")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err = s.SetConfig(ctx, "issue_prefix", "identity"); err != nil {
		t.Fatal(err)
	}
	if err = s.Commit(ctx, "identity DSN"); err != nil {
		t.Fatal(err)
	}
	check := func(message string) {
		t.Helper()
		err := s.withDBConn(ctx, func(db versioncontrolops.DBConn) error {
			var name, email string
			if err := db.QueryRowContext(ctx, "SELECT committer, email FROM dolt_log WHERE message = ?", message).Scan(&name, &email); err != nil {
				return err
			}
			if name != "Alice Author" || email != "alice@example.com" {
				t.Errorf("persisted author %q <%s>", name, email)
			}
			return nil
		})
		if err != nil {
			t.Fatal(err)
		}
	}
	check("identity DSN")
	err = s.RunInTransaction(ctx, "identity explicit", func(tx storage.Transaction) error {
		return tx.CreateIssue(ctx, &types.Issue{ID: "identity-1", Title: "identity proof", Status: types.StatusOpen, Priority: 2, IssueType: types.TypeTask}, "tester")
	})
	if err != nil {
		t.Fatal(err)
	}
	check("identity explicit")
}
