package graphstore

import "fmt"

// AssessmentTypeAvailable separates authoring availability from the immutable
// installed Type catalog. Callers must still validate the Type and its kind.
func AssessmentTypeAvailable(scope, id string, enabled bool) bool {
	if enabled {
		return true
	}
	for _, name := range []string{"evaluation", "conformance", "addresses", "satisfies", "violates"} {
		if id == scope+"types/"+name {
			return false
		}
	}
	return true
}

// AssessmentsEnabled is the immutable policy captured when this Store opened.
func (s *Store) AssessmentsEnabled() bool { return s.options.AssessmentsEnabled }

func (s *Store) requireAssessmentAuthoring(id string) error {
	if !AssessmentTypeAvailable(s.ScopeURL(), id, s.options.AssessmentsEnabled) {
		return fmt.Errorf("%w: assessment Type authoring is disabled", ErrCapabilityUnavailable)
	}
	return nil
}
