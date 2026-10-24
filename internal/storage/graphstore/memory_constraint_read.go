package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memorycatalog"
	"github.com/steveyegge/beads/internal/memoryquery"
	"github.com/steveyegge/beads/internal/typecatalog"
)

// CurrentSubjectsRequest selects direct incoming declarations to Constraint
// roots. Budgets and current-only pin policy are those of CurrentFamilyRequest.
type CurrentSubjectsRequest struct {
	Roots    []string
	Limits   memoryquery.Limits
	MaxBytes uint64
}

// CurrentConstraintRoot is one original subject's independent staged read slot.
type CurrentConstraintRoot = memoryquery.ConstraintRoot

type CurrentConstraintsRequest struct {
	Roots    []CurrentConstraintRoot
	Limits   memoryquery.Limits
	MaxBytes uint64
}

// CurrentConstraintsRead carries explicit phase membership, parallel Subject To
// declarations and Requires-stage frontiers alongside one canonical record view.
// Stages and Graph.Views share root-slot order. Renderers must not reconstruct
// phase membership from their unlabelled union or embedded Owned copies.
type CurrentConstraintsRead struct {
	CurrentFamilyRead
	Stages []memoryquery.ConstraintStage
}

// ReadCurrentSubjects reads direct declarations only, including incoming Links
// not owned by the selected Constraint. It does not traverse between subjects.
func (s *Store) ReadCurrentSubjects(ctx context.Context, req CurrentSubjectsRequest) (CurrentFamilyRead, error) {
	if ctx == nil {
		return CurrentFamilyRead{}, errors.New("graph operation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return CurrentFamilyRead{}, err
	}
	if len(req.Roots) == 0 || len(req.Roots) > PreviewSnapshotLimit {
		return CurrentFamilyRead{}, fmt.Errorf("%w: invalid current root count", graph.ErrValidation)
	}
	slots := make([]memoryquery.CurrentRoot, len(req.Roots))
	for i, path := range req.Roots {
		slots[i] = memoryquery.CurrentRoot{Path: path}
	}
	shared := CurrentFamilyRequest{Roots: slots, Direction: graph.DirectionIn, Limits: req.Limits, MaxBytes: req.MaxBytes}
	if err := validateCurrentFamilyRequest(shared); err != nil {
		return CurrentFamilyRead{}, err
	}
	var result CurrentFamilyRead
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		r, err := s.newCurrentFamilyReader(ctx, tx, shared)
		if err != nil {
			return err
		}
		for _, root := range req.Roots {
			n, err := r.Current(ctx, root)
			if err != nil {
				return err
			}
			if !r.isConstraint(n.TypeURL) {
				return fmt.Errorf("%w: subjects root must be Constraint", graph.ErrValidation)
			}
		}
		g, err := memoryquery.CurrentSubjects(ctx, r, r.family(typecatalog.Reference{URI: r.catalog.projectionBinding(memorycatalog.SubjectTo)}), req.Roots, req.Limits)
		if err != nil {
			return err
		}
		result, err = r.materialize(ctx, g)
		return err
	})
	if err = currentReadError(ctx, err); err != nil {
		return CurrentFamilyRead{}, err
	}
	return result, nil
}

// ReadCurrentConstraints performs both fixed phases in one rollback-only
// transaction with a shared current reader and cumulative budgets. Its roots may
// be any currently eligible Memory or native Issue. Only the original roots'
// outgoing Subject To declarations seed each Requires/Defines closure. This is
// discovery of declared constraints, not an assessment of satisfaction.
func (s *Store) ReadCurrentConstraints(ctx context.Context, req CurrentConstraintsRequest) (CurrentConstraintsRead, error) {
	if ctx == nil {
		return CurrentConstraintsRead{}, errors.New("graph operation requires a context")
	}
	if err := ctx.Err(); err != nil {
		return CurrentConstraintsRead{}, err
	}
	if len(req.Roots) == 0 || len(req.Roots) > PreviewSnapshotLimit {
		return CurrentConstraintsRead{}, fmt.Errorf("%w: invalid current root count", graph.ErrValidation)
	}
	slots := make([]memoryquery.CurrentRoot, len(req.Roots))
	for i, root := range req.Roots {
		slots[i] = memoryquery.CurrentRoot{Path: root.Path, Depth: root.RequiresDepth}
	}
	shared := CurrentFamilyRequest{Roots: slots, Direction: graph.DirectionOut, Limits: req.Limits, MaxBytes: req.MaxBytes}
	if err := validateCurrentFamilyRequest(shared); err != nil {
		return CurrentConstraintsRead{}, err
	}
	var result CurrentConstraintsRead
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		r, err := s.newCurrentFamilyReader(ctx, tx, shared)
		if err != nil {
			return err
		}
		subjectTo := r.family(typecatalog.Reference{URI: r.catalog.projectionBinding(memorycatalog.SubjectTo)})
		requires := r.family(typecatalog.Reference{URI: r.catalog.projectionBinding(memorycatalog.Requires)})
		g, err := memoryquery.CurrentConstraints(ctx, r, subjectTo, requires, req.Roots, req.Limits)
		if err != nil {
			return err
		}
		result.CurrentFamilyRead, err = r.materialize(ctx, g.Graph)
		if err != nil {
			return err
		}
		result.Stages = g.Stages
		return nil
	})
	if err = currentReadError(ctx, err); err != nil {
		return CurrentConstraintsRead{}, err
	}
	return result, nil
}
