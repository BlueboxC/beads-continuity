package graphstore

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"unicode/utf8"

	graph "github.com/steveyegge/beads/graphops"
)

// EndpointPin is write intent for an existing endpoint identity. Current
// resolves in the writer transaction; Revision is otherwise an exact token.
// A zero intent is explicitly floating. A nil update intent preserves its pin.
type EndpointPin struct {
	Revision string
	Current  bool
}

// EndpointReference keeps endpoint identity independent of an optional exact
// retained revision. Empty Revision is floating, never an exact empty token.
// It is a carrier, not an endpoint admission or Type-validation engine.
type EndpointReference struct {
	URI      string
	Revision string
}

func (r EndpointReference) MarshalJSON() ([]byte, error) {
	if !utf8.ValidString(r.URI) || !utf8.ValidString(r.Revision) {
		return nil, fmt.Errorf("%w: endpoint Reference must be UTF-8", graph.ErrValidation)
	}
	if r.Revision == "" {
		return json.Marshal(r.URI)
	}
	if err := validateVersionToken(r.Revision); err != nil {
		return nil, err
	}
	return json.Marshal(struct {
		URI      string `json:"uri"`
		Revision string `json:"revision"`
	}{r.URI, r.Revision})
}

func (r *EndpointReference) UnmarshalJSON(raw []byte) error {
	*r = EndpointReference{}
	raw = bytes.TrimSpace(raw)
	if !utf8.Valid(raw) {
		return fmt.Errorf("%w: endpoint Reference must be UTF-8", graph.ErrValidation)
	}
	if len(raw) == 0 {
		return fmt.Errorf("%w: empty endpoint Reference", graph.ErrValidation)
	}
	if raw[0] == '"' {
		return json.Unmarshal(raw, &r.URI)
	}
	// Decode the closed pinned arm explicitly. A missing/empty revision must
	// never collapse into a floating reference during retained-state lowering.
	decoder := json.NewDecoder(bytes.NewReader(raw))
	token, err := decoder.Token()
	if err != nil || token != json.Delim('{') {
		return fmt.Errorf("%w: endpoint Reference must be URI or pinned object", graph.ErrValidation)
	}
	fields := map[string]string{}
	for decoder.More() {
		key, err := decoder.Token()
		if err != nil {
			return err
		}
		name, ok := key.(string)
		if !ok || (name != "uri" && name != "revision") {
			return fmt.Errorf("%w: unknown endpoint Reference member", graph.ErrValidation)
		}
		if _, exists := fields[name]; exists {
			return fmt.Errorf("%w: duplicate endpoint Reference member", graph.ErrValidation)
		}
		var value json.RawMessage
		if err := decoder.Decode(&value); err != nil {
			return err
		}
		if len(value) == 0 || value[0] != '"' {
			return fmt.Errorf("%w: endpoint Reference members must be strings", graph.ErrValidation)
		}
		var fieldsValue string
		if err := json.Unmarshal(value, &fieldsValue); err != nil {
			return err
		}
		fields[name] = fieldsValue
	}
	if _, err := decoder.Token(); err != nil {
		return err
	}
	if _, err := decoder.Token(); err != io.EOF {
		return fmt.Errorf("%w: trailing endpoint Reference JSON", graph.ErrValidation)
	}
	uri, hasURI := fields["uri"]
	revision, hasRevision := fields["revision"]
	if !hasURI || !hasRevision {
		return fmt.Errorf("%w: pinned Reference requires uri and revision", graph.ErrValidation)
	}
	if err := validateVersionToken(revision); err != nil {
		return err
	}
	r.URI, r.Revision = uri, revision
	return nil
}

func (r LinkRecord) SourceReference() EndpointReference {
	return EndpointReference{URI: r.Source, Revision: r.SourceRevision}
}
func (r LinkRecord) TargetReference() EndpointReference {
	return EndpointReference{URI: r.Target, Revision: r.TargetRevision}
}

type linkRecordJSON struct {
	ID          string            `json:"id"`
	Type        string            `json:"type"`
	Revision    string            `json:"revision"`
	Version     string            `json:"version"`
	Source      EndpointReference `json:"source"`
	Target      EndpointReference `json:"target"`
	Properties  map[string]any    `json:"properties"`
	Metadata    json.RawMessage   `json:"metadata"`
	Attribution Attribution       `json:"attribution"`
}

func (r LinkRecord) MarshalJSON() ([]byte, error) {
	return json.Marshal(linkRecordJSON{r.ID, r.Type, r.Revision, r.Version, r.SourceReference(), r.TargetReference(), r.Properties, r.Metadata, r.Attribution})
}

