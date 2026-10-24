package graphstore

import (
	"errors"
	"github.com/steveyegge/beads/internal/storage"
	"testing"
)

func TestSchema8FrozenNoteRejectsNullAndForeignMembers(t *testing.T) {
	for _, raw := range []string{`null`, `[]`, `{"note":null}`, `{"note":3}`, `{"extra":"x"}`} {
		if err := validateSchema8LinkProperties([]byte(raw)); !errors.Is(err, storage.ErrValidation) {
			t.Fatalf("properties %s: %v", raw, err)
		}
	}
	for _, raw := range []string{`{}`, `{"note":""}`, `{"note":"retained"}`} {
		if err := validateSchema8LinkProperties([]byte(raw)); err != nil {
			t.Fatal(err)
		}
	}
}
