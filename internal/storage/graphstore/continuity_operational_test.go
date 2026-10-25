//go:build cgo

package graphstore

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/activity"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/internal/knowledge"
	"github.com/steveyegge/beads/memoryops"
)

func TestOperationalContinuitySelectsCurrentIndexAndCitedOrigins(t *testing.T) {
	ctx, o := issueExperimentOptions(t, "embedded")
	s := openVersionsStore(t, ctx, o)
	m := s.Memories("fixture")
	if _, e := m.Remember(ctx, memoryops.RememberRequest{Key: "objective", Content: "preserved objective"}); e != nil {
		t.Fatal(e)
	}
	if _, e := m.Remember(ctx, memoryops.RememberRequest{Key: activity.SettingsKey, Content: `{"version":1,"enabled":true}`}); e != nil {
		t.Fatal(e)
	}
	index := codeindex.Index{Version: 3, Languages: []string{"go"}, Roots: []string{"src"}, Files: []codeindex.File{{Path: "src/a.go", Language: "go", SHA256: strings.Repeat("a", 64), Symbols: []codeindex.Symbol{{ID: "src/a.go::Solved", Name: "Solved", Kind: "function"}}}}}
	if _, e := codeindex.Save(ctx, m, map[string]string{}, index); e != nil {
		t.Fatal(e)
	}
	root := t.TempDir()
	text := []byte("package fixture\n")
	if e := os.WriteFile(filepath.Join(root, "a.go"), text, 0600); e != nil {
		t.Fatal(e)
	}
	event, e := activity.Build(activity.Input{CWD: root, SessionID: "operational-session", TurnID: "operational-turn", Event: "Stop", LastAssistantMessage: "Reported fixture only"}, root, time.Now())
	if e != nil {
		t.Fatal(e)
	}
	if _, e = activity.Save(ctx, m, event); e != nil {
		t.Fatal(e)
	}
	all, e := m.List(ctx, memoryops.ListRequest{})
	if e != nil {
		t.Fatal(e)
	}
	refs, e := activity.SelectReferences(all.Memories, []string{event.ID})
	if e != nil {
		t.Fatal(e)
	}
	reader, e := knowledge.Open(root)
	if e != nil {
		t.Fatal(e)
	}
	defer reader.Close()
	proposal, e := reader.PrepareActivityProposal(knowledge.Record{ID: "operational-origin", Kind: "solution", Summary: "source-backed fixture", Scope: "recorded", Sources: []knowledge.Source{{Path: "a.go", SHA256: activity.Digest(text)}}}, "", knowledge.State{}, refs)
	if e != nil {
		t.Fatal(e)
	}
	if _, e = knowledge.SaveProposal(ctx, m, proposal); e != nil {
		t.Fatal(e)
	}
	// The journal exceeds ordinary enumeration; unrelated corrupt old code is retained.
	for start := 0; start < PreviewContinuityReadRowLimit+1; start += 256 {
		values := []string{}
		args := []any{}
		for n := start; n < start+256 && n < PreviewContinuityReadRowLimit+1; n++ {
			values = append(values, "(?,?)")
			args = append(args, fmt.Sprintf("kv.memory.@activity/event/unused-%06d", n), "{}")
		}
		if _, e = s.db.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES "+strings.Join(values, ","), args...); e != nil {
			t.Fatal(e)
		}
	}
	if _, e = s.db.ExecContext(ctx, "INSERT INTO config (`key`,value) VALUES ('kv.memory.@knowledge/code/blob/obsolete','corrupt old generation')"); e != nil {
		t.Fatal(e)
	}
	var before string
	if e = s.db.QueryRowContext(ctx, "SELECT DOLT_HASHOF_TABLE('config')").Scan(&before); e != nil {
		t.Fatal(e)
	}
	if _, e = m.List(ctx, memoryops.ListRequest{}); !errors.Is(e, ErrLimitExceeded) {
		t.Fatalf("ordinary List budget changed: %v", e)
	}
	plane, got, e := m.ReadCodeContinuity(ctx)
	if e != nil {
		t.Fatal(e)
	}
	expected, e := codeindex.Load(all.Memories)
	if e != nil {
		t.Fatal(e)
	}
	if !reflect.DeepEqual(got, expected) || plane.Memories["objective"] != "preserved objective" {
		t.Fatal("current index/human plane changed")
	}
	if _, found := plane.Memories[codeindex.Prefix+"blob/obsolete"]; found {
		t.Fatal("retained obsolete blob loaded")
	}
	state := knowledge.Decode(plane.Memories)
	if len(state.Proposals) != 1 || state.Proposals[0].ActivityValidity != "current" {
		t.Fatal("cited raw origin lost", state.Proposals)
	}
	learning, view, e := m.ReadLearningContinuity(ctx, "operational-session", "operational-turn", nil)
	if e != nil {
		t.Fatal(e)
	}
	packet, e := reader.PrepareLearning(learning.Memories, []string{"a.go"}, "operational-session", "operational-turn", nil)
	if e != nil {
		t.Fatal(e)
	}
	if view.Total != 1 || view.Invalid != PreviewContinuityReadRowLimit+1 || len(packet.Draft.ActivityEvents) != 1 || packet.Draft.ActivityEvents[0] != refs[0] {
		t.Fatal("session counts or raw origin changed", view.Total, view.Invalid)
	}
	if _, e = m.ReadKnowledgeContinuity(ctx, []string{"invalid"}); !errors.Is(e, memoryops.ErrValidation) {
		t.Fatal("invalid origin accepted", e)
	}
	var after string
	if e = s.db.QueryRowContext(ctx, "SELECT DOLT_HASHOF_TABLE('config')").Scan(&after); e != nil || after != before {
		t.Fatal("read mutated retained data", e)
	}
	key := "kv.memory." + activity.Prefix + "event/" + event.ID
	if _, e = s.db.ExecContext(ctx, "UPDATE config SET value=CONCAT(value,' ') WHERE `key`=?", key); e != nil {
		t.Fatal(e)
	}
	selected, e := m.ReadKnowledgeContinuity(ctx, nil)
	if e != nil {
		t.Fatal(e)
	}
	if knowledge.Decode(selected.Memories).Proposals[0].ActivityValidity != "needs_review" {
		t.Fatal("changed original bytes renewed provenance")
	}
	if _, e = s.db.ExecContext(ctx, "UPDATE config SET value='broken current manifest' WHERE `key`=?", "kv.memory."+codeindex.ManifestKey); e != nil {
		t.Fatal(e)
	}
	partial, invalid, e := m.ReadCodeContinuity(ctx)
	if e == nil || partial.Memories != nil || invalid.Version != 0 {
		t.Fatal("corrupt current generation returned partial success")
	}
}