func (r *LinkRecord) UnmarshalJSON(raw []byte) error {
	var wire linkRecordJSON
	if err := json.Unmarshal(raw, &wire); err != nil {
		return err
	}
	*r = LinkRecord{ID: wire.ID, Type: wire.Type, Revision: wire.Revision, Version: wire.Version, Source: wire.Source.URI, Target: wire.Target.URI, SourceRevision: wire.Source.Revision, TargetRevision: wire.Target.Revision, Properties: wire.Properties, Metadata: wire.Metadata, Attribution: wire.Attribution}
	return nil
}

// endpointPinInTx resolves only local Bead pins under the already checked
// authority transaction. Exact lookup never substitutes current state, and
// errors retain the existing missing/gone/unknown distinctions.
func (s *Store) endpointPinInTx(ctx context.Context, tx *sql.Tx, path string, pin EndpointPin, used *uint64) (string, error) {
	return s.endpointPinInTxBudget(ctx, tx, path, pin, used, false)
}

// Historical Link composition charges earlier retained roles and the current
// working set before decoding an additional explicit endpoint pin. Ordinary pin
// writes retain their existing acquisition policy.
func (s *Store) endpointPinInTxBudget(ctx context.Context, tx *sql.Tx, path string, pin EndpointPin, used *uint64, includeCurrent bool) (string, error) {
	if pin.Current && pin.Revision != "" {
		return "", fmt.Errorf("%w: choose current or exact endpoint revision", graph.ErrValidation)
	}
	if pin.Current {
		_, revision, err := s.beadEndpointInTx(ctx, tx, path)
		if err != nil {
			return "", err
		}
		pin.Revision = revision
	}
	if pin.Revision == "" {
		return "", nil
	}
	if err := validateVersionToken(pin.Revision); err != nil {
		return "", err
	}
	if err := reserveEndpointVersionBytes(ctx, tx, path, pin.Revision, used); err != nil {
		return "", err
	}
	if includeCurrent {
		if err := checkCurrentReadBytesAdditional(ctx, tx, *used); err != nil {
			return "", err
		}
	}
	value, err := s.readVersionInTx(ctx, tx, path, pin.Revision)
	if err != nil {
		return "", err
	}
	switch value.(type) {
	case Record, IssueRecord:
		return pin.Revision, nil
	default:
		return "", fmt.Errorf("%w: endpoint exact reference must select a retained Bead", graph.ErrValidation)
	}
}

// Reserve before decoding either endpoint's complete historical payload. Missing
// mappings remain the exact reader's error, not a fabricated removal diagnosis.
func reserveEndpointVersionBytes(ctx context.Context, tx *sql.Tx, path, revision string, used *uint64) error {
	var size uint64
	err := tx.QueryRowContext(ctx, `SELECT CAST(LEAST(COALESCE(SUM(n),0),?) AS UNSIGNED) FROM (
 SELECT 256+OCTET_LENGTH(snapshot)+OCTET_LENGTH(actor) AS n FROM graph_preview_versions WHERE path=? AND version=?
 UNION ALL
 SELECT 256+OCTET_LENGTH(m.owned)+OCTET_LENGTH(v.durable_state)+OCTET_LENGTH(v.change_actor)+OCTET_LENGTH(v.attribution_status) AS n
 FROM graph_preview_issue_versions m JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE m.path=? AND m.version=?
 ) endpoint_bytes`, PreviewCurrentReadByteLimit+1, path, revision, path, revision).Scan(&size)
	if err != nil {
		return err
	}
	if size > PreviewCurrentReadByteLimit-*used {
		return fmt.Errorf("%w: endpoint historical acquisition exceeds %d-byte budget", ErrLimitExceeded, PreviewCurrentReadByteLimit)
	}
	*used += size
	return nil
}

// Complete-state Type validation dereferences each exact endpoint once. Count
// actual per-Link acquisitions, including repeated references to one large
// historical revision, before any payload is decoded.
func pinnedEndpointSizeQueries() []currentReadSizeQuery {
	var queries []currentReadSizeQuery
	for _, endpoint := range []string{"source", "target"} {
		path, pin := "l."+endpoint+"_path", "l."+endpoint+"_revision"
		queries = append(queries, currentReadSizeQuery{
			"graph_preview_links l JOIN graph_preview_versions v ON v.path=" + path + " AND v.version=" + pin + " WHERE " + pin + "<>''",
			"v.snapshot,v.actor", 1}, currentReadSizeQuery{
			"graph_preview_links l JOIN graph_preview_issue_versions m ON m.path=" + path + " AND m.version=" + pin + " JOIN issue_versions v ON v.issue_id=m.issue_id AND v.revision=m.issue_revision WHERE " + pin + "<>''",
			"m.owned,v.durable_state,v.change_actor,v.attribution_status", 1})
	}
	return queries
}
