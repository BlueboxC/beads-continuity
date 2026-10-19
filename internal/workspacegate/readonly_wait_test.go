package workspacegate

import (
	"context"
	"errors"
	"os"
	"slices"
	"testing"
	"time"
)

// Strict readonly must preserve the existing wait budget without creating files.

// gateDirEntries lists dir's entries by name (os.ReadDir sorts them), so a
// test can prove an acquisition left the gate's directory as it found it.
func gateDirEntries(t *testing.T, dir string) []string {
	t.Helper()
	entries, err := os.ReadDir(dir)
	if err != nil {
		t.Fatal(err)
	}
	names := make([]string, 0, len(entries))
	for _, e := range entries {
		names = append(names, e.Name())
	}
	return names
}

// A waiting readonly hold on a free existing gate creates no companion files.
func TestReadOnlyWaitingSharedAcquireOfExistingGateCreatesNothing(t *testing.T) {
	g, dir := testGate(t)
	// Materialize the gate file the way any ordinary command does.
	_ = mustAcquire(t, g, Shared, Options{}).Release()
	before := gateDirEntries(t, dir)
	if _, err := os.Stat(g.Path()); err != nil {
		t.Fatalf("precondition: the gate file must exist: %v", err)
	}

	const wait = time.Second
	var waited []string
	start := time.Now()
	h, err := g.Acquire(context.Background(), Shared, Options{
		ReadOnly: true,
		Wait:     wait,
		OnWait:   func(holder string) { waited = append(waited, holder) },
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("waiting ReadOnly shared Acquire of a free, existing gate: %v", err)
	}
	defer func() { _ = h.Release() }()
	if len(waited) != 0 {
		t.Errorf("the first attempt did not take the free gate: OnWait fired with %q", waited)
	}
	if elapsed >= wait {
		t.Errorf("took %s, want far under its %s budget: nothing was holding the gate", elapsed, wait)
	}
	if h.f == nil {
		t.Fatal("handle holds no file: against an existing gate file a ReadOnly acquisition is a real shared hold")
	}
	if got := gateDirEntries(t, dir); !slices.Equal(got, before) {
		t.Errorf("waiting ReadOnly acquisition changed %s\n before: %v\n after:  %v", dir, before, got)
	}
	// It is a real shared hold: a maintenance operation is turned away.
	if _, err := g.Acquire(context.Background(), Exclusive, Options{}); !errors.Is(err, ErrBusy) {
		t.Fatalf("exclusive Acquire under the ReadOnly shared holder: err = %v, want ErrBusy", err)
	}
	if err := h.Release(); err != nil {
		t.Fatalf("Release: %v", err)
	}
	if got := gateDirEntries(t, dir); !slices.Equal(got, before) {
		t.Errorf("after release %s\n before: %v\n after:  %v", dir, before, got)
	}
}

// (ii) No gate file: nothing has ever gated here. A waiting ReadOnly shared
// acquisition returns at once with an empty handle (it holds no file and has
// nothing to wait for) and leaves the directory exactly as it was: it neither
// creates the gate file nor waits for one to appear.
func TestReadOnlyWaitingSharedAcquireOfMissingGateHoldsNothing(t *testing.T) {
	g, dir := testGate(t)
	before := gateDirEntries(t, dir)
	if len(before) != 0 {
		t.Fatalf("precondition: %s must start empty, has %v", dir, before)
	}

	const wait = time.Second
	var waited []string
	start := time.Now()
	h, err := g.Acquire(context.Background(), Shared, Options{
		ReadOnly: true,
		Wait:     wait,
		OnWait:   func(holder string) { waited = append(waited, holder) },
	})
	elapsed := time.Since(start)
	if err != nil {
		t.Fatalf("waiting ReadOnly shared Acquire with no gate file: %v, want success holding nothing", err)
	}
	if h == nil || h.f != nil {
		t.Fatalf("handle = %+v, want an empty handle (no gate file was opened)", h)
	}
	if h.Mode() != Shared {
		t.Errorf("handle mode = %s, want %s", h.Mode(), Shared)
	}
	if len(waited) != 0 {
		t.Errorf("OnWait fired with %q: with no gate file there is nothing to wait for", waited)
	}
	if elapsed >= wait {
		t.Errorf("took %s, want far under its %s budget: a missing gate file is answered before any retry", elapsed, wait)
	}
	if got := gateDirEntries(t, dir); !slices.Equal(got, before) {
		t.Errorf("waiting ReadOnly acquisition changed %s\n before: %v\n after:  %v", dir, before, got)
	}
	for i := 1; i <= 2; i++ {
		if err := h.Release(); err != nil {
			t.Fatalf("Release #%d of the empty handle: %v", i, err)
		}
	}
	if got := gateDirEntries(t, dir); !slices.Equal(got, before) {
		t.Errorf("after release %s\n before: %v\n after:  %v", dir, before, got)
	}
}

// A waiting readonly hold respects its budget and writes nothing under contention.
func TestReadOnlyWaitingSharedAcquireUnderExclusiveHolderWaitsItsBudget(t *testing.T) {
	g, dir := testGate(t)
	holder := mustAcquire(t, g, Exclusive, Options{Reason: "test maintenance"})
	defer func() { _ = holder.Release() }()
	before := gateDirEntries(t, dir)

	const wait = 300 * time.Millisecond
	waited := 0
	start := time.Now()
	h, err := g.Acquire(context.Background(), Shared, Options{
		ReadOnly: true,
		Wait:     wait,
		OnWait:   func(string) { waited++ },
	})
	elapsed := time.Since(start)
	if !errors.Is(err, ErrBusy) {
		if h != nil {
			_ = h.Release()
		}
		t.Fatalf("waiting ReadOnly shared Acquire under an exclusive holder: err = %v, want ErrBusy", err)
	}
	// Acquire's deadline is taken after start, and it reports ErrBusy only
	// once that deadline has passed, so this lower bound is exact.
	if elapsed < wait {
		t.Errorf("gave up after %s, before its %s budget ran out: ReadOnly must wait like any shared acquisition", elapsed, wait)
	}
	// The loop overshoots by at most one poll interval (100ms by default).
	// The ceiling is far more generous than that so a loaded host cannot make
	// this flaky; it still catches any special-cased budget.
	if limit := wait + 2*time.Second; elapsed > limit {
		t.Errorf("gave up after %s, want about its %s budget (at most %s)", elapsed, wait, limit)
	}
	if waited != 1 {
		t.Errorf("OnWait fired %d times, want once (after the first busy attempt)", waited)
	}
	if got := gateDirEntries(t, dir); !slices.Equal(got, before) {
		t.Errorf("waiting ReadOnly acquisition changed %s\n before: %v\n after:  %v", dir, before, got)
	}
}
