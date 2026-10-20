package main

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"sync/atomic"
	"testing"

	"github.com/anthropics/anthropic-sdk-go"
	"github.com/anthropics/anthropic-sdk-go/option"
	"github.com/steveyegge/beads/internal/types"
)

func TestTokenize(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected map[string]int
	}{
		{
			name:  "simple words",
			input: "fix authentication bug",
			expected: map[string]int{
				"fix":            1,
				"authentication": 1,
				"bug":            1,
			},
		},
		{
			name:  "duplicate words",
			input: "fix the bug fix",
			expected: map[string]int{
				"fix": 2,
				"the": 1,
				"bug": 1,
			},
		},
		{
			name:  "mixed case and punctuation",
			input: "Fix: Authentication BUG!",
			expected: map[string]int{
				"fix":            1,
				"authentication": 1,
				"bug":            1,
			},
		},
		{
			name:  "hyphenated words preserved",
			input: "auto-import feature",
			expected: map[string]int{
				"auto-import": 1,
				"feature":     1,
			},
		},
		{
			name:     "single chars filtered",
			input:    "a b c hello",
			expected: map[string]int{"hello": 1},
		},
		{
			name:     "empty string",
			input:    "",
			expected: map[string]int{},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := tokenize(tt.input)
			if len(result) != len(tt.expected) {
				t.Errorf("tokenize(%q) got %d tokens, want %d: %v", tt.input, len(result), len(tt.expected), result)
				return
			}
			for k, v := range tt.expected {
				if result[k] != v {
					t.Errorf("tokenize(%q)[%q] = %d, want %d", tt.input, k, result[k], v)
				}
			}
		})
	}
}

func TestJaccardSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     map[string]int
		expected float64
		epsilon  float64
	}{
		{
			name:     "identical sets",
			a:        map[string]int{"fix": 1, "bug": 1},
			b:        map[string]int{"fix": 1, "bug": 1},
			expected: 1.0,
			epsilon:  0.001,
		},
		{
			name:     "disjoint sets",
			a:        map[string]int{"fix": 1, "bug": 1},
			b:        map[string]int{"add": 1, "feature": 1},
			expected: 0.0,
			epsilon:  0.001,
		},
		{
			name:     "partial overlap",
			a:        map[string]int{"fix": 1, "bug": 1, "auth": 1},
			b:        map[string]int{"fix": 1, "auth": 1, "login": 1},
			expected: 0.5, // intersection=2, union=4
			epsilon:  0.001,
		},
		{
			name:     "both empty",
			a:        map[string]int{},
			b:        map[string]int{},
			expected: 0.0,
			epsilon:  0.001,
		},
		{
			name:     "one empty",
			a:        map[string]int{"fix": 1},
			b:        map[string]int{},
			expected: 0.0,
			epsilon:  0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := jaccardSimilarity(tt.a, tt.b)
			diff := result - tt.expected
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.epsilon {
				t.Errorf("jaccardSimilarity() = %f, want %f (diff %f)", result, tt.expected, diff)
			}
		})
	}
}

func TestCosineSimilarity(t *testing.T) {
	tests := []struct {
		name     string
		a, b     map[string]int
		expected float64
		epsilon  float64
	}{
		{
			name:     "identical",
			a:        map[string]int{"fix": 1, "bug": 1},
			b:        map[string]int{"fix": 1, "bug": 1},
			expected: 1.0,
			epsilon:  0.001,
		},
		{
			name:     "orthogonal",
			a:        map[string]int{"fix": 1},
			b:        map[string]int{"add": 1},
			expected: 0.0,
			epsilon:  0.001,
		},
		{
			name:     "empty sets",
			a:        map[string]int{},
			b:        map[string]int{},
			expected: 0.0,
			epsilon:  0.001,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := cosineSimilarity(tt.a, tt.b)
			diff := result - tt.expected
			if diff < 0 {
				diff = -diff
			}
			if diff > tt.epsilon {
				t.Errorf("cosineSimilarity() = %f, want %f", result, tt.expected)
			}
		})
	}
}

