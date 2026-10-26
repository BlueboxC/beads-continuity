package versioncontrolops

import "errors"

// CheckFederationExclusions rejects the historical snapshot filter before any
// side effect: its deletion commit can merge back, and ancestors keep the rows.
func CheckFederationExclusions(excluded []string) error {
	if len(excluded) == 0 {
		return nil
	}
	return errors.New("federation.exclude_types cannot filter safely: excluded rows remain in history and filtered deletions can merge back; sync refused before any commit or transfer. Review existing peers and history before changing exclusions; an empty list explicitly selects unfiltered sync")
}
