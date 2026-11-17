package doltutil

import (
	"regexp"
	"strings"
)

const ambiguousRemotePlaceholder = "[ambiguous remote URL redacted]"

// RedactRemoteURL renders a remote for diagnostics. An unambiguous SSH
// account is retained and userinfo is otherwise removed. A colon in an
// apparent authority followed by a slash or space and a later @ can name a
// port and path or a raw password. Withhold that entire URL spelling.
// This function operates on diagnostic text only.
func RedactRemoteURL(raw string) string {
	sep := strings.Index(raw, "://")
	if sep < 0 {
		return raw
	}
	redacted, ambiguous := redactRemoteAuthority(raw, sep+3)
	if ambiguous {
		return ambiguousRemotePlaceholder
	}
	return redacted
}

func redactRemoteAuthority(raw string, schemeEnd int) (redacted string, ambiguous bool) {
	rest := raw[schemeEnd:]
	authority := beforeURLDelimiter(rest, "/?# \t\r\n")
	at := strings.LastIndexByte(authority, '@')
	if at < 0 {
		if end := ambiguousAuthorityEnd(rest, authority); end >= 0 {
			return ambiguousRemotePlaceholder + rest[end:], true
		}
		return raw, false
	}

	scheme := raw[:schemeEnd-3]
	if strings.EqualFold(scheme, "ssh") || strings.EqualFold(scheme, "git+ssh") {
		account, _, hasPassword := strings.Cut(rest[:at], ":")
		if !hasPassword {
			return raw, false
		}
		return raw[:schemeEnd] + account + rest[at:], false
	}
	return raw[:schemeEnd] + rest[at+1:], false
}

// ambiguousAuthorityEnd gives the end of the diagnostic token to withhold.
// Whitespace before @ can be part of the password. Queries and fragments
// after a definite authority stay separate from the ambiguity check.
func ambiguousAuthorityEnd(rest, authority string) int {
	prefix := beforeURLDelimiter(authority, `'"`)
	if !strings.Contains(prefix, ":") || len(authority) == len(rest) ||
		!strings.ContainsRune("/ \t", rune(rest[len(authority)])) {
		return -1
	}
	candidate := beforeURLDelimiter(rest, "?#\r\n")
	at := strings.IndexByte(candidate, '@')
	if at < 0 {
		return -1
	}
	tail := beforeURLDelimiter(rest[at+1:], " \t\r\n'\"<>")
	return at + 1 + len(tail)
}

func beforeURLDelimiter(s, delimiters string) string {
	if end := strings.IndexAny(s, delimiters); end >= 0 {
		return s[:end]
	}
	return s
}

var remoteURLStart = regexp.MustCompile(`[a-zA-Z][a-zA-Z0-9+.-]*://`)

// RedactRemoteText applies the same URL policy inside a diagnostic. Ambiguous
// spans are replaced with a placeholder and separate URLs are handled independently.
func RedactRemoteText(s string) string {
	starts := remoteURLStart.FindAllStringIndex(s, -1)
	if len(starts) == 0 {
		return s
	}
	var out strings.Builder
	last := 0
	for i, start := range starts {
		end := len(s)
		if i+1 < len(starts) {
			end = starts[i+1][0]
		}
		raw := s[start[0]:end]
		redacted, _ := redactRemoteAuthority(raw, start[1]-start[0])
		if redacted == raw {
			continue
		}
		out.WriteString(s[last:start[0]])
		out.WriteString(redacted)
		last = end
	}
	if last == 0 {
		return s
	}
	out.WriteString(s[last:])
	return out.String()
}

// RedactRemoteError preserves the underlying typed error for errors.Is and
// errors.As. Errors with already safe messages keep their original identity.
func RedactRemoteError(err error) error {
	if err == nil {
		return nil
	}
	message := err.Error()
	redacted := RedactRemoteText(message)
	if redacted == message {
		return err
	}
	return redactedError{err: err, message: redacted}
}

type redactedError struct {
	err     error
	message string
}

func (e redactedError) Error() string { return e.message }
func (e redactedError) Unwrap() error { return e.err }
