package knowledge

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestKnowledgeGrowthPreservesAssertionsAndReviewHistory(t *testing.T) {
	_, reader, record, memories := proposalFixture(t)
	for i := 0; i < 1025; i++ {
		item := record
		item.ID = fmt.Sprintf("legacy-%04d", i)
		if err := SaveRecord(t.Context(), memories, item); err != nil {
			t.Fatal(err)
		}
	}
	for i := 0; i < 1152; i++ {
		item := record
		item.ID = fmt.Sprintf("reviewed-%04d", i)
		p, err := reader.PrepareProposal(item, "", State{})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SaveProposal(t.Context(), memories, p); err != nil {
			t.Fatal(err)
		}
		decision, scope, evidence := "reject", "", ""
		if i%2 == 0 {
			decision, scope, evidence = "accept", "inspected", "Fixed source fixture inspected"
		}
		isolated := State{Proposals: []ProposalView{{Proposal: p, Status: "pending", Validity: "current"}}}
		r, err := PrepareReview(isolated, p.ID, decision, "supervisor", "Inspected fixture", scope, evidence)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := SaveReview(t.Context(), memories, r); err != nil {
			t.Fatal(err)
		}
	}
	state := reader.Refresh(Decode(memories.plane))
	if len(state.Records) != 1601 || len(state.Proposals) != 1152 || len(state.Warnings) != 0 {
		t.Fatalf("lost assertions or review history: records=%d proposals=%d warnings=%d", len(state.Records), len(state.Proposals), len(state.Warnings))
	}
	for _, p := range state.Proposals {
		if len(p.Reviews) != 1 || p.Status == "pending" || p.Status == "conflict" {
			t.Fatal("review decision or immutable lineage lost")
		}
	}
	before := memories.writes
	p := submit(t, reader, memories, record, "")
	r := review(t, reader, memories, p.ID, "accept")
	if _, err := SaveReview(t.Context(), memories, r); err != nil {
		t.Fatal(err)
	}
	state = reader.Refresh(Decode(memories.plane))
	if len(state.Records) != 1602 || memories.writes != before+2 || len(Context(state)) > 6400 {
		t.Fatal("new promotion changed history or expanded context")
	}
}

func TestKnowledgeGrowthStillRefusesNewAcceptanceAtCapacity(t *testing.T) {
	_, reader, record, memories := proposalFixture(t)
	for i := 0; i < MaxRecords; i++ {
		item := record
		item.ID = fmt.Sprintf("existing-%04d", i)
		if err := SaveRecord(t.Context(), memories, item); err != nil {
			t.Fatal(err)
		}
	}
	p := submit(t, reader, memories, record, "")
	state := reader.Refresh(Decode(memories.plane))
	before := memories.writes
	if _, err := PrepareReview(state, p.ID, "accept", "supervisor", "Reviewed", "inspected", "Fixed fixture"); err == nil {
		t.Fatal("acceptance exceeded the finite assertion capacity")
	}
	if memories.writes != before || len(state.Records) != MaxRecords || state.Proposals[0].Status != "pending" {
		t.Fatal("capacity refusal changed retained state")
	}
}

// Measures complete decode, fresh source validation and bounded recovery.
// Eight records share each 1 KiB source; setup and source creation are excluded.
func BenchmarkKnowledgeCapacity(b *testing.B) {
	for _, count := range []int{512, 1024, 2048} {
		b.Run(fmt.Sprint(count), func(b *testing.B) {
			if count > MaxRecords {
				b.Skip("outside this binary's assertion capacity")
			}
			root := b.TempDir()
			if err := os.WriteFile(filepath.Join(root, "DOX.md"), []byte("# Contract\n"), 0600); err != nil {
				b.Fatal(err)
			}
			reader, err := Open(root)
			if err != nil {
				b.Fatal(err)
			}
			defer func() { _ = reader.Close() }()
			memories := &proposalMemory{plane: map[string]string{}}
			for i := 0; i < count; i++ {
				path := fmt.Sprintf("source-%04d.md", i/8)
				if i%8 == 0 {
					if err := os.WriteFile(filepath.Join(root, path), []byte(strings.Repeat("x", 1024)), 0600); err != nil {
						b.Fatal(err)
					}
				}
				r, err := reader.Bind(Record{ID: fmt.Sprintf("record-%04d", i), Kind: "solution", Summary: "Reviewed fixture", Scope: "recorded", Sources: []Source{{Path: path}}})
				if err != nil {
					b.Fatal(err)
				}
				if err := SaveRecord(context.Background(), memories, r); err != nil {
					b.Fatal(err)
				}
			}
			b.ReportAllocs()
			for b.Loop() {
				state := reader.Refresh(Decode(memories.plane))
				if len(state.Records) != count || len(Context(state)) > 6400 || len(state.Warnings) != 0 {
					b.Fatal("incomplete records or expanded context")
				}
			}
		})
	}
}