func TestFindMechanicalDuplicates(t *testing.T) {
	issues := []*types.Issue{
		{
			ID:          "bd-001",
			Title:       "Fix authentication bug in login flow",
			Description: "The login page fails when using SSO authentication",
		},
		{
			ID:          "bd-002",
			Title:       "Authentication login bug fix",
			Description: "SSO authentication fails on the login page",
		},
		{
			ID:          "bd-003",
			Title:       "Add dark mode support",
			Description: "Implement dark mode theme for the application",
		},
	}

	pairs := findMechanicalDuplicates(issues, 0.3)

	// Should find bd-001 and bd-002 as similar
	found := false
	for _, p := range pairs {
		if (p.IssueA.ID == "bd-001" && p.IssueB.ID == "bd-002") ||
			(p.IssueA.ID == "bd-002" && p.IssueB.ID == "bd-001") {
			found = true
			if p.Similarity < 0.3 {
				t.Errorf("expected similarity >= 0.3 for auth bug pair, got %f", p.Similarity)
			}
		}
	}
	if !found {
		t.Error("expected to find auth bug pair as similar")
	}

	// bd-003 should NOT match with bd-001 or bd-002
	for _, p := range pairs {
		if p.IssueA.ID == "bd-003" || p.IssueB.ID == "bd-003" {
			t.Errorf("dark mode issue should not match auth issues, got similarity %f", p.Similarity)
		}
	}
}

func TestFindMechanicalDuplicatesHighThreshold(t *testing.T) {
	issues := []*types.Issue{
		{ID: "bd-001", Title: "Fix bug A", Description: "Something completely different"},
		{ID: "bd-002", Title: "Add feature B", Description: "Totally unrelated content"},
	}

	pairs := findMechanicalDuplicates(issues, 0.9)
	if len(pairs) != 0 {
		t.Errorf("expected no pairs at threshold 0.9 for different issues, got %d", len(pairs))
	}
}

func TestFindMechanicalDuplicatesMinIssues(t *testing.T) {
	// Single issue - no pairs possible
	issues := []*types.Issue{
		{ID: "bd-001", Title: "Only issue"},
	}
	pairs := findMechanicalDuplicates(issues, 0.1)
	if len(pairs) != 0 {
		t.Errorf("expected no pairs for single issue, got %d", len(pairs))
	}
}

func TestIssueText(t *testing.T) {
	issue := &types.Issue{
		Title:       "Fix bug",
		Description: "detailed description",
	}
	text := issueText(issue)
	if text != "Fix bug detailed description" {
		t.Errorf("issueText() = %q, want %q", text, "Fix bug detailed description")
	}

	// No description
	issue2 := &types.Issue{Title: "Just title"}
	text2 := issueText(issue2)
	if text2 != "Just title" {
		t.Errorf("issueText() = %q, want %q", text2, "Just title")
	}
}

func TestSummarizeDuplicates(t *testing.T) {
	tests := []struct {
		name          string
		total, limit  int
		wantShown     int
		wantTruncated bool
	}{
		{
			name:          "under the limit is not truncated",
			total:         10,
			limit:         50,
			wantShown:     10,
			wantTruncated: false,
		},
		{
			name:          "exactly at the limit is not truncated",
			total:         50,
			limit:         50,
			wantShown:     50,
			wantTruncated: false,
		},
		{
			name:          "over the limit is truncated (GH#7686)",
			total:         110,
			limit:         50,
			wantShown:     50,
			wantTruncated: true,
		},
		{
			name:          "limit 0 means unlimited, never truncated",
			total:         229,
			limit:         0,
			wantShown:     229,
			wantTruncated: false,
		},
		{
			name:          "negative limit also means unlimited",
			total:         5,
			limit:         -1,
			wantShown:     5,
			wantTruncated: false,
		},
		{
			name:          "zero total is never truncated",
			total:         0,
			limit:         50,
			wantShown:     0,
			wantTruncated: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := summarizeDuplicates(tt.total, tt.limit)
			if got.Total != tt.total {
				t.Errorf("summarizeDuplicates(%d, %d).Total = %d, want %d", tt.total, tt.limit, got.Total, tt.total)
			}
			if got.Shown != tt.wantShown {
				t.Errorf("summarizeDuplicates(%d, %d).Shown = %d, want %d", tt.total, tt.limit, got.Shown, tt.wantShown)
			}
			if got.Truncated != tt.wantTruncated {
				t.Errorf("summarizeDuplicates(%d, %d).Truncated = %v, want %v", tt.total, tt.limit, got.Truncated, tt.wantTruncated)
			}
		})
	}
}

