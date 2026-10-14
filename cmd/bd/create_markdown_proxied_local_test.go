//go:build cgo && unix

package main

import (
	"os/exec"
	"testing"
	"time"
)

func TestManagedLocalProxiedMarkdownDependencies(t *testing.T) {
	requireManagedLocalProxiedEnv(t)
	t.Setenv("DOLT_ROOT_PATH", t.TempDir())
	// Keep the disposable Dolt version probe offline.
	if out, err := exec.Command("dolt", "config", "--global", "--add", "versioncheck.disabled", "true").CombinedOutput(); err != nil {
		t.Fatalf("configure isolated Dolt: %v\n%s", err, out)
	}
	bd := buildEmbeddedBD(t)
	p := bdManagedLocalInit(t, bd, "md", time.Minute)
	checkMarkdownDependencyContract(t, p.dir, func(args ...string) (string, error) {
		out, err := bdProxiedRun(t, bd, p.dir, args...)
		return string(out), err
	})
}
