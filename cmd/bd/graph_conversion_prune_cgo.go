//go:build cgo

package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"path/filepath"
	"reflect"

	"github.com/steveyegge/beads/internal/codeindex"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/graphstore"
	"github.com/steveyegge/beads/memoryops"
)

func graphConversionPruneCandidate(ctx context.Context, receipt *graphConversionReceipt) (err error) {
	s, err := embeddeddolt.Open(ctx, filepath.Join(receipt.Directory, "candidate"), receipt.Plan.Store.Database, "main")
	if err != nil {
		return err
	}
	closed := false
	defer func() {
		if !closed {
			err = errors.Join(err, s.Close())
		}
	}()
	memories, err := s.Memories()
	if err != nil {
		return err
	}
	plane, err := memories.List(ctx, memoryops.ListRequest{KeyPrefix: codeindex.Prefix})
	if err != nil {
		return err
	}
	prune, err := codeindex.PlanPrune(plane.Memories)
	if err != nil {
		return err
	}
	receipt.PrunedCode = make(map[string]string, len(prune.ObsoleteKeys))
	for _, key := range prune.ObsoleteKeys {
		h := sha256.Sum256([]byte(plane.Memories[key]))
		receipt.PrunedCode[key] = hex.EncodeToString(h[:])
	}
	if _, err := codeindex.ApplyPrune(ctx, memories, plane.Memories); err != nil {
		return err
	}
	closed = true
	if err := s.Close(); err != nil {
		return err
	}
	o := graphConversionOptions(receipt.Plan)
	o.DataDir = graphConversionCandidateDir(*receipt)
	after, err := graphstore.PlanConversion(ctx, o)
	if err != nil {
		return err
	}
	expectedConfig, err := graphstore.ConversionConfigAfterPrune(ctx, graphConversionOptions(receipt.Plan), receipt.Plan.Store, receipt.PrunedCode)
	if err != nil {
		return err
	}
	if expectedConfig != after.OtherConfigSHA256 {
		return errors.New("candidate prune changed non-obsolete configuration rows")
	}
	// The official guarded prune may change only the current config projection.
	before := receipt.Plan.Store
	before.PruneObsoleteCode = false
	before.SourceDataDir = after.SourceDataDir
	before.State.Tables = cloneStringMap(before.State.Tables)
	before.State.BranchDirty = make(map[string]bool, len(after.State.BranchDirty))
	for key, value := range receipt.Plan.Store.State.BranchDirty {
		before.State.BranchDirty[key] = value
	}
	before.State.BranchDirty["main"] = after.State.BranchDirty["main"]
	before.State.Tables["config"] = after.State.Tables["config"]
	before.State.Working = after.State.Working
	before.OtherConfigSHA256 = after.OtherConfigSHA256
	if !reflect.DeepEqual(before, after) {
		return errors.New("candidate prune changed data outside the derived config projection")
	}
	after.SourceDataDir = receipt.Plan.Store.SourceDataDir
	receipt.AdoptionPlan = &after
	return nil
}