// Regression from Harry Miller's upstream PR #7522.
func TestAnalyzeWithAI_SkipsLeadingThinkingBlock(t *testing.T) {
	const reply = `[{"pair_index":0,"is_duplicate":true,"confidence":0.9,"reason":"same bug"}]`
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"content":[{"type":"thinking","thinking":"hmm","signature":"sig"},{"type":"text","text":%q}]}`, reply)
	}))
	defer srv.Close()
	client := anthropic.NewClient(option.WithAPIKey("k"), option.WithBaseURL(srv.URL), option.WithMaxRetries(0))
	candidates := []duplicatePair{{IssueA: &types.Issue{ID: "bd-1", Title: "a"}, IssueB: &types.Issue{ID: "bd-2", Title: "b"}, Method: "mechanical"}}
	got := analyzeWithAI(context.Background(), client, "m", candidates)
	if len(got) != 1 || got[0].Method != "ai" || got[0].Reason != "same bug" {
		t.Fatalf("got %+v, want one ai pair with reason %q", got, "same bug")
	}
}

func TestAnalyzeWithAI_ResponseBlocks(t *testing.T) {
	const text = `{"type":"text","text":"[{\"pair_index\":0,\"is_duplicate\":true,\"confidence\":0.9,\"reason\":\"same\"}]"}`
	const thinking = `{"type":"thinking","thinking":"private-reasoning-sentinel","signature":"sig"}`
	for _, tc := range []struct {
		name, content, stop string
		fallback            bool
	}{
		{"text first", "[" + text + "]", "end_turn", false},
		{"redacted thinking", "[{\"type\":\"redacted_thinking\",\"data\":\"private\"}," + text + "]", "end_turn", false},
		{"first of multiple texts", "[" + text + ",{\"type\":\"text\",\"text\":\"do not append\"}]", "end_turn", false},
		{"interleaved thinking", "[" + thinking + "," + text + "," + thinking + "]", "end_turn", false},
		{"no blocks", "[]", "end_turn", true},
		{"no text", "[" + thinking + "]", "end_turn", true},
		{"empty text", "[{\"type\":\"text\",\"text\":\"\"}]", "end_turn", true},
		{"empty first text", "[{\"type\":\"text\",\"text\":\"\"}," + text + "]", "end_turn", true},
		{"truncated valid JSON", "[" + text + "]", "max_tokens", true},
		{"malformed JSON", "[{\"type\":\"text\",\"text\":\"[\"}]", "end_turn", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var requests atomic.Int32
			srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				if r.Method != "POST" || r.URL.Path != "/v1/messages" {
					t.Errorf("unexpected route: %s %s", r.Method, r.URL.Path)
				}
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"id":"msg_1","type":"message","role":"assistant","model":"m","stop_reason":%q,"usage":{"input_tokens":1,"output_tokens":1},"content":%s}`, tc.stop, tc.content)
			}))
			defer srv.Close()
			client := anthropic.NewClient(option.WithAPIKey("stub-only"), option.WithBaseURL(srv.URL), option.WithHTTPClient(srv.Client()), option.WithMaxRetries(0))
			candidates := []duplicatePair{{IssueA: &types.Issue{ID: "bd-1", Title: "a"}, IssueB: &types.Issue{ID: "bd-2", Title: "b"}, Similarity: 0.8, Method: "mechanical", Reason: "original"}}
			got := analyzeWithAI(context.Background(), client, "m", candidates)
			if requests.Load() != 1 {
				t.Fatalf("requests=%d, want 1", requests.Load())
			}
			if tc.fallback {
				if !reflect.DeepEqual(got, candidates) {
					t.Fatalf("fallback changed: %+v", got)
				}
				return
			}
			if len(got) != 1 || got[0].Method != "ai" || got[0].Reason != "same" || got[0].Similarity != 0.9 {
				t.Fatalf("bad AI result: %+v", got)
			}
		})
	}
}
