package graphstore

import (
	"strings"
	"testing"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/types"
	publicops "github.com/steveyegge/beads/issueops"
)

func TestNativeConcreteTypeIdentity(t *testing.T) {
	const scope = "https://example.test/work/"
	seen := map[string]string{}
	values := []string{"decision", "custom/task", "a b", "é", "e\u0301", "UPPER", "upper", strings.Repeat("x", types.MaxDependencyTypeLen)}
	for _, value := range types.AllIssueTypes {
		values = append(values, string(value))
	}
	for _, value := range types.WellKnownDependencyTypes() {
		values = append(values, string(value))
	}
	for _, value := range values {
		for _, build := range []func(string, string) (string, error){NativeIssueTypeURL, NativeDependencyTypeURL} {
			id, err := build(scope, value)
			if err != nil {
				t.Fatal(err)
			}
			base, got, ok := NativeTypeClassification(scope, id)
			if !ok || got != value {
				t.Fatalf("identity roundtrip %q: %q %t", value, got, ok)
			}
			key := base + "\x00" + value
			if previous, ok := seen[id]; ok && previous != key {
				t.Fatalf("identity collision: %q / %q", previous, key)
			}
			seen[id] = key
			if id == scope+"types/decision" || id == IssueTypeURL(scope) || id == DependencyTypeURL(scope) {
				t.Fatalf("child collides with existing identity: %s", id)
			}
			d, err := nativeConcreteDescriptor(scope, id)
			if err != nil {
				t.Fatal(err)
			}
			if d.ID() != id || len(d.ConformsTo()) != 1 || d.ConformsTo()[0] != base {
				t.Fatalf("descriptor identity/refinement differs: %s", d.CanonicalJSON())
			}
			want := graph.KindBead
			if base == DependencyTypeURL(scope) {
				want = graph.KindLink
			}
			if d.Describes() != want {
				t.Fatal("wrong resource kind")
			}
			parsed, err := graph.ParseTypeDescriptor(d.CanonicalJSON())
			if err != nil || parsed.Fingerprint() != d.Fingerprint() {
				t.Fatalf("closed descriptor roundtrip: %v", err)
			}
		}
	}
	// The mapper cannot silently claim workspace authorization. An arbitrary
	// custom Issue class still maps injectively; the native writer must admit it.
	if _, err := NativeIssueTypeURL(scope, "not-configured"); err != nil {
		t.Fatal(err)
	}
}

func TestNativeConcreteTypeIdentityRefusals(t *testing.T) {
	const scope = "https://example.test/work/"
	for _, value := range []string{"", string([]byte{0xff})} {
		if _, err := NativeIssueTypeURL(scope, value); err == nil {
			t.Fatalf("mapped invalid classification %q", value)
		}
		if _, err := NativeDependencyTypeURL(scope, value); err == nil {
			t.Fatalf("mapped invalid kind %q", value)
		}
	}
	for _, id := range []string{IssueTypeURL(scope), DependencyTypeURL(scope), scope + "types/decision", scope + "types/native-issue/", scope + "types/native-issue/6A", scope + "types/native-issue/ff", scope + "types/native-issue/6", scope + "types/native-issue/7461736b/00", "https://other.test/types/native-issue/7461736b"} {
		if _, _, ok := NativeTypeClassification(scope, id); ok {
			t.Fatalf("decoded noncanonical/foreign identity %q", id)
		}
	}
	if id, err := NativeIssueTypeURL(scope, strings.Repeat("é", 32)); err != nil || len(id) > len(scope)+512 {
		t.Fatalf("native Unicode column bound: %s %v", id, err)
	}
	if _, err := NativeIssueTypeURL(scope, strings.Repeat("x", 33)); err == nil {
		t.Fatal("unrepresentable Issue class accepted")
	}
	if _, err := NativeDependencyTypeURL(scope, strings.Repeat("é", 17)); err == nil {
		t.Fatal("Dependency byte bound relaxed")
	}
	if _, err := NativeIssueTypeURL("not-a-scope", "task"); err == nil {
		t.Fatal("invalid Scope accepted")
	}
}

func TestNativeIssuePropertyReplacementPreservesAffiliation(t *testing.T) {
	source := &types.Issue{Title: "Original", Priority: 2, IssueType: types.TypeTask}
	for _, tc := range []struct {
		name, raw string
		want      types.IssueType
		refuses   bool
	}{
		{name: "replacement-omission", raw: `[{"op":"replace","path":"","value":{"title":"Replacement","priority":1}}]`, want: types.TypeTask},
		{name: "setter-then-property-replacement", raw: `[{"op":"replace","path":"/issue_type","value":"bug"},{"op":"replace","path":"","value":{"title":"Replacement","priority":1}}]`, want: types.TypeBug},
		{name: "property-replacement-then-setter", raw: `[{"op":"replace","path":"","value":{"title":"Replacement","priority":1}},{"op":"replace","path":"/issue_type","value":"bug"}]`, want: types.TypeBug},
		{name: "explicit-remove", raw: `[{"op":"remove","path":"/issue_type"}]`, refuses: true},
		{name: "removed-affiliation-stays-invalid", raw: `[{"op":"remove","path":"/issue_type"},{"op":"replace","path":"","value":{"title":"Replacement","priority":1}}]`, refuses: true},
		{name: "required-title-still-required", raw: `[{"op":"replace","path":"","value":{"priority":1}}]`, refuses: true},
		{name: "required-priority-still-required", raw: `[{"op":"replace","path":"","value":{"title":"Replacement"}}]`, refuses: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			patch, err := historicalIssuePropertiesPatch(source, []byte(tc.raw), publicops.MetadataPatch{})
			if (err != nil) != tc.refuses {
				t.Fatalf("refusal=%t got %v", tc.refuses, err)
			}
			if err == nil && (!patch.IssueType.Set || patch.IssueType.Value != tc.want) {
				t.Fatalf("affiliation changed/dropped: %+v", patch.IssueType)
			}
			if source.IssueType != types.TypeTask || source.Title != "Original" {
				t.Fatal("patch parsing changed source")
			}
		})
	}
}
