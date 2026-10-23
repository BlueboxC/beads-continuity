package main

import (
	"github.com/steveyegge/beads/internal/types"
	"strings"
	"testing"
)

func TestForkMirrorRemedyUsesPlaceholder(t *testing.T) {
	for _, tc := range []struct{ os, path string }{{"windows", `C:\Users\me\$work\planning`}, {"linux", "x'; marker #"}, {"windows", "x\u202e"}} {
		err := repoSyncMirrorRefusal(&types.Issue{ID: "mps-1", SourceRepo: tc.path}, "P", tc.os)
		if err == nil || !strings.Contains(err.Error(), "bd repo remove <path>") {
			t.Errorf("unsafe remedy: %v", err)
		}
	}
}
