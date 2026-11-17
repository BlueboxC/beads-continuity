package formula

import (
	"errors"
	"fmt"
	"slices"
	"strings"
	"testing"
)

// Contributor regression from #8008.
func TestFilterStepsByConditionRejectsDroppedReferences(t *testing.T) {
	off := map[string]string{"fanout_enabled": "false"}
	tests := []struct {
		name     string
		steps    []*Step
		vars     map[string]string
		wantErrs []string // substrings of the error; nil means no error
		wantIDs  []string // kept step IDs (top level) when no error
	}{
		{
			name: "kept needs to a dropped sibling",
			steps: []*Step{
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "consumer", Needs: []string{"fanout"}},
			},
			vars: off,
			wantErrs: []string{
				`step "consumer": needs references "fanout", but condition "{{fanout_enabled}}" on step "fanout" drops it from this pour`,
				`give "consumer" the same condition`,
			},
		},
		{
			name: "kept depends_on to a dropped sibling",
			steps: []*Step{
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "consumer", DependsOn: []string{"fanout"}},
			},
			vars: off,
			wantErrs: []string{
				`step "consumer": depends_on references "fanout", but condition "{{fanout_enabled}}" on step "fanout" drops it from this pour`,
			},
		},
		{
			name: "kept child needs a step dropped through its parent's condition",
			steps: []*Step{
				{ID: "phase", Condition: "{{fanout_enabled}}", Children: []*Step{{ID: "inner"}}},
				{ID: "outer", Children: []*Step{{ID: "consumer", Needs: []string{"inner"}}}},
			},
			vars: off,
			wantErrs: []string{
				`step "consumer": needs references "inner", but condition "{{fanout_enabled}}" on step "phase" drops it from this pour`,
			},
		},
		{
			name: "inferred waits_for spawner is dropped",
			steps: []*Step{
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "gate", Needs: []string{"fanout"}, WaitsFor: "all-children"},
			},
			vars: off,
			wantErrs: []string{
				`step "gate": waits_for "all-children" waits for the children of "fanout", but condition "{{fanout_enabled}}" on step "fanout" drops it from this pour`,
				`give "gate" the same condition, or name a spawner that is always poured`,
			},
		},
		{
			name: "explicit children-of spawner is dropped while needs[0] is kept",
			steps: []*Step{
				{ID: "start"},
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "gate", Needs: []string{"start"}, WaitsFor: "children-of(fanout)"},
			},
			vars: off,
			wantErrs: []string{
				`step "gate": waits_for "children-of(fanout)" waits for the children of "fanout", but condition "{{fanout_enabled}}" on step "fanout" drops it from this pour`,
			},
		},
		{
			name: "waits_for spawner dropped through its parent's condition",
			steps: []*Step{
				{ID: "phase", Condition: "{{fanout_enabled}}", Children: []*Step{{ID: "fanout"}}},
				{ID: "start"},
				{ID: "gate", Needs: []string{"start"}, WaitsFor: "children-of(fanout)"},
			},
			vars: off,
			wantErrs: []string{
				`step "gate": waits_for "children-of(fanout)" waits for the children of "fanout", but condition "{{fanout_enabled}}" on step "phase" drops it from this pour`,
			},
		},
		{
			name: "gate sharing the spawner's condition is dropped with it",
			steps: []*Step{
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "gate", Needs: []string{"fanout"}, WaitsFor: "all-children", Condition: "{{fanout_enabled}}"},
				{ID: "after"},
			},
			vars:    off,
			wantIDs: []string{"after"},
		},
		{
			name: "kept consumer of a gate dropped with its spawner",
			steps: []*Step{
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "gate", Needs: []string{"fanout"}, WaitsFor: "all-children", Condition: "{{fanout_enabled}}"},
				{ID: "consumer", Needs: []string{"gate"}},
			},
			vars: off,
			wantErrs: []string{
				`step "consumer": needs references "gate", but condition "{{fanout_enabled}}" on step "gate" drops it from this pour (give "consumer" the same condition`,
			},
		},
		{
			name: "two broken references are listed in one error",
			steps: []*Step{
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "a", Needs: []string{"fanout"}},
				{ID: "b", DependsOn: []string{"fanout"}},
			},
			vars: off,
			wantErrs: []string{
				`step "a": needs references "fanout"`,
				`step "b": depends_on references "fanout"`,
			},
		},
		{
			name: "consumer sharing the condition is dropped with it",
			steps: []*Step{
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "consumer", Needs: []string{"fanout"}, Condition: "{{fanout_enabled}}"},
				{ID: "after"},
			},
			vars:    off,
			wantIDs: []string{"after"},
		},
		{
			name: "unreferenced dropped leaf",
			steps: []*Step{
				{ID: "build"},
				{ID: "test", Needs: []string{"build"}},
				{ID: "deploy", Needs: []string{"test"}, Condition: "{{deploy}} == true"},
			},
			vars:    map[string]string{"deploy": "false"},
			wantIDs: []string{"build", "test"},
		},
		{
			name: "all conditions true",
			steps: []*Step{
				{ID: "fanout", Condition: "{{fanout_enabled}}"},
				{ID: "consumer", Needs: []string{"fanout"}, DependsOn: []string{"fanout"}},
			},
			vars:    map[string]string{"fanout_enabled": "true"},
			wantIDs: []string{"fanout", "consumer"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			before := stepTreeSnapshot(tt.steps)
			result, err := FilterStepsByCondition(tt.steps, tt.vars)
			if after := stepTreeSnapshot(tt.steps); after != before {
				t.Errorf("input steps modified:\nbefore %s\nafter  %s", before, after)
			}
			if tt.wantErrs == nil {
				if err != nil {
					t.Fatalf("FilterStepsByCondition() error = %v, want nil", err)
				}
				gotIDs := make([]string, 0, len(result))
				for _, s := range result {
					gotIDs = append(gotIDs, s.ID)
				}
				if !slices.Equal(gotIDs, tt.wantIDs) {
					t.Errorf("FilterStepsByCondition() IDs = %v, want %v", gotIDs, tt.wantIDs)
				}
				return
			}
			if err == nil {
				t.Fatalf("FilterStepsByCondition() = %d steps, nil error; want an error naming %q", len(result), tt.wantErrs)
			}
			if result != nil {
				t.Errorf("FilterStepsByCondition() returned %d steps alongside the error, want nil", len(result))
			}
			// The fork's ErrVarValidation is reserved for variable values.
			// A retained dependency is a formula error, distinct from a
			// failed lookup or invalid --var input.
			if errors.Is(err, ErrVarValidation) || strings.Contains(strings.ToLower(err.Error()), "not found") {
				t.Errorf("wrong error classification for a dropped dependency: %v", err)
			}
			for _, want := range tt.wantErrs {
				if !strings.Contains(err.Error(), want) {
					t.Errorf("error %q does not contain %q", err, want)
				}
			}
		})
	}
}

func stepTreeSnapshot(steps []*Step) string {
	var b strings.Builder
	for _, s := range steps {
		fmt.Fprintf(&b, "{%s cond=%q needs=%v deps=%v waits=%q children=%s}",
			s.ID, s.Condition, s.Needs, s.DependsOn, s.WaitsFor, stepTreeSnapshot(s.Children))
	}
	return b.String()
}
