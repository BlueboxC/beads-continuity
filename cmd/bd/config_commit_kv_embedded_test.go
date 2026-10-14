//go:build cgo

package main

import (
	"os"
	"os/exec"
	"testing"
)

// Observe the real CLI epilogue in history; a readable KV value alone does not
// prove it was versioned. Batch/off must still persist without a version commit.
func TestEmbeddedKVAutoCommitPolicy(t *testing.T) {
	if os.Getenv("BEADS_TEST_EMBEDDED_DOLT") != "1" {
		t.Skip("set BEADS_TEST_EMBEDDED_DOLT=1")
	}
	binary := buildEmbeddedBD(t)
	for _, mode := range []string{"on", "off", "batch"} {
		t.Run(mode, func(t *testing.T) {
			work, beadsDir, _ := bdInit(t, binary, "--prefix", "kvcommit", "--skip-hooks", "--skip-agents")
			invoke := func(args ...string) {
				t.Helper()
				cmd := exec.Command(binary, append([]string{"--sandbox", "--dolt-auto-commit", mode}, args...)...)
				cmd.Dir, cmd.Env = work, bdEnv(work)
				if out, err := cmd.CombinedOutput(); err != nil {
					t.Fatalf("%v: %v\n%s", args, err, out)
				}
			}
			before := countMemoryDoltCommits(t, beadsDir)
			invoke("kv", "set", "retained", "value")
			invoke("kv", "get", "retained")
			afterSet := countMemoryDoltCommits(t, beadsDir)
			invoke("kv", "clear", "retained")
			afterClear := countMemoryDoltCommits(t, beadsDir)
			want := 0
			if mode == "on" {
				want = 1
			}
			if afterSet-before != want || afterClear-afterSet != want {
				t.Fatalf("%s history deltas set=%d clear=%d, want %d each", mode, afterSet-before, afterClear-afterSet, want)
			}
		})
	}
}
