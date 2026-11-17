package doltutil

import (
	"errors"
	"fmt"
	"net/url"
	"testing"
)

func TestRemoteRedactionMasksMalformedUserinfo(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
	}{
		{"http_slash", "https://fixture-user:token/fragment@example.invalid/team/repo"},
		{"http_space", "https://fixture-user:token fragment@example.invalid/team/repo"},
		{"ssh_slash", "ssh://fixture-user:token/fragment@example.invalid/team/repo"},
		{"ssh_space", "git+ssh://fixture-user:token fragment@example.invalid/team/repo"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const withheld = "[ambiguous remote URL redacted]"
			if got := RedactRemoteURL(tt.raw); got != withheld {
				t.Error("direct URL redaction did not withhold the ambiguous URL")
			}
			diagnostic := fmt.Sprintf("remote '%s' refused; see https://guide.example.invalid/topics", tt.raw)
			want := fmt.Sprintf("remote '%s' refused; see https://guide.example.invalid/topics", withheld)
			if got := RedactRemoteText(diagnostic); got != want {
				t.Error("diagnostic redaction did not preserve the surrounding message")
			}
			cause := errors.New("fixture rejection")
			typed := &url.Error{Op: "read", URL: "fixture", Err: cause}
			err := RedactRemoteError(fmt.Errorf("%s: %w", diagnostic, typed))
			var unwrapped *url.Error
			if !errors.Is(err, cause) || !errors.As(err, &unwrapped) || unwrapped != typed ||
				err.Error() != want+`: read "fixture": fixture rejection` {
				t.Error("redacted error did not keep its typed cause and safe message")
			}
		})
	}
}

func TestRemoteRedactionProtectsAmbiguousNumericPrefix(t *testing.T) {
	for _, tt := range []struct {
		name string
		raw  string
	}{
		{"password_with_slash", "https://fixture-user:42/token@example.invalid/team"},
		{"empty_port_with_at_in_path", "https://fixture-user:/token@example.invalid/team"},
		{"password_with_space", "https://fixture-user:42 token@example.invalid/team"},
		{"valid_port_with_at_in_path", "https://example.invalid:443/team/member@example.invalid"},
	} {
		t.Run(tt.name, func(t *testing.T) {
			const withheld = "[ambiguous remote URL redacted]"
			if got := RedactRemoteURL(tt.raw); got != withheld {
				t.Error("direct URL redaction exposed an ambiguous spelling")
			}
			text := "transfer refused for '" + tt.raw + "'; see https://guide.example.invalid/topics"
			want := "transfer refused for '" + withheld + "'; see https://guide.example.invalid/topics"
			if got := RedactRemoteText(text); got != want {
				t.Error("diagnostic did not safely withhold the complete URL and preserve the adjacent message")
			}
			cause := &url.Error{Op: "read", URL: "fixture", Err: errors.New("fixture rejection")}
			err := RedactRemoteError(fmt.Errorf("%s: %w", text, cause))
			var typed *url.Error
			if !errors.Is(err, cause) || !errors.As(err, &typed) || typed != cause ||
				err.Error() != want+`: read "fixture": fixture rejection` {
				t.Error("error redaction exposed an ambiguous URL or lost the typed cause")
			}
		})
	}
}

func TestRemoteRedactionKeepsCredentialFreePathsAndValidUserinfo(t *testing.T) {
	for _, tt := range []struct{ raw, want string }{
		{
			"https://fixture-user:token%2Ffragment@example.invalid/team/repo",
			"https://example.invalid/team/repo",
		},
		{
			"git+ssh://fixture-user:token%20fragment@example.invalid/team/repo",
			"git+ssh://fixture-user@example.invalid/team/repo",
		},
		{
			"https://example.invalid/team/member@example.invalid",
			"https://example.invalid/team/member@example.invalid",
		},
		{
			"ssh://fixture-user@example.invalid/team/member@example.invalid",
			"ssh://fixture-user@example.invalid/team/member@example.invalid",
		},
		{
			"https://[2001:db8::1]:8443/team/repo",
			"https://[2001:db8::1]:8443/team/repo",
		},
		{
			"https://example.invalid?owner=member@example.invalid#editor@example.invalid",
			"https://example.invalid?owner=member@example.invalid#editor@example.invalid",
		},
		{
			"SSH://fixture-user@[2001:db8::1]:2222/team?owner=member@example.invalid",
			"SSH://fixture-user@[2001:db8::1]:2222/team?owner=member@example.invalid",
		},
		{
			"git@example.invalid:team/member@example.invalid",
			"git@example.invalid:team/member@example.invalid",
		},
	} {
		t.Run(tt.raw, func(t *testing.T) {
			if got := RedactRemoteURL(tt.raw); got != tt.want {
				t.Error("direct URL redaction changed the endpoint or retained valid userinfo")
			}
			text := fmt.Sprintf("inspect '%s'; fallback to https://second.example.invalid/team", tt.raw)
			want := fmt.Sprintf("inspect '%s'; fallback to https://second.example.invalid/team", tt.want)
			if got := RedactRemoteText(text); got != want {
				t.Error("text redaction changed the path or crossed into a separate URL")
			}
		})
	}
}
