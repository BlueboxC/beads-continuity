package graphstore

import (
	"context"
	"database/sql"
	"fmt"
)

// ContinuityProfile is an explicit per-open policy, never inferred from size,
// workspace contents, environment or a conversion receipt.
const ContinuityProfile = "continuity-v1"

type CapacityLimits struct {
	Profile       string `json:"profile"`
	Resources     uint64 `json:"resources"`
	CurrentBytes  uint64 `json:"currentBytes"`
	RetainedRows  uint64 `json:"retainedRows"`
	RetainedBytes uint64 `json:"retainedBytes"`
}

type CapacityUsage struct {
	Resources     uint64 `json:"resources"`
	CurrentBytes  uint64 `json:"currentBytes"`
	RetainedRows  uint64 `json:"retainedRows"`
	RetainedBytes uint64 `json:"retainedBytes"`
}

type CapacityStatus struct {
	Limits CapacityLimits `json:"limits"`
	Usage  CapacityUsage  `json:"usage"`
}

// ReadCapacity measures logical sizes in one checked SQL snapshot without
// loading retained generations into Go or changing admission policy.
func (s *Store) ReadCapacity(ctx context.Context) (CapacityStatus, error) {
	var usage acquisitionUsage
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		usage, err = readAcquisitionUsage(ctx, tx)
		return err
	})
	if err != nil {
		return CapacityStatus{}, err
	}
	return CapacityStatus{s.CapacityLimits(), CapacityUsage{usage.resources, usage.currentBytes, usage.continuityRows, usage.continuityBytes}}, nil
}

func capacityLimits(profile string) (CapacityLimits, error) {
	switch profile {
	case "":
		return CapacityLimits{"ordinary", PreviewSnapshotLimit, PreviewCurrentReadByteLimit, PreviewContinuityReadRowLimit, PreviewContinuityReadByteLimit}, nil
	case ContinuityProfile:
		// The existing selected read ceilings do not change. Retained config is
		// measured in SQL, not hydrated as one read or pruned to fit a read.
		return CapacityLimits{ContinuityProfile, 16384, PreviewContinuityReadByteLimit, 2097152, 8 << 30}, nil
	default:
		return CapacityLimits{}, fmt.Errorf("%w: unsupported graph operational profile %q", ErrInvalidStore, profile)
	}
}

func (s *Store) CapacityLimits() CapacityLimits {
	limits, _ := capacityLimits(s.options.OperationalProfile) // validated before opening
	return limits
}

// OperationalSnapshot explicitly acquires the profile's complete graph. The
// ordinary CurrentSnapshot and full-plane memory List keep their own ceilings.
func (s *Store) OperationalSnapshot(ctx context.Context) (Snapshot, error) {
	var result Snapshot
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		var err error
		result, err = s.operationalSnapshotInTx(ctx, tx)
		return err
	})
	if err != nil {
		return Snapshot{}, err
	}
	return result, nil
}

func checkProfileGrowth(profile string, before, after acquisitionUsage) error {
	limits, err := capacityLimits(profile)
	if err != nil {
		return err
	}
	return checkGrowthDimensions(before, after, limits)
}

func (s *Store) operationalSnapshotInTx(ctx context.Context, tx *sql.Tx) (Snapshot, error) {
	limits := s.CapacityLimits()
	return s.currentSnapshotWithBoundsInTx(ctx, tx, int(limits.Resources), limits.CurrentBytes)
}
