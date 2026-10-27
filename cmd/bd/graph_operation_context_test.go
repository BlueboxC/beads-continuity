package main

import (
	"context"
	"errors"
	"testing"
	"time"
)

func TestGraphOperationContextBudgets(t *testing.T) {
	old := graphOperationalProfile
	t.Cleanup(func() { graphOperationalProfile = old })
	for _, tc := range []struct {
		profile string
		want    time.Duration
	}{{"", 30 * time.Second}, {"continuity-v1", 2 * time.Minute}} {
		graphOperationalProfile = tc.profile
		start := time.Now()
		ctx, cancel := graphOperationContext(context.Background())
		deadline, ok := ctx.Deadline()
		cancel()
		if !ok || deadline.Before(start.Add(tc.want)) || deadline.After(time.Now().Add(tc.want)) {
			t.Fatalf("profile %q deadline %v, expected budget %s", tc.profile, deadline, tc.want)
		}
	}
}

func TestGraphOperationContextKeepsCallerBoundary(t *testing.T) {
	old := graphOperationalProfile
	graphOperationalProfile = "continuity-v1"
	t.Cleanup(func() { graphOperationalProfile = old })
	parent, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	ctx, cancel := graphOperationContext(parent)
	defer cancel()
	want, _ := parent.Deadline()
	got, _ := ctx.Deadline()
	if !got.Equal(want) {
		t.Fatalf("extended caller deadline: %v != %v", got, want)
	}
	stop()
	if !errors.Is(ctx.Err(), context.Canceled) {
		t.Fatalf("caller cancellation lost: %v", ctx.Err())
	}
}
