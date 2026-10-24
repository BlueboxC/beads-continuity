package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"sort"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
)

// ListInstalledTypes returns checked fixed/retained bindings and currently
// configured unused native Issue declarations in stable ID order. This read
// never installs a binding; partial or older generations are refused.
func (s *Store) ListInstalledTypes(ctx context.Context) ([]graph.TypeDescriptor, error) {
	var descriptors []graph.TypeDescriptor
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		descriptors, err = s.selectedDescriptorsInTx(ctx, tx)
		return err
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(descriptors, func(i, j int) bool {
		return graph.CompareCodeUnits(descriptors[i].ID(), descriptors[j].ID()) < 0
	})
	return descriptors, nil
}

// ReadType reads a checked selected declaration. Native adapter definitions are
// deterministic; used concrete bindings persist immutably across configuration
// edits and retained reads. Configured unused classes may be derived, and their
// visibility does not authorize new native use. This accessor never registers
// or edits a Type, and every record validates its concrete binding.
func (s *Store) ReadType(ctx context.Context, path string) (graph.TypeDescriptor, error) {
	if !strings.HasPrefix(path, "types/") {
		return graph.TypeDescriptor{}, fmt.Errorf("%w: expected a local types/ path", graph.ErrValidation)
	}
	// These private installed paths share the canonical identifier-segment
	// grammar of Bead paths; do not normalize or admit URL query selectors.
	if err := validatePath("beads/" + strings.TrimPrefix(path, "types/")); err != nil {
		return graph.TypeDescriptor{}, fmt.Errorf("%w: invalid Type path: %v", graph.ErrValidation, err)
	}
	id := graph.CanonicalURL(s.options.Binding.ScopeURL, path)
	var descriptor graph.TypeDescriptor
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		var err error
		descriptor, err = s.readTypeInTx(ctx, tx, id)
		return err
	})
	if err != nil {
		return graph.TypeDescriptor{}, err
	}
	return descriptor, nil
}

// readTypeInTx requires the caller to validate the binding in this transaction.
// Concrete native identities are authoritative adapter-derived descriptors;
// their existence does not bypass native classification admission.
func (s *Store) readTypeInTx(ctx context.Context, tx *sql.Tx, id string) (graph.TypeDescriptor, error) {
	// Resolve against the complete selected installer definition set, rather than
	// a hardcoded command alias switch or an unbounded persisted descriptor.
	var name string
	var expected graph.TypeDescriptor
	for _, definition := range previewTypeDefinitions() {
		if id != s.ScopeURL()+"types/"+definition.name {
			continue
		}
		candidate, err := definition.build(s.ScopeURL())
		if err != nil {
			return graph.TypeDescriptor{}, err
		}
		if candidate.ID() != id {
			return graph.TypeDescriptor{}, fmt.Errorf("%w: selected Type identity differs", ErrInvalidStore)
		}
		name, expected = definition.name, candidate
		break
	}
	if name == "" {
		if isConcreteNativeIssue(s.ScopeURL(), id) || isConcreteNativeDependency(s.ScopeURL(), id) {
			descriptors, err := s.selectedDescriptorsInTx(ctx, tx)
			if err != nil {
				return graph.TypeDescriptor{}, err
			}
			for _, d := range descriptors {
				if d.ID() == id {
					return d, nil
				}
			}
			return graph.TypeDescriptor{}, ErrNotFound
		}
		return graph.TypeDescriptor{}, ErrNotFound
	}
	var raw []byte
	var fingerprint string
	if err := tx.QueryRowContext(ctx, `SELECT descriptor,fingerprint FROM graph_preview_types WHERE name=? AND OCTET_LENGTH(descriptor)=?`, name, len(expected.CanonicalJSON())).Scan(&raw, &fingerprint); err != nil {
		return graph.TypeDescriptor{}, fmt.Errorf("%w: read installed %s descriptor: %v", ErrInvalidStore, name, err)
	}
	parsed, err := graph.ParseTypeDescriptor(raw)
	if err != nil {
		return graph.TypeDescriptor{}, fmt.Errorf("%w: invalid installed %s descriptor: %v", ErrInvalidStore, name, err)
	}
	if parsed.ID() != id || !bytes.Equal(parsed.CanonicalJSON(), raw) || !bytes.Equal(raw, expected.CanonicalJSON()) || parsed.Fingerprint() != fingerprint || fingerprint != expected.Fingerprint() {
		return graph.TypeDescriptor{}, fmt.Errorf("%w: installed %s descriptor identity or fingerprint differs", ErrInvalidStore, name)
	}
	return parsed, nil
}

// ScopeURL returns the workspace-bound canonical identity, not a transport or
// listener address. Every read verifies this binding against persisted state.
func (s *Store) ScopeURL() string { return s.options.Binding.ScopeURL }
