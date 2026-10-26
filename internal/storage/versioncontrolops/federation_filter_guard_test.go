package versioncontrolops

import "testing"

func TestFederationExclusionsRequireExplicitUnfilteredSync(t *testing.T) {
	for _, tc := range []struct {
		name     string
		excluded []string
		refuse   bool
	}{
		{"unset", nil, false},
		{"explicit empty", []string{}, false},
		{"default wisp", []string{"wisp"}, true},
		{"custom type", []string{"private"}, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := CheckFederationExclusions(tc.excluded); (got != nil) != tc.refuse {
				t.Fatalf("exclusion admission: %v", got)
			}
		})
	}
}
