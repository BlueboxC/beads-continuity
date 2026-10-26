package doltutil

import (
	"fmt"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf8"
)

// PrintedValue spells value as a refusal names it beside a remedy command: as
// it is when it prints as itself (PrintsAsItself), and otherwise escaped
// (EscapedValue), which spells each character that does not as an escape, so
// the value cannot redraw, reorder or split the command. Unlike
// ui.SanitizeForTerminal, which drops such characters, it still names the
// value bd recorded.
func PrintedValue(value string) string {
	if PrintsAsItself(value) {
		return value
	}
	return EscapedValue(value)
}

// EscapedValue spells value as a refusal names a value its remedy leaves as a
// placeholder, for the user to quote in its place: Go-quoted, as %q spells it,
// with each character ShellQuotedFor refuses on some platform but the
// backslash, which %q already escapes, also spelled as an escape: \x22 for ",
// \x24 for $, \x26 for &, \u201c for “. Between the double quotes is then no
// character a shell ShellQuotedFor names expands, ends the quotes at or splits
// the argument at, so a user who pastes the name as it is printed runs
// nothing, and strconv.Unquote still reads it back as the value.
func EscapedValue(value string) string {
	shellActive := func(r rune) bool {
		return r != '\\' && strings.ContainsRune(unquotableOnPOSIX+unquotableOnWindows, r)
	}
	var escaped strings.Builder
	escaped.WriteByte('"')
	for value != "" {
		// The run up to the next such character is spelled as %q spells it.
		n := strings.IndexFunc(value, shellActive)
		if n < 0 {
			n = len(value)
		}
		quoted := strconv.Quote(value[:n])
		escaped.WriteString(quoted[1 : len(quoted)-1])
		value = value[n:]
		if value == "" {
			break
		}
		r, size := utf8.DecodeRuneInString(value)
		if r < utf8.RuneSelf {
			fmt.Fprintf(&escaped, `\x%02x`, r)
		} else {
			fmt.Fprintf(&escaped, `\u%04x`, r)
		}
		value = value[size:]
	}
	escaped.WriteByte('"')
	return escaped.String()
}

// PrintsAsItself reports whether a terminal shows value as the characters it
// holds: it is valid UTF-8, since a stray byte such as 0x9b starts a control
// sequence in a terminal that reads 8-bit controls, and each rune is graphic
// (unicode.IsGraphic): a letter, mark, number, punctuation, symbol or space,
// such as the U+3000 a Japanese IME types. That rules out a control
// character, such as a carriage return or the escape that starts a terminal
// sequence, a format character, such as U+202E, which reverses the text after
// it, and a line or paragraph separator, U+2028 or U+2029.
func PrintsAsItself(value string) bool {
	return utf8.ValidString(value) && !strings.ContainsFunc(value, func(r rune) bool { return !unicode.IsGraphic(r) })
}

// ShellQuotedFor quotes value as one argument of a command printed for a user
// to paste into a shell on goos, and reports whether it can: a remedy prints a
// value in a command only when the shells people run there all pass it to bd
// unchanged, and otherwise names it beside a placeholder.
//
// On every platform it refuses a value that does not print as itself
// (PrintsAsItself): a line break ends the command in each shell, a carriage
// return or terminal escape sequence can redraw the printed command so the
// one a user copies is not the one bd printed, and a character such as U+202E
// or U+2028 can reorder or split how the command shows.
//
// On POSIX a value goes in single quotes (doltutil.ShellQuote), which sh, bash,
// zsh, fish and PowerShell keep literal, unless it holds:
//   - \, which fish reads as an escape there, so a backslash before a quote
//     ends them;
//   - ', which ends them in each: doltutil.ShellQuote spells it as \' between
//     two quoted parts, which sh, bash, zsh and fish read as a quote, but
//     PowerShell, which takes a backslash literally, does not, and runs the
//     rest of the value as a command;
//   - ‘ ’ ‚ ‛ (U+2018 to U+201B), which end them in PowerShell.
//
// On Windows a value goes in plain double quotes, inside which cmd.exe,
// PowerShell and Git Bash keep spaces, backslashes and ( ) ' # @ literal, as
// Go's argument parsing does, unless it holds:
//   - ", which ends the quotes in each, or “ ” „, which end them in PowerShell;
//   - $ or `, which PowerShell and bash expand there;
//   - % or !, which cmd.exe expands there (! with delayed expansion on), and
//     bash expands ! as history;
//   - & | < > or ^, which cmd.exe acts on when PowerShell hands a .cmd shim,
//     such as npm's bd.cmd, an argument with no space in it unquoted, or ; ,
//     or =, which cmd.exe reads there as separating a batch file's
//     arguments, so a shim that reads them one by one (%1, %2) rather than
//     passing them all on (%*), as npm's does, hands bd a host such as
//     db.example.com;--recreate-missing as two (each refused whether or not
//     the value has a space, so a path such as R&D planning gets a
//     placeholder too);
//   - \\, which bash reads there as one backslash;
//
// or ends in a backslash, which Go's argument parsing reads as escaping the
// closing quote. cmd.exe needs it doubled, and PowerShell, which takes
// backslashes literally, would then pass both on.
func ShellQuotedFor(goos, value string) (string, bool) {
	if !PrintsAsItself(value) {
		return "", false
	}
	if goos != "windows" {
		if strings.ContainsAny(value, unquotableOnPOSIX) {
			return "", false
		}
		return ShellQuote(value), true
	}
	if strings.HasSuffix(value, `\`) || strings.Contains(value, `\\`) || strings.ContainsAny(value, unquotableOnWindows) {
		return "", false
	}
	return `"` + value + `"`, true
}

// unquotableOnPOSIX and unquotableOnWindows hold the characters a shell there
// does not keep literal inside the quotes ShellQuotedFor puts a value in, or,
// on Windows, in the argument PowerShell hands a .cmd shim unquoted.
// EscapedValue spells each of them as an escape.
const (
	unquotableOnPOSIX   = "\\'‘’‚‛"
	unquotableOnWindows = "\"“”„$`%!&|<>^;,="
)
