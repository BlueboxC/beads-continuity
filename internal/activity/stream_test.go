package activity

import (
	"encoding/json"
	"fmt"
	"maps"
	"reflect"
	"testing"
	"time"
)

func TestStreamingRecentParityAndOldHandoffs(t *testing.T) {
	plane := map[string]string{SettingsKey: `{"version":1,"enabled":true}`, Prefix + "event/broken": `{}`}
	for i := range 80 {
		event := "PostToolUse"
		if i < 4 {
			event = "Stop"
		}
		e, err := Build(Input{SessionID: "session", TurnID: fmt.Sprintf("turn-%d", i), CWD: "/repo", Event: event, Tool: "read", ToolUseID: fmt.Sprintf("call-%d", i), LastAssistantMessage: "reported next step"}, "/repo", time.Unix(int64(i+1), 0))
		if err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(e)
		if err != nil {
			t.Fatal(err)
		}
		plane[Prefix+"event/"+e.ID] = string(raw)
	}
	for _, summaries := range []bool{false, true} {
		kind := ""
		if summaries {
			kind = "turn_end"
		}
		want := readRecent(plane, "session", "", 3, kind)
		got := ReadRows(plane[SettingsKey], maps.All(plane), "session", "", 3, summaries)
		if !reflect.DeepEqual(got, want) || got.Total != 80 || got.Invalid != 1 || len(got.Events) != 3 {
			t.Fatalf("stream differs: %+v", got)
		}
	}
	if got := FormatContext(ReadRows(plane[SettingsKey], maps.All(plane), "", "", 3, true)); got != Context(plane) {
		t.Fatal("stream lost older handoffs or full counts")
	}
}
