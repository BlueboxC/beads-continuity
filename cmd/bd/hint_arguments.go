package main

import (
	"fmt"
	"runtime"
	"strings"

	"github.com/steveyegge/beads/internal/ui"
)

// Plain minted IDs retain their existing spelling in hints.
func plainHintWord(value string) bool {
	if value == "" || value[0] == '-' {
		return false
	}
	for _, r := range value {
		if r >= 'a' && r <= 'z' || r >= 'A' && r <= 'Z' || r >= '0' && r <= '9' || strings.ContainsRune("._:/-", r) {
			continue
		}
		return false
	}
	return true
}

func hintCommandArgFor(goos, value, placeholder string) (string, string) {
	if plainHintWord(value) {
		return value, ""
	}
	// Quotes do not prevent a leading dash from becoming a CLI option.
	if !strings.HasPrefix(value, "-") {
		if quoted, ok := shellQuotedFor(goos, value); ok {
			return quoted, ""
		}
	}
	return placeholder, "\n  # " + placeholder + " = " + escapedValue(value)
}

func hintCommandArg(value, placeholder string) (string, string) {
	return hintCommandArgFor(runtime.GOOS, value, placeholder)
}

func printedIssueID(id string) string {
	if plainHintWord(id) {
		return id
	}
	return escapedValue(id)
}

func printedIssueIDs(ids []string) string {
	out := make([]string, len(ids))
	for i, id := range ids {
		out[i] = printedIssueID(id)
	}
	return strings.Join(out, " ")
}

func printStepInstructionsHint(id string) {
	arg, note := hintCommandArg(id, "<step-id>")
	fmt.Printf("\n%s Run bd show %s to see detailed instructions.%s\n", ui.RenderAccent("💡"), arg, note)
}

func duplicateMergeCommand(sources []string, target string) string {
	args := make([]string, len(sources))
	var notes string
	for i, id := range sources {
		placeholder := fmt.Sprintf("<duplicate-id-%d>", i+1)
		arg, note := hintCommandArg(id, placeholder)
		args[i] = arg
		notes += note
	}
	targetArg, note := hintCommandArg(target, "<target-id>")
	ids := strings.Join(args, " ")
	return fmt.Sprintf("bd close %s && bd dep add %s %s --type related%s", ids, ids, targetArg, notes+note)
}

func recipeRefusalWord(value string) string {
	if plainHintWord(value) {
		return "'" + value + "'"
	}
	return escapedValue(value)
}

func printedFeedbackID(id, title string) string {
	title = applyTitleConfig(title)
	if title == "" {
		return printedIssueID(id)
	}
	return printedIssueID(id) + " — " + printedValue(ui.SanitizeForTerminal(title))
}
