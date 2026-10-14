package codeindex

import (
	"context"
	"fmt"
	"os/exec"
	"strings"
	"testing"
)

func TestParserOutputLimitPreservesFiles(t *testing.T) {
	cases := []struct {
		name, extension, executable, small string
		oversized, medium                  int
		content                            func(int) string
		parse                              func([]parserInput) (parserOutput, error)
		size                               func(File) int
	}{
		{name: "json", extension: "json", executable: "node", small: `{"ok":1}`, oversized: 32000, medium: 19000,
			content: func(count int) string {
				var out strings.Builder
				out.WriteByte('{')
				prefix := strings.Repeat("k", 96)
				for i := 0; i < count; i++ {
					if i > 0 {
						out.WriteByte(',')
					}
					fmt.Fprintf(&out, "%q:0", fmt.Sprintf("%s%05d", prefix, i))
				}
				out.WriteByte('}')
				return out.String()
			},
			parse: func(inputs []parserInput) (parserOutput, error) {
				return parseTrees(context.Background(), "", "json", inputs)
			},
			size: func(file File) int { return len(file.Symbols) },
		},
		{name: "python", extension: "py", executable: "python3", small: "def run():\n    ok()\n", oversized: 60000, medium: 32000,
			content: func(count int) string {
				var out strings.Builder
				out.WriteString("def run():\n")
				prefix := strings.Repeat("k", 96)
				for i := 0; i < count; i++ {
					fmt.Fprintf(&out, "    %s%05d()\n", prefix, i)
				}
				return out.String()
			},
			parse: func(inputs []parserInput) (parserOutput, error) { return parse(context.Background(), "", inputs) },
			size:  func(file File) int { return len(file.Calls) },
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if _, err := exec.LookPath(tc.executable); err != nil {
				t.Skip("operator interpreter unavailable")
			}
			t.Run("one-file-too-large", func(t *testing.T) {
				inputs := []parserInput{{Path: "dense." + tc.extension, Content: tc.content(tc.oversized)}, {Path: "small." + tc.extension, Content: tc.small}}
				got, err := tc.parse(inputs)
				if err != nil {
					t.Fatal(err)
				}
				if len(got.Files) != 2 || got.Files[0].Path != inputs[0].Path || got.Files[1].Path != inputs[1].Path {
					t.Fatal("file identities lost")
				}
				large, small := got.Files[0], got.Files[1]
				if large.ParseError != "ASTLimit" || len(large.Symbols)+len(large.Imports)+len(large.Calls) != 0 {
					t.Fatalf("large file retained partial structure: %s / %d", large.ParseError, tc.size(large))
				}
				if small.ParseError != "" || tc.size(small) != 1 {
					t.Fatalf("valid sibling lost: %s / %d", small.ParseError, tc.size(small))
				}
			})
			t.Run("only-batch-too-large", func(t *testing.T) {
				content := tc.content(tc.medium)
				inputs := []parserInput{{Path: "first." + tc.extension, Content: content}, {Path: "second." + tc.extension, Content: content}}
				got, err := tc.parse(inputs)
				if err != nil {
					t.Fatal(err)
				}
				if len(got.Files) != len(inputs) {
					t.Fatalf("incomplete batch: %d", len(got.Files))
				}
				for i, file := range got.Files {
					if file.Path != inputs[i].Path || file.ParseError != "" || tc.size(file) != tc.medium {
						t.Fatalf("valid structure discarded: %s / %s / %d", file.Path, file.ParseError, tc.size(file))
					}
				}
			})
		})
	}
}
