package gitlab

import (
	"net/url"
	"strings"
)

// Identity matching may span renamed projects, but never instances. Preserve
// the configured scheme, origin and installation prefix; no HTTP is performed.
func (t *Tracker) onConfiguredHost(ref string) bool {
	if t.client == nil {
		return false
	}
	base, err := url.Parse(t.client.BaseURL)
	u, parseErr := url.Parse(strings.TrimSpace(ref))
	if err != nil || parseErr != nil || u.User != nil || !strings.EqualFold(base.Scheme, u.Scheme) {
		return false
	}
	baseHost, basePath, baseOK := canonicalGitLabURL(t.client.BaseURL)
	host, path, ok := canonicalGitLabURL(ref)
	return baseOK && ok && host == baseHost && strings.HasPrefix(path, strings.TrimRight(basePath, "/")+"/")
}
