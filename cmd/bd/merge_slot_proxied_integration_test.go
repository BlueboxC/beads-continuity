//go:build cgo && unix

package main

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

// Exercise the real CLI, proxy and Dolt commit boundary: concurrent attempts
// must not both report acquisition; a refused closure must roll its edge back.
func TestProxiedMergeSlotAndSupersede(t *testing.T) {
	requireManagedLocalProxiedEnv(t)
	bd := buildEmbeddedBD(t)
	p := bdManagedLocalInit(t, bd, "slotprobe", 5*time.Minute)
	call := func(ok bool, args ...string) map[string]any {
		t.Helper()
		out, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, append(args, "--json")...)
		if (err == nil) != ok {
			t.Fatalf("%v: %v\n%s%s", args, err, out, stderr)
		}
		var value map[string]any
		if err := json.Unmarshal([]byte(out), &value); err != nil {
			t.Fatalf("%v JSON: %v: %s%s", args, err, out, stderr)
		}
		return value
	}
	missing := call(true, "merge-slot", "check")
	if missing["available"] != false || missing["error"] != "not found" {
		t.Fatalf("missing: %v", missing)
	}
	made := call(true, "merge-slot", "create")
	if made["id"] != "slotprobe-merge-slot" {
		t.Fatalf("slot identity: %v", made)
	}
	db := openProxiedDB(t, p)
	history := func() int {
		t.Helper()
		var n int
		if err := db.QueryRow("SELECT COUNT(*) FROM dolt_log").Scan(&n); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := history()
	call(true, "merge-slot", "create")
	call(true, "merge-slot", "check")
	if history() != before {
		t.Fatal("idempotent create/check minted a history entry")
	}
	type attempt struct {
		holder      string
		out, stderr string
		err         error
	}
	results := make(chan attempt, 8)
	start := make(chan struct{})
	for i := 0; i < 8; i++ {
		go func(i int) {
			<-start
			holder := fmt.Sprintf("agent-%d", i)
			out, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "merge-slot", "acquire", "--holder", holder, "--json")
			results <- attempt{holder, out, stderr, err}
		}(i)
	}
	close(start)
	winner := ""
	wins := 0
	for i := 0; i < 8; i++ {
		r := <-results
		var value map[string]any
		if err := json.Unmarshal([]byte(r.out), &value); err != nil {
			t.Fatalf("race JSON: %v: %s%s", err, r.out, r.stderr)
		}
		if value["acquired"] == true {
			if r.err != nil {
				t.Fatal(r.err)
			}
			wins++
			winner = r.holder
		} else if r.err == nil || value["acquired"] != false {
			t.Fatalf("race result: %+v %v", r, value)
		}
	}
	if wins != 1 {
		t.Fatalf("%d simultaneous winners", wins)
	}
	stdout, stderr, err := bdProxiedRunBuffers(t, bd, p.dir, "merge-slot", "release", "--holder", "foreign")
	if err == nil || !strings.Contains(stdout+stderr, "slot held") {
		t.Fatalf("foreign release accepted: %s%s", stdout, stderr)
	}

	for i := 0; i < 2; i++ {
		wait := call(false, "merge-slot", "acquire", "--holder", "waiter", "--wait")
		if wait["waiting"] != true || wait["position"] != float64(1) {
			t.Fatalf("wait: %v", wait)
		}
	}
	state := call(true, "merge-slot", "check")
	if state["holder"] != winner || len(state["waiters"].([]any)) != 1 {
		t.Fatalf("held state: %v", state)
	}
	call(true, "merge-slot", "release", "--holder", winner)
	call(true, "merge-slot", "acquire", "--holder", "waiter")
	call(true, "merge-slot", "release", "--holder", "waiter")
	before = history()
	call(true, "merge-slot", "release")
	if history() != before {
		t.Fatal("idempotent release minted a history entry")
	}
	old := bdProxiedCreate(t, bd, p.dir, "old implementation")
	replacement := bdProxiedCreate(t, bd, p.dir, "replacement implementation")
	result := call(true, "supersede", old.ID, "--with", replacement.ID)
	if result["superseded"] != old.ID || result["replacement"] != replacement.ID {
		t.Fatalf("supersede: %v", result)
	}
	assertProxiedDepExistsWithType(t, db, old.ID, replacement.ID, "supersedes")
	var status string
	if err := db.QueryRow("SELECT status FROM issues WHERE id=?", old.ID).Scan(&status); err != nil || status != "closed" {
		t.Fatalf("closure: %s %v", status, err)
	}
	protected := bdProxiedCreate(t, bd, p.dir, "protected task")
	gate := call(true, "gate", "create", "--blocks", protected.ID, "--type", "human")
	if _, err := bdProxiedRun(t, bd, p.dir, "config", "set", "gates.human.resolvers", `["owner"]`); err != nil {
		t.Fatal(err)
	}
	before = history()
	stdout, stderr, err = bdProxiedRunBuffers(t, bd, p.dir, "--actor", "agent", "supersede", gate["id"].(string), "--with", replacement.ID)
	if err == nil {
		t.Fatalf("protected supersede accepted: %s%s", stdout, stderr)
	}
	var count int
	if err := db.QueryRow("SELECT COUNT(*) FROM dependencies WHERE issue_id=? AND COALESCE(depends_on_issue_id,depends_on_wisp_id,depends_on_external)=? AND type='supersedes'", gate["id"], replacement.ID).Scan(&count); err != nil || count != 0 {
		t.Fatalf("failed close leaked edge: %d %v", count, err)
	}
	if history() != before {
		t.Fatal("failed supersede committed history")
	}
	for _, args := range [][]string{{"merge-slot", "acquire", "--holder", "readonly"}, {"merge-slot", "create"}, {"supersede", protected.ID, "--with", replacement.ID}} {
		stdout, stderr, err := bdProxiedRunBuffersWithEnv(t, bd, p.dir, []string{"BD_READONLY=1"}, args...)
		if err == nil {
			t.Fatalf("readonly mutation accepted: %v %s%s", args, stdout, stderr)
		}
	}
}
