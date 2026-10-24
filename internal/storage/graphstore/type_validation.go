package graphstore

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/steveyegge/beads/internal/typecatalog"
	"github.com/steveyegge/beads/internal/typeconstraints"
)

// UnmarshalJSON retains the complete properties object. Type-specific
// admission belongs to the selected catalog, rather than this common carrier.
// Required title/body shapes remain common to all Memory refinements.
func (p *Properties) UnmarshalJSON(raw []byte) error {
	canonical, err := graph.CanonicalizeJSON(raw)
	if err != nil {
		return err
	}
	if len(canonical) == 0 || canonical[0] != '{' {
		return fmt.Errorf("%w: Memory properties must be an object", graph.ErrValidation)
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(canonical, &fields); err != nil {
		return err
	}
	var next Properties
	for _, field := range []struct {
		name        string
		destination *string
	}{{"title", &next.Title}, {"body", &next.Body}} {
		value, present := fields[field.name]
		if !present || len(value) == 0 || value[0] != '"' {
			return fmt.Errorf("%w: Memory %s must be a string", graph.ErrValidation, field.name)
		}
		if err := json.Unmarshal(value, field.destination); err != nil {
			return err
		}
		delete(fields, field.name)
	}
	if len(fields) != 0 {
		extra, err := canonicalJSON(fields)
		if err != nil {
			return err
		}
		next.extra = string(extra)
	}
	*p = next
	return nil
}

// validateTypeStateInTx runs after staged writes and before commit (or dry-run
// rollback). All operations see one final population, so incident Link checks,
// family counts and cycles also cover deletions and Type changes atomically.
func (s *Store) validateTypeStateInTx(ctx context.Context, tx *sql.Tx) error {
	var relevant bool
	if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM graph_preview_catalog WHERE resource_kind='bead' AND allocation_state='live' AND backing='generic') OR EXISTS(SELECT 1 FROM graph_preview_links)`).Scan(&relevant); err != nil {
		return err
	}
	if !relevant {
		// The native-only case has no selected Memory population. Its existing
		// writers retain payload, history and Dependency validation unchanged.
		return nil
	}
	if err := checkTypeValidationBytes(ctx, tx); err != nil {
		return err
	}
	if err := s.checkLinkMappingsInTx(ctx, tx); err != nil {
		return err
	}
	c, err := s.selectedTypes()
	if err != nil {
		return err
	}
	var beads []typeValidationBead
	rows, err := tx.QueryContext(ctx, `SELECT c.path,c.type_url,c.backing,CASE WHEN c.backing='generic' THEN p.properties ELSE NULL END
 FROM graph_preview_catalog c LEFT JOIN graph_preview_payloads p ON p.path=c.path
 WHERE `+typeValidationBeads+` ORDER BY c.path`)
	if err != nil {
		return err
	}
	for rows.Next() {
		value, err := scanTypeValidationBead(rows)
		if err != nil {
			_ = rows.Close()
			return err
		}
		beads = append(beads, value)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	beadTypes := map[string]typecatalog.Reference{}
	for _, value := range beads {
		ref := typecatalog.Reference{URI: value.typ}
		if err := c.types.ValidateDeclaredType(ref, graph.KindBead); value.backing != "issue" && err != nil {
			return fmt.Errorf("%w: %v", storage.ErrValidation, err)
		}
		beadTypes[value.path] = typecatalog.Reference{URI: nativeFamilyReference(s.ScopeURL(), value.typ)}
		if value.backing == "generic" {
			if !c.memoryType(value.typ) {
				return fmt.Errorf("%w: unsupported generic Bead Type", ErrInvalidStore)
			}
			if err := c.validateProperties(value.typ, value.properties, graph.KindBead); err != nil {
				return err
			}
			if err := s.validateExactSubjectBindings(ctx, tx, c, value.path, value.typ, value.properties); err != nil {
				return err
			}
		} else if value.backing != "issue" || !isConcreteNativeIssue(s.ScopeURL(), value.typ) {
			return fmt.Errorf("%w: unsupported native Bead Type", ErrInvalidStore)
		}
	}
	type link struct {
		path, typ, source, target, sourcePin, targetPin string
		properties                                      json.RawMessage
	}
	var links []link
	rows, err = tx.QueryContext(ctx, `SELECT l.path,c.type_url,l.source_path,l.target_path,l.source_revision,l.target_revision,l.properties
 FROM graph_preview_links l JOIN graph_preview_catalog c ON c.path=l.path
 WHERE c.resource_kind='link' AND c.allocation_state='live' AND c.backing='informational' ORDER BY l.path`)
	if err != nil {
		return err
	}
	for rows.Next() {
		var value link
		if err := rows.Scan(&value.path, &value.typ, &value.source, &value.target, &value.sourcePin, &value.targetPin, &value.properties); err != nil {
			_ = rows.Close()
			return err
		}
		links = append(links, value)
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return err
	}
	var population []typeconstraints.Link
	for _, link := range links {
		ref := typecatalog.Reference{URI: link.typ}
		if !IsInformationalTypeURL(s.ScopeURL(), link.typ) {
			return fmt.Errorf("%w: unsupported informational Link Type", ErrInvalidStore)
		}
		if err := c.validateProperties(link.typ, link.properties, graph.KindLink); err != nil {
			return err
		}
		source, sourceOK := beadTypes[link.source]
		target, targetOK := beadTypes[link.target]
		if !sourceOK || !targetOK {
			return fmt.Errorf("%w: Link endpoints require live local Beads", storage.ErrValidation)
		}

		// A local exact endpoint contributes the Type of its selected retained
		// state, not a later current retyping of the same identity.
		if link.sourcePin != "" {
			exact, err := s.readVersionInTx(ctx, tx, link.source, link.sourcePin)
			if err != nil {
				return err
			}
			source = typecatalog.Reference{URI: nativeFamilyReference(s.ScopeURL(), recordType(exact))}
			// A later retyping must not turn a historical owned-source
			// Reference into an unowned claim and bypass the selected deferral.
			owned, err := c.types.Owns(source, ref)
			if err != nil {
				return err
			}
			if owned {
				return fmt.Errorf("%w: pinned owned source observation point is not selected", ErrCapabilityUnavailable)
			}
		}
		if link.targetPin != "" {
			exact, err := s.readVersionInTx(ctx, tx, link.target, link.targetPin)
			if err != nil {
				return err
			}
			target = typecatalog.Reference{URI: nativeFamilyReference(s.ScopeURL(), recordType(exact))}
		}
		if err := c.types.ValidateEndpoints(ref,
			typecatalog.EndpointFacts{ResourceKind: "bead", Scope: "same", Type: source},
			typecatalog.EndpointFacts{ResourceKind: "bead", Scope: "same", Type: target}); err != nil {
			return fmt.Errorf("%w: %v", storage.ErrValidation, err)
		}
		population = append(population, typeconstraints.Link{
			Identity: typeconstraints.Identity{Scope: s.ScopeURL(), Path: link.path}, Type: ref,
			Source: typeconstraints.Endpoint{Identity: typeconstraints.Identity{Scope: s.ScopeURL(), Path: link.source}, Revision: link.sourcePin},
			Target: typeconstraints.Endpoint{Identity: typeconstraints.Identity{Scope: s.ScopeURL(), Path: link.target}, Revision: link.targetPin},
		})
	}
	// Native Dependency is not a refinement of any selected Memory Link
	// family. Its native writer remains responsible for that separate domain;
	// the population above contains every member of every rule family here.
	if err := c.graph.Validate(population); err != nil {
		return fmt.Errorf("%w: %v", storage.ErrValidation, err)
	}
	return nil
}

type typeValidationBead struct {
	path, typ, backing string
	properties         json.RawMessage
}

// The query deliberately returns SQL NULL for native payloads. database/sql
// supports NULL through *[]byte, but not directly through *json.RawMessage.
// Only generic properties become JSON input to the selected Type validator.
func scanTypeValidationBead(rows *sql.Rows) (typeValidationBead, error) {
	var value typeValidationBead
	var properties []byte
	if err := rows.Scan(&value.path, &value.typ, &value.backing, &properties); err != nil {
		return typeValidationBead{}, err
	}
	if value.backing == "generic" {
		value.properties = json.RawMessage(properties)
	}
	return value, nil
}

// Only selected Memory payloads and actual informational endpoint Type facts
// are acquired. Native Issue payload/history and unrelated native records are
// neither decoded nor charged against this validator's acquisition budget.
const typeValidationBeads = `c.resource_kind='bead' AND c.allocation_state='live' AND
 (c.backing='generic' OR EXISTS(SELECT 1 FROM graph_preview_links l WHERE l.source_path=c.path OR l.target_path=c.path))`

func checkTypeValidationBytes(ctx context.Context, tx *sql.Tx) error {
	queries := []currentReadSizeQuery{
		{"graph_preview_catalog c LEFT JOIN graph_preview_payloads p ON p.path=c.path WHERE " + typeValidationBeads,
			"c.path,c.type_url,c.backing,CASE WHEN c.backing='generic' THEN p.properties ELSE NULL END", 1},
		// Include orphan rows in the pre-acquisition bound. Mapping validation
		// refuses them before the payload query rather than hiding them in joins.
		{"graph_preview_links l LEFT JOIN graph_preview_catalog c ON c.path=l.path",
			"l.path,c.type_url,l.source_path,l.target_path,l.source_revision,l.target_revision,l.properties", 1},
	}
	queries = append(queries, pinnedEndpointSizeQueries()...)
	var used uint64
	for _, query := range queries {
		fields := []string{"256"}
		for _, column := range strings.Split(query.columns, ",") {
			fields = append(fields, "COALESCE(OCTET_LENGTH("+strings.TrimSpace(column)+"),0)")
		}
		var size uint64
		if err := tx.QueryRowContext(ctx, "SELECT CAST(LEAST(COALESCE(SUM("+strings.Join(fields, "+")+"),0),?) AS UNSIGNED) FROM "+query.from, PreviewCurrentReadByteLimit+1).Scan(&size); err != nil {
			return err
		}
		if size > PreviewCurrentReadByteLimit-used {
			return fmt.Errorf("%w: Type validation population exceeds its %d-byte acquisition budget", ErrLimitExceeded, PreviewCurrentReadByteLimit)
		}
		used += size
	}
	return nil
}

type exactSubjectBinding struct {
	URI      string `json:"uri"`
	Revision string `json:"revision"`
}

func (s *Store) validateExactSubjectBindings(ctx context.Context, tx *sql.Tx, catalog *selectedTypeCatalog, ownerPath, typ string, properties json.RawMessage) error {
	declaration := catalog.definitions[typ]
	for _, property := range declaration.Properties {
		if property.Shape != "exact-bead-references" {
			continue
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(properties, &fields); err != nil {
			return fmt.Errorf("%w: malformed subject properties", storage.ErrValidation)
		}
		var references []exactSubjectBinding
		if json.Unmarshal(fields[property.Name], &references) != nil || len(references) == 0 {
			return fmt.Errorf("%w: nonempty exact subject bindings required", storage.ErrValidation)
		}
		for _, ref := range references {
			path, err := s.publicResourcePathInTx(ctx, tx, ref.URI, graph.KindBead)
			if err != nil {
				return fmt.Errorf("%w: subjects require canonical Bead identities in this Scope", storage.ErrValidation)
			}
			if path == ownerPath {
				return fmt.Errorf("%w: Evaluation and assessed subject identities must be distinct", storage.ErrValidation)
			}
			if err := validateVersionToken(ref.Revision); err != nil {
				return err
			}
			if err := s.checkRetainedSubjectReference(ctx, tx, path, ref.Revision); err != nil {
				return err
			}
		}
	}
	return nil
}

// Reference admission establishes retained Bead identity/version availability;
// it does not fetch or revalidate the target's domain payload. Native History
// remains under the native provider, and removed native versions stay refused.
func (s *Store) checkRetainedSubjectReference(ctx context.Context, tx *sql.Tx, path, revision string) error {
	var kind, backing, state string
	var typ, key sql.NullString
	err := tx.QueryRowContext(ctx, `SELECT resource_kind,backing,allocation_state,
 CASE WHEN OCTET_LENGTH(type_url)<=? THEN type_url ELSE NULL END,backing_key
 FROM graph_preview_catalog WHERE path=?`, len(s.ScopeURL())+512, path).Scan(&kind, &backing, &state, &typ, &key)
	if errors.Is(err, sql.ErrNoRows) {
		return ErrNotFound
	}
	if err != nil {
		return err
	}
	if kind != "bead" || !typ.Valid || (state != "live" && state != "deleted") {
		return fmt.Errorf("%w: subject must identify a retained Bead", storage.ErrValidation)
	}
	var exists bool
	switch {
	case backing == "generic" && s.isMemoryType(typ.String) && !key.Valid:
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM graph_preview_versions WHERE path=? AND version=?
 AND NOT(CASE WHEN JSON_VALID(CAST(snapshot AS CHAR)) THEN COALESCE(JSON_UNQUOTE(JSON_EXTRACT(CAST(snapshot AS CHAR),'$.state'))='deleted',FALSE) ELSE FALSE END))`, path, revision).Scan(&exists); err != nil {
			return err
		}
	case backing == "issue" && isConcreteNativeIssue(s.ScopeURL(), typ.String) && key.Valid:
		if err := tx.QueryRowContext(ctx, `SELECT EXISTS(SELECT 1 FROM graph_preview_issue_versions m
 JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision
 WHERE m.path=? AND m.version=? AND m.issue_id=? AND v.removed_at IS NULL)`, path, revision, key.String).Scan(&exists); err != nil {
			return err
		}
	default:
		return fmt.Errorf("%w: unsupported retained Bead domain", storage.ErrValidation)
	}
	if !exists {
		return ErrVersionUnknown
	}
	return nil
}

