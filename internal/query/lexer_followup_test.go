package query

import (
	"fmt"
	"strings"
	"testing"
)

func TestCurrentLexerReportedEdges(t *testing.T) {
	cases := []struct {
		name, input   string
		wantErr       bool
		wantSubstring string
		rejectNumber  bool
	}{
		{"NFC_control", "label=café", false, "", false},
		{"literal_NUL_truncation", "label=ab\x00 OR status=closed", true, "unexpected NUL", false},
		{"literal_NUL_in_quote", "title=\"ab\x00cd\"", true, "unexpected NUL", false},
		{"combining_mark_NFD", "label=cafe\u0301", false, "", false},
		{"Indic_combining_marks", "label=हिन्दी", false, "", false},
		{"non_ASCII_digit", "priority=٣", true, "", true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := NewLexer(c.input)
			var got []string
			var lastErr error
			badNum := false
			for i := 0; i < 100; i++ {
				tok, err := l.NextToken()
				if err != nil {
					lastErr = err
					break
				}
				got = append(got, fmt.Sprintf("%v(%q)@%d", tok.Type, tok.Value, tok.Pos))
				if tok.Type == TokenNumber && tok.Value == "٣" {
					badNum = true
				}
				if tok.Type == TokenEOF {
					break
				}
			}
			t.Logf("tokens=%v error=%v", got, lastErr)
			if c.rejectNumber && badNum {
				t.Error("non-ASCII digit classified as NUMBER")
			}
			if c.wantErr && lastErr == nil {
				t.Error("accepted input that must be rejected")
			}
			if !c.wantErr && lastErr != nil {
				t.Errorf("valid text rejected: %v", lastErr)
			}
			if c.wantSubstring != "" && lastErr != nil && !strings.Contains(lastErr.Error(), c.wantSubstring) {
				t.Errorf("wrong diagnostic: %v", lastErr)
			}
		})
	}
}
