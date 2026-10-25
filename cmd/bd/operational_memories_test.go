package main

import (
	"context"
	"errors"
	"github.com/steveyegge/beads/internal/activity"
	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/memoryops"
	"reflect"
	"testing"
)

var _ codeContinuityReader = graphContinuityRole{}
var _ interface {
	ReadKnowledgeContinuity(context.Context, []string) (memoryops.ListResult, error)
} = graphContinuityRole{}
var _ interface {
	ReadLearningContinuity(context.Context, string, string, []string) (memoryops.ListResult, activity.View, error)
} = graphContinuityRole{}
var _ interface {
	ReadObservedActivity(context.Context, string, string, int) (activity.View, error)
} = graphContinuityRole{}

type operationalMemoryProbe struct {
	maintenanceMemories
	codeReads, knowledgeReads, learningReads int
	selected                                 []string
	session, turn                            string
	failure                                  error
}

func (m *operationalMemoryProbe) ReadCodeContinuity(context.Context) (memoryops.ListResult, codeindex.Index, error) {
	m.codeReads++
	return memoryops.ListResult{Memories: m.rows}, codeindex.Index{Version: 3}, m.failure
}
func (m *operationalMemoryProbe) ReadKnowledgeContinuity(_ context.Context, ids []string) (memoryops.ListResult, error) {
	m.knowledgeReads++
	m.selected = append([]string(nil), ids...)
	return memoryops.ListResult{Memories: m.rows}, m.failure
}
func (m *operationalMemoryProbe) ReadLearningContinuity(_ context.Context, s, t string, ids []string) (memoryops.ListResult, activity.View, error) {
	m.learningReads++
	m.session, m.turn = s, t
	m.selected = append([]string(nil), ids...)
	return memoryops.ListResult{Memories: m.rows}, activity.View{Total: 50}, m.failure
}
func TestOperationalMemoryProfilesDispatchAndPreserveErrors(t *testing.T) {
	m := &operationalMemoryProbe{maintenanceMemories: maintenanceMemories{rows: map[string]string{"human": "kept"}}}
	plane, index, err := readCodeMemories(t.Context(), m)
	if err != nil || index.Version != 3 || plane.Memories["human"] != "kept" || m.codeReads != 1 {
		t.Fatal("selected code profile not used", err)
	}
	ids := []string{"exact-origin"}
	_, err = readKnowledgeMemories(t.Context(), m, ids)
	if err != nil || m.knowledgeReads != 1 || !reflect.DeepEqual(m.selected, ids) {
		t.Fatal("knowledge origins not forwarded", err)
	}
	_, observed, err := readLearningMemories(t.Context(), m, "session", "turn", ids)
	if err != nil || observed == nil || observed.Total != 50 || m.session != "session" || m.turn != "turn" {
		t.Fatal("learning selection/counts lost", err)
	}
	m.failure = errors.New("selection failed")
	if _, _, err = readCodeMemories(t.Context(), m); !errors.Is(err, m.failure) {
		t.Fatal("code failure hidden", err)
	}
	if _, err = readKnowledgeMemories(t.Context(), m, nil); !errors.Is(err, m.failure) {
		t.Fatal("knowledge failure hidden", err)
	}
	if _, _, err = readLearningMemories(t.Context(), m, "session", "", nil); !errors.Is(err, m.failure) {
		t.Fatal("learning failure hidden", err)
	}
	if m.writes != 0 {
		t.Fatal("profile selection wrote data")
	}
}