func (s *Store) informationalOwnedPathsInTx(ctx context.Context, tx *sql.Tx, sourcePath string) ([]string, error) {
	c, err := s.selectedTypes()
	if err != nil {
		return nil, err
	}
	var sourceType string
	if err := tx.QueryRowContext(ctx, `SELECT type_url FROM graph_preview_catalog WHERE path=? AND resource_kind='bead' AND allocation_state='live'`, sourcePath).Scan(&sourceType); err != nil {
		return nil, err
	}
	rows, err := tx.QueryContext(ctx, `SELECT l.path,c.type_url FROM graph_preview_links l
 JOIN graph_preview_catalog c ON c.path=l.path AND c.resource_kind='link' AND c.allocation_state='live'
 WHERE l.source_path=? ORDER BY l.path`, sourcePath)
	if err != nil {
		return nil, err
	}
	var paths []string
	for rows.Next() {
		var path, typ string
		if err := rows.Scan(&path, &typ); err != nil {
			_ = rows.Close()
			return nil, err
		}
		owned, err := c.types.Owns(typecatalog.Reference{URI: nativeFamilyReference(s.ScopeURL(), sourceType)}, typecatalog.Reference{URI: typ})
		if err != nil {
			_ = rows.Close()
			return nil, fmt.Errorf("%w: invalid persisted informational ownership Type: %v", ErrInvalidStore, err)
		}
		if owned {
			paths = append(paths, path)
		}
	}
	if err := errors.Join(rows.Err(), rows.Close()); err != nil {
		return nil, err
	}
	return paths, nil
}

