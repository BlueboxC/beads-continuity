package graphstore

import (
	"errors"
	"testing"
)

func TestContinuityOperationalProfileLimits(t *testing.T) {
	ordinary, err := capacityLimits("")
	if err != nil {
		t.Fatal(err)
	}
	profile, err := capacityLimits(ContinuityProfile)
	if err != nil {
		t.Fatal(err)
	}
	if ordinary.Resources != 2048 || ordinary.RetainedBytes != 64<<20 || profile.CurrentBytes != 64<<20 || profile.RetainedRows != 2097152 || profile.RetainedBytes != 8<<30 || profile.Resources != 16384 {
		t.Fatal("ordinary acquisition contract changed")
	}
	if _, err := capacityLimits("unbounded"); err == nil {
		t.Fatal("unknown profile admitted")
	}
	// The actual workspace shapes: resource growth above 2,048 and retained
	// generations above 64 MiB require explicit policy, never automatic promotion.
	before := acquisitionUsage{resources: 2841, currentBytes: 8 << 20, continuityRows: 100000, continuityBytes: 3 << 30}
	after := before
	after.resources++
	after.continuityRows++
	after.continuityBytes += 1 << 20
	if err := checkAcquisitionGrowth(before, after); !errors.Is(err, ErrLimitExceeded) {
		t.Fatalf("ordinary growth: %v", err)
	}
	if err := checkProfileGrowth(ContinuityProfile, before, after); err != nil {
		t.Fatal(err)
	}
	at := acquisitionUsage{profile.Resources, profile.CurrentBytes, profile.RetainedRows, profile.RetainedBytes}
	for _, grow := range []func(*acquisitionUsage){func(u *acquisitionUsage) { u.resources++ }, func(u *acquisitionUsage) { u.currentBytes++ }, func(u *acquisitionUsage) { u.continuityRows++ }, func(u *acquisitionUsage) { u.continuityBytes++ }} {
		excess := at
		grow(&excess)
		if err := checkProfileGrowth(ContinuityProfile, at, excess); !errors.Is(err, ErrLimitExceeded) {
			t.Fatalf("profile growth past bound: %v", err)
		}
		if err := checkProfileGrowth(ContinuityProfile, excess, at); err != nil {
			t.Fatalf("shrink refused: %v", err)
		}
	}
}
