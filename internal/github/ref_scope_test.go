package github

import "testing"

// Contributor scenarios from NahumLitvin's PR #7794, plus endpoint controls.
func TestExtractIdentifierScopedToRepository(t *testing.T) {
	tr := &Tracker{client: NewClient("token", "acme", "repo-b")}
	for _, tt := range []struct{ ref, want string }{
		{"https://github.com/acme/repo-b/issues/42", "42"},
		{"https://github.com/ACME/Repo-B/issues/42", "42"},
		{"github:42", "42"},
		{"https://github.com/acme/repo-a/issues/42", ""},
		{"https://github.com/other/repo-b/issues/42", ""},
		{"https://github.com.evil.test/acme/repo-b/issues/42", ""},
		{"https://gitlab.com/acme/repo-b/issues/42", ""},
		{"https://github.com/acme/repo-b/issues/42/extra", ""},
		{"https://api.github.com/repos/acme/repo-b/issues/42", "42"},
	} {
		if got := tr.ExtractIdentifier(tt.ref); got != tt.want {
			t.Errorf("ExtractIdentifier(%q) = %q, want %q", tt.ref, got, tt.want)
		}
	}
}

func TestExtractIdentifierEnterpriseEndpoint(t *testing.T) {
	tr := &Tracker{client: NewClient("token", "acme", "repo").WithBaseURL("https://forge.example.test/git/api/v3")}
	for _, ref := range []string{"https://forge.example.test/git/acme/repo/issues/7", "https://forge.example.test/git/api/v3/repos/acme/repo/issues/7"} {
		if got := tr.ExtractIdentifier(ref); got != "7" {
			t.Errorf("%s: %q", ref, got)
		}
	}
	for _, ref := range []string{"https://other.test/git/acme/repo/issues/7", "https://forge.example.test/other/acme/repo/issues/7"} {
		if got := tr.ExtractIdentifier(ref); got != "" {
			t.Errorf("foreign %s: %q", ref, got)
		}
	}
}
