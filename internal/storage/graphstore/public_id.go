package graphstore

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
)

// A public ID is reserved across Issues, Memories, and Links for the Scope's
// lifetime, including deleted allocations. Native Issue backing keys and explicit
// Memory public_id values are identities; legacy NULL Memory/Link allocations
// continue to publish their original path. The writer_token coordination fence
// serializes check-and-insert reservations; no suffix alias is reserved.
func publicResourceIDExistsInTx(ctx context.Context, tx *sql.Tx, id string) (bool, error) {
	if err := validatePublicID(id); err != nil {
		return false, err
	}
	// The discovery document and installed descriptors already own these
	// exact public URLs. Prefix families are not reserved: spelling never
	// establishes Resource kind.
	if id == "bdp.json" {
		return true, nil
	}
	var authority int
	err := tx.QueryRowContext(ctx, `SELECT 1 FROM graph_preview_types
 WHERE CAST(JSON_UNQUOTE(JSON_EXTRACT(CAST(descriptor AS CHAR),'$.id')) AS BINARY) =
 CONCAT((SELECT CAST(scope_url AS BINARY) FROM graph_preview_scope WHERE singleton=1),CAST(? AS BINARY)) LIMIT 1`, id).Scan(&authority)
	if err == nil {
		return true, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var exists int
	err = tx.QueryRowContext(ctx, `SELECT 1 FROM graph_preview_catalog
		WHERE (
			(backing='issue' AND backing_key=?) OR
			(backing IN ('generic','dependency','informational') AND COALESCE(public_id,path)=?)
		) LIMIT 1`, id, id).Scan(&exists)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	return err == nil, err
}

// ResolveAllocatedResourcePath finds a public ID even after its Resource has
// been deleted. Historical update needs the durable allocation path before
// its checked writer can compare the terminal revision and restore content.
// An exact public ID wins over old Bead/Link suffix shorthand.
func (s *Store) ResolveAllocatedResourcePath(ctx context.Context, id string) (string, error) {
	return s.resolveAllocatedResourcePath(ctx, id, true)
}

// ResolveExactAllocatedResourcePath excludes compatibility suffixes for a
// write that will commit in a later transaction. The returned allocation is
// stable for this Scope's lifetime, including across deletion and recovery.
func (s *Store) ResolveExactAllocatedResourcePath(ctx context.Context, id string) (string, error) {
	return s.resolveAllocatedResourcePath(ctx, id, false)
}

func (s *Store) resolveAllocatedResourcePath(ctx context.Context, id string, allowShorthand bool) (string, error) {
	if err := validatePublicID(id); err != nil {
		return "", err
	}
	var selected string
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		rows, err := tx.QueryContext(ctx, `SELECT path FROM graph_preview_catalog WHERE
			(backing='issue' AND backing_key=?) OR
			(backing IN ('generic','dependency','informational') AND COALESCE(public_id,path)=?) LIMIT 2`, id, id)
		if err != nil {
			return err
		}
		for rows.Next() {
			var path string
			if err := rows.Scan(&path); err != nil {
				_ = rows.Close()
				return err
			}
			if selected != "" {
				_ = rows.Close()
				return fmt.Errorf("%w: public ID has multiple graph allocations", ErrInvalidStore)
			}
			selected = path
		}
		if err := errors.Join(rows.Err(), rows.Close()); err != nil {
			return err
		}
		if selected != "" {
			return nil
		}
		if !allowShorthand {
			return ErrNotFound
		}
		// Suffixes are compatibility syntax, never public identity. A native
		// Issue's private path cannot answer an unqualified local selector.
		for _, candidate := range []struct{ path, backing string }{{"beads/" + id, "generic"}, {"links/" + id, "link"}} {
			var backing string
			err := tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=? AND public_id IS NULL`, candidate.path).Scan(&backing)
			if errors.Is(err, sql.ErrNoRows) {
				continue
			}
			if err != nil {
				return err
			}
			if candidate.backing == "generic" && backing != "generic" ||
				candidate.backing == "link" && backing != "dependency" && backing != "informational" {
				continue
			}
			if selected != "" {
				return fmt.Errorf("%w: local shorthand identifies multiple Resources; use a public ID or canonical Scope URL", storage.ErrValidation)
			}
			selected = candidate.path
		}
		if selected == "" {
			return ErrNotFound
		}
		return nil
	})
	return selected, err
}

// validatePublicID is the common Preview 3 admission boundary. Legacy key
// acceptance is broader; incompatible inputs name their exact spelling and
// reason rather than being renamed, encoded, trimmed or truncated.
func validatePublicID(id string) error {
	if !utf8.ValidString(id) || strings.TrimSpace(id) == "" {
		return fmt.Errorf("%w: incompatible public ID %q: expected nonblank UTF-8; choose an explicit compatible replacement ID", storage.ErrValidation, id)
	}
	if err := graph.ValidateCanonicalResourceID(id); err != nil {
		return fmt.Errorf("%w: incompatible public ID %q: %w; choose an explicit compatible replacement ID", storage.ErrValidation, id, err)
	}
	return nil
}

// PrepareMemoryIdentity prepares a private locator for an explicit public ID.
// It does not reserve the identity: Create/createInTx reserves atomically with
// payload and history. Importers can prepare requests before their batch tx.
func PrepareMemoryIdentity(req CreateRequest) (CreateRequest, error) {
	if req.PublicID == "" {
		return req, nil
	}
	if err := validatePublicID(req.PublicID); err != nil {
		return CreateRequest{}, err
	}
	if req.Path == "" {
		var err error
		req.Path, err = preferredPrivateResourcePath(graph.KindBead, req.PublicID)
		if err != nil {
			return CreateRequest{}, err
		}
		req.privatePathGenerated = true
	}

	return req, nil
}

func preferredPrivateResourcePath(kind graph.ResourceKind, publicID string) (string, error) {
	if err := validatePublicID(publicID); err != nil {
		return "", err
	}
	root, validate := "beads/", validatePath
	if kind == graph.KindLink {
		root, validate = "links/", validateLinkPath
	} else if kind != graph.KindBead {
		return "", fmt.Errorf("%w: invalid private allocation kind", storage.ErrValidation)
	}
	for _, path := range []string{publicID, root + publicID} {
		if validate(path) == nil {
			return path, nil
		}
	}
	token, err := freshToken()
	if err != nil {
		return "", err
	}
	return root + token, nil
}

func prepareLinkIdentity(path, publicID string) (string, bool, error) {
	if publicID == "" {
		return path, false, nil
	}
	if err := validatePublicID(publicID); err != nil {
		return "", false, err
	}
	if path != "" {
		return path, false, validateLinkPath(path)
	}
	path, err := preferredPrivateResourcePath(graph.KindLink, publicID)
	return path, true, err
}

func choosePrivateResourcePathInTx(ctx context.Context, tx *sql.Tx, path string, generated bool, kind graph.ResourceKind) (string, error) {
	for attempt := 0; ; attempt++ {
		var occupied int
		err := tx.QueryRowContext(ctx, `SELECT 1 FROM graph_preview_catalog WHERE path=?`, path).Scan(&occupied)
		if errors.Is(err, sql.ErrNoRows) {
			return path, nil
		}
		if err != nil {
			return "", err
		}
		if !generated || attempt >= 8 {
			return "", ErrAlreadyExists
		}
		token, err := freshToken()
		if err != nil {
			return "", err
		}
		root := "beads/"
		if kind == graph.KindLink {
			root = "links/"
		}
		path = root + token
	}
}

func memoryPublicIDInTx(ctx context.Context, tx *sql.Tx, path string) (string, error) {
	var size uint64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(OCTET_LENGTH(public_id),0) FROM graph_preview_catalog WHERE path=?`, path).Scan(&size); err != nil {
		return "", fmt.Errorf("%w: public ID allocation size: %v", ErrInvalidStore, err)
	}
	if size > PreviewCurrentReadByteLimit {
		return "", ErrLimitExceeded
	}
	var id sql.NullString
	if err := tx.QueryRowContext(ctx, `SELECT public_id FROM graph_preview_catalog WHERE path=?`, path).Scan(&id); err != nil {
		return "", fmt.Errorf("%w: public ID allocation is missing: %v", ErrInvalidStore, err)
	}
	if !id.Valid {
		return "", nil
	}
	if err := validatePublicID(id.String); err != nil {
		return "", fmt.Errorf("%w: invalid allocation public ID", ErrInvalidStore)
	}
	return id.String, nil
}

// ResolveMemorySelector accepts an exact admitted public ID or its complete
// canonical Scope URL. Those syntaxes are disjoint under the common grammar.
// Returned private allocation paths never change or become reusable.
func (s *Store) ResolveMemorySelector(ctx context.Context, selector string) (string, error) {
	id := selector
	if strings.HasPrefix(selector, s.ScopeURL()) {
		var err error
		id, err = graph.ScopeLocalPublicResourceID(s.ScopeURL(), selector)
		if err != nil {
			return "", err
		}
	}
	if err := validatePublicID(id); err != nil {
		return "", err
	}
	var selected string
	err := s.withTx(ctx, false, func(tx *sql.Tx) error {
		if err := checkBinding(ctx, tx, s.options); err != nil {
			return err
		}
		if err := checkCurrentReadBytes(ctx, tx); err != nil {
			return err
		}
		identity, err := resolveAllocatedResourceIdentityInTx(ctx, tx, id)
		if err != nil {
			return err
		}
		var backing string
		if err := tx.QueryRowContext(ctx, `SELECT backing FROM graph_preview_catalog WHERE path=?`, identity.PrivatePath).Scan(&backing); err != nil {
			return err
		}
		if backing != "generic" {
			return fmt.Errorf("%w: selected public ID is not a Memory", ErrCapabilityUnavailable)
		}
		selected = identity.PrivatePath
		return nil
	})
	return selected, err
}
