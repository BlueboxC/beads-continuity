//go:build cgo

package main

import "testing"

func TestGraphOperationalProfileAdmission(t *testing.T) {
	bd := buildBDUnderTest(t)
	work, home := t.TempDir(), t.TempDir()
	graphPolicyCLI(t, bd, work, home, nil, "", "init", "--graph-mode", "link", "--scope-url", "https://example.invalid/capacity/", "--skip-hooks", "--skip-agents", "--non-interactive", "--json")
	before := legacyUpgradeTreeDigest(t, work)
	graphPolicyCLI(t, bd, work, home, nil, "invalid_selector", "--graph-profile", "unbounded", "status", "--graph", "--json")
	if after := legacyUpgradeTreeDigest(t, work); after != before {
		t.Fatal("invalid profile changed workspace")
	}
	graphPolicyCLI(t, bd, work, home, nil, "", "--graph-profile", "continuity-v1", "status", "--graph", "--json")
	// Profile selection is not a persisted policy or a format migration.
	graphPolicyCLI(t, bd, work, home, nil, "", "status", "--graph", "--json")
	graphPolicyCLI(t, bd, t.TempDir(), home, nil, "capability_unavailable", "--graph-profile", "continuity-v1", "status", "--json")
}
