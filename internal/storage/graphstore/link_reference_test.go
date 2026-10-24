package graphstore

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
)

func TestEndpointReferenceJSONRetainsPinSeparately(t *testing.T) {
	for _, reference := range []EndpointReference{{URI: "https://example.test/beads/a@literal"}, {URI: "https://example.test/beads/a@literal", Revision: "opaque:@/token"}} {
		raw, err := json.Marshal(reference)
		if err != nil {
			t.Fatal(err)
		}
		var round EndpointReference
		if err := json.Unmarshal(raw, &round); err != nil || round != reference {
			t.Fatalf("roundtrip %s: %+v %v", raw, round, err)
		}
		if reference.Revision != "" && (!strings.Contains(string(raw), `"uri":`) || !strings.Contains(string(raw), `"revision":`)) {
			t.Fatal("pin not structured")
		}
	}
}

func TestEndpointReferenceRejectsMalformedPinnedArm(t *testing.T) {
	for _, raw := range []string{`null`, `42`, `{}`, `{"uri":"x"}`, `{"uri":"x","revision":""}`, `{"uri":"x","revision":null}`, `{"uri":"x","revision":"a","extra":0}`, `{"uri":"x","revision":"a","revision":"b"}`} {
		var reference EndpointReference
		if err := json.Unmarshal([]byte(raw), &reference); err == nil {
			t.Errorf("accepted %s", raw)
		}
	}
}

func TestLinkRecordJSONKeepsBothEndpointPins(t *testing.T) {
	original := LinkRecord{ID: "https://example.test/links/l", Type: "https://example.test/types/references", Revision: "r1", Version: "r1", Source: "https://example.test/beads/source", Target: "https://example.test/beads/target", SourceRevision: "source-v1", TargetRevision: "target-v2", Properties: map[string]any{}, Metadata: json.RawMessage(`{}`)}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	var round LinkRecord
	if err := json.Unmarshal(raw, &round); err != nil || !reflect.DeepEqual(original, round) {
		t.Fatalf("lost endpoint state: %s %+v %v", raw, round, err)
	}
	original.SourceRevision = ""
	original.TargetRevision = ""
	raw, err = json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(raw), `"source":"https://example.test/beads/source"`) || strings.Contains(string(raw), `"sourceRevision"`) {
		t.Fatalf("floating/native shape changed: %s", raw)
	}
}
