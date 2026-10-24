package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
)

// installedNativeBindings validates immutable native adapter bindings beyond
// the fixed fresh baseline. No public Type writer or executable schema is
// admitted: every byte must equal the descriptor derived from the native URI.
func installedNativeBindings(ctx context.Context, tx *sql.Tx, scope string, baseline []graph.TypeDescriptor) ([]graph.TypeDescriptor, error) {
	// The fixed rows were validated by installedPreviewTypes. Keep SQL static;
	// exclude their checked names while iterating this bounded catalog instead.
	baselineNames := map[string]bool{}
	for _, d := range previewTypeDefinitions() {
		baselineNames[d.name] = true
	}
	rows, err := tx.QueryContext(ctx, `SELECT name,CASE WHEN OCTET_LENGTH(descriptor)<=? THEN descriptor ELSE NULL END,fingerprint FROM graph_preview_types`, len(scope)*4+8192)
	if err != nil {
		return nil, err
	}
	seen := map[string]bool{}
	for _, d := range baseline {
		seen[d.ID()] = true
	}
	var result []graph.TypeDescriptor
	for rows.Next() {
		var name, fingerprint string
		var raw []byte
		if err := rows.Scan(&name, &raw, &fingerprint); err != nil {
			return nil, errors.Join(err, rows.Close())
		}
		if baselineNames[name] {
			continue
		}
		if !strings.HasPrefix(name, "native-binding-") || !authorityID.MatchString(strings.TrimPrefix(name, "native-binding-")) || len(raw) == 0 {
			return nil, errors.Join(fmt.Errorf("%w: invalid native Type binding", ErrInvalidStore), rows.Close())
		}
		parsed, err := graph.ParseTypeDescriptor(raw)
		if err != nil {
			return nil, errors.Join(fmt.Errorf("%w: malformed native Type binding: %v", ErrInvalidStore, err), rows.Close())
		}
		expected, err := nativeConcreteDescriptor(scope, parsed.ID())
		if err != nil || seen[parsed.ID()] || !bytes.Equal(raw, expected.CanonicalJSON()) || fingerprint != expected.Fingerprint() {
			return nil, errors.Join(fmt.Errorf("%w: native Type binding differs from its authoritative mapping", ErrInvalidStore), rows.Close())
		}
		seen[parsed.ID()] = true
		result = append(result, parsed)
	}
	return result, errors.Join(rows.Err(), rows.Close())
}

// ensureNativeTypeInTx runs only in the native writer's guarded transaction.
// The opaque internal key is not a Type identity. Actual identities retain the
// full injective encoding, and descriptors cannot be edited independently.
func (s *Store) ensureNativeTypeInTx(ctx context.Context, tx *sql.Tx, id string) error {
	expected, err := nativeConcreteDescriptor(s.ScopeURL(), id)
	if err != nil {
		return err
	}
	for _, definition := range previewTypeDefinitions() {
		if id == s.ScopeURL()+"types/"+definition.name {
			return nil
		}
	}
	installed, err := installedPreviewTypes(ctx, tx, s.ScopeURL())
	if err != nil {
		return err
	}
	for _, d := range installed {
		if d.ID() == id {
			return nil
		}
	}
	// A Type declaration and a Resource allocation cannot share an identity,
	// including a deleted allocation. Do not acquire its payload or rewrite it.
	allocated, err := publicResourceIDExistsInTx(ctx, tx, strings.TrimPrefix(id, s.ScopeURL()))
	if err != nil {
		return err
	}
	if allocated {
		return fmt.Errorf("%w: concrete native Type identity is already allocated to a Resource", ErrAlreadyExists)
	}
	if len(installed) >= len(previewTypeDefinitions())+PreviewSnapshotLimit {
		return fmt.Errorf("%w: native Type binding inventory exceeds acquisition bound", ErrLimitExceeded)
	}
	token, err := freshToken()
	if err != nil {
		return err
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO graph_preview_types (name,descriptor,fingerprint) VALUES (?,?,?)`, "native-binding-"+token, expected.CanonicalJSON(), expected.Fingerprint())
	return err
}

// requireNativeBindingInTx checks a current/retained affiliation without
// acquiring unrelated native payloads or whole-workspace current records.
// The caller has already checked the fixed baseline in this transaction.
func (s *Store) requireNativeBindingInTx(ctx context.Context, tx *sql.Tx, id string) error {
	expected, err := nativeConcreteDescriptor(s.ScopeURL(), id)
	if err != nil {
		return fmt.Errorf("%w: native affiliation has no concrete descriptor", ErrInvalidStore)
	}
	for _, definition := range previewTypeDefinitions() {
		if id == s.ScopeURL()+"types/"+definition.name {
			return nil
		}
	}
	var raw []byte
	var name string
	err = tx.QueryRowContext(ctx, `SELECT name,descriptor FROM graph_preview_types WHERE fingerprint=? AND OCTET_LENGTH(descriptor)=?`, expected.Fingerprint(), len(expected.CanonicalJSON())).Scan(&name, &raw)
	if err != nil || !strings.HasPrefix(name, "native-binding-") || !bytes.Equal(raw, expected.CanonicalJSON()) {
		return fmt.Errorf("%w: native affiliation has no checked retained binding: %v", ErrInvalidStore, err)
	}
	return nil
}