// MarshalJSON restores all authored fields; it does not convert, discard or
// default extension values. Canonicalization also checks UTF-8 in direct Go
// construction before encoding/json could replace malformed text.
func (p Properties) MarshalJSON() ([]byte, error) {
	if !utf8.ValidString(p.Title) || !utf8.ValidString(p.Body) {
		return nil, fmt.Errorf("%w: Memory title and body must be UTF-8", graph.ErrValidation)
	}
	fields := map[string]json.RawMessage{}
	if p.extra != "" {
		if err := json.Unmarshal([]byte(p.extra), &fields); err != nil {
			return nil, err
		}
	}
	title, err := json.Marshal(p.Title)
	if err != nil {
		return nil, err
	}
	body, err := json.Marshal(p.Body)
	if err != nil {
		return nil, err
	}
	fields["title"], fields["body"] = title, body
	return canonicalJSON(fields)
}

func mergeMemoryProperties(before Properties, merge json.RawMessage) (Properties, error) {
	raw, err := canonicalJSON(before)
	if err != nil {
		return Properties{}, err
	}
	var fields, delta map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return Properties{}, err
	}
	if err := json.Unmarshal(merge, &delta); err != nil {
		return Properties{}, err
	}
	for name, value := range delta {
		fields[name] = value
	}
	raw, err = canonicalJSON(fields)
	if err != nil {
		return Properties{}, err
	}
	var result Properties
	if err := json.Unmarshal(raw, &result); err != nil {
		return Properties{}, err
	}
	return result, nil
}
