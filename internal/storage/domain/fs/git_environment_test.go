package fs

import (
	"os"
	"testing"

	"github.com/steveyegge/beads/internal/gittestenv"
)

func TestMain(m *testing.M) {
	gittestenv.Clear()
	os.Exit(m.Run())
}
