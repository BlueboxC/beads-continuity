//go:build darwin

package doltserver

import (
	"errors"
	"os"
	"strconv"
	"testing"
)

func TestForkStopRetainsOwnChildWhenInspectionIsUnavailable(t *testing.T) {
	dir, _ := managedStartFixture(t, "owned")
	state, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	// This real child implements readiness only; SQL persistence is a separate boundary.
	originalFlush := flushWorkingSetBeforeStop
	t.Cleanup(func() { flushWorkingSetBeforeStop = originalFlush })
	flushes := 0
	flushWorkingSetBeforeStop = func(host string, port int) error {
		if port != state.Port {
			t.Fatalf("flushed another endpoint: %s:%d", host, port)
		}
		flushes++
		return nil
	}
	original := readDoltProcesses
	t.Cleanup(func() { readDoltProcesses = original })
	readDoltProcesses = func() ([]int, error) { return nil, errors.New("inspection unavailable") }
	other := t.TempDir()
	if err := os.WriteFile(pidPath(other), []byte(strconv.Itoa(state.PID)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writePortFile(other, state.Port); err != nil {
		t.Fatal(err)
	}
	if err := Stop(other); err == nil {
		t.Fatal("an owned child in another workspace was accepted")
	}
	if !isProcessAlive(state.PID) {
		t.Fatal("foreign-workspace refusal stopped the child")
	}
	if err := writePortFile(dir, state.Port%65535+1); err != nil {
		t.Fatal(err)
	}
	if err := Stop(dir); err == nil {
		t.Fatal("mismatched owned-child port was accepted")
	}
	if !isProcessAlive(state.PID) {
		t.Fatal("port mismatch stopped the child")
	}
	if err := writePortFile(dir, state.Port); err != nil {
		t.Fatal(err)
	}
	if err := Stop(dir); err != nil {
		t.Fatalf("own child could not be stopped: %v", err)
	}
	if flushes != 1 {
		t.Fatalf("successful owned stop flushed %d times, want one", flushes)
	}
	if isProcessAlive(state.PID) {
		t.Fatal("own child still alive after Stop")
	}
	if _, err := os.Stat(pidPath(dir)); !os.IsNotExist(err) {
		t.Fatalf("own state retained after successful stop: %v", err)
	}
}
