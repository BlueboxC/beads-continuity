package git

import (
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
)

// GetMainRepoRootFrom resolves dir without reading or changing the process cache.
func GetMainRepoRootFrom(dir string) (string, error) {
	if dir == "" {
		return "", fmt.Errorf("main repository root requires a working directory")
	}
	work, err := filepath.Abs(dir)
	if err != nil {
		return "", err
	}
	work, err = filepath.EvalSymlinks(work)
	if err != nil {
		return "", err
	}
	cmd := exec.Command("git", "rev-parse", "--path-format=absolute", "--git-dir", "--git-common-dir", "--show-toplevel")
	cmd.Dir = work
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("not a git work tree: %w", err)
	}
	parts := strings.Split(strings.TrimSuffix(string(out), "\n"), "\n")
	if len(parts) != 3 {
		return "", fmt.Errorf("unexpected repository identity output")
	}
	normalize := func(p string) string {
		p = NormalizePath(strings.TrimSuffix(p, "\r"))
		if resolved, e := filepath.EvalSymlinks(p); e == nil {
			p = resolved
		}
		if canonical := canonicalizeCase(p); canonical != "" {
			p = canonical
		}
		return p
	}
	gitDir, common, root := normalize(parts[0]), normalize(parts[1]), normalize(parts[2])
	if gitDir != common {
		return filepath.Dir(common), nil
	}
	return root, nil
}
