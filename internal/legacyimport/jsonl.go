// Package legacyimport reads ordinary bd export JSONL, never graph records.
package legacyimport

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"reflect"
	"strconv"
	"strings"

	graph "github.com/steveyegge/beads/graphops"
	"unicode/utf8"

	"github.com/steveyegge/beads/internal/types"
)

// MaxBytes bounds an entire atomic import, rather than each individual line.
const MaxBytes = 16 * 1024 * 1024

// ErrLimitExceeded distinguishes bounded admission from malformed legacy data.
var ErrLimitExceeded = errors.New("legacy input limit exceeded")

// Memory is the key/value vocabulary used by ordinary export --include-memories.
type Memory struct {
	Key   string `json:"key"`
	Value string `json:"value"`
}

// Batch carries current data only. Ordinary export contains no retained history.
type Batch struct {
	Issues   []*types.Issue
	Memories []Memory
}

// Parse refuses unknown fields so newer or graph vocabulary cannot be lost.
func Parse(r io.Reader) (Batch, error) {
	data, err := io.ReadAll(io.LimitReader(r, MaxBytes+1))
	if err != nil {
		return Batch{}, err
	}
	if len(data) > MaxBytes {
		return Batch{}, fmt.Errorf("%w: legacy import exceeds %d bytes", ErrLimitExceeded, MaxBytes)
	}
	if !utf8.Valid(data) {
		return Batch{}, fmt.Errorf("legacy import must be UTF-8")
	}
	var result Batch
	scanner := bufio.NewScanner(bytes.NewReader(data))
	scanner.Buffer(make([]byte, 4096), MaxBytes+1)
	for line := 1; scanner.Scan(); line++ {
		raw := bytes.TrimSpace(scanner.Bytes())
		if len(raw) == 0 {
			continue
		}
		if err := parseLine(raw, &result); err != nil {
			return Batch{}, fmt.Errorf("legacy import line %d: %w", line, err)
		}
	}
	return result, scanner.Err()
}

func parseLine(raw []byte, result *Batch) error {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err := uniqueKeys(decoder, raw, 0); err != nil {
		return err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return err
	}
	if fields == nil {
		return fmt.Errorf("expected a legacy JSON object")
	}
	if _, ok := fields["_schema"]; ok {
		for key, value := range fields {
			if key != "_schema" && key != "_dolt_branch" && key != "_dolt_commit" && key != "_project_id" && key != "_sort" {
				return fmt.Errorf("schema header must not contain data or unknown provenance")
			}
			var text string
			if err := json.Unmarshal(value, &text); err != nil || bytes.Equal(value, []byte("null")) {
				return fmt.Errorf("schema header %s must be a string", key)
			}
			if key == "_schema" && text != "beads-jsonl/1" {
				return fmt.Errorf("unsupported legacy schema %q", text)
			}
		}
		return nil // Historical ordinary exports may carry a standalone schema header.
	}
	var kind string
	if value, ok := fields["_type"]; ok {
		if bytes.Equal(value, []byte("null")) {
			return fmt.Errorf("_type must be a string")
		}
		if err := json.Unmarshal(value, &kind); err != nil {
			return err
		}
		delete(fields, "_type")
	}
	if kind == "memory" {
		if _, ok := fields["value"]; !ok {
			return fmt.Errorf("memory value must be present")
		}
		body, err := json.Marshal(fields)
		if err != nil {
			return err
		}
		var memory Memory
		if err := strictDecode(body, &memory); err != nil {
			return err
		}
		if memory.Key == "" {
			return fmt.Errorf("memory key must be nonempty")
		}
		result.Memories = append(result.Memories, memory)
		return nil
	}
	if kind != "" && kind != "issue" {
		return fmt.Errorf("unsupported legacy record type %q; graph data import is unavailable", kind)
	}
	for _, name := range []string{"dependency_count", "dependent_count", "comment_count"} {
		if value, ok := fields[name]; ok {
			var count int
			if err := json.Unmarshal(value, &count); err != nil || count < 0 || bytes.Equal(value, []byte("null")) {
				return fmt.Errorf("%s must be a nonnegative integer", name)
			}
			delete(fields, name) // Projections are recomputed from imported relationships.
		}
	}
	for _, name := range []string{"wisp", "wisp_plane"} {
		if value, ok := fields[name]; ok {
			var marker bool
			if bytes.Equal(value, []byte("null")) {
				return fmt.Errorf("%s must be a boolean", name)
			}
			if err := json.Unmarshal(value, &marker); err != nil {
				return err
			}
			if marker {
				return fmt.Errorf("%s records are unavailable in graph import", name)
			}
			delete(fields, name)
		}
	}
	// Comment has a compatibility UnmarshalJSON for old numeric IDs. Check its
	// vocabulary explicitly, since Decoder.DisallowUnknownFields cannot see inside it.
	if rawComments, ok := fields["comments"]; ok {
		var comments []map[string]json.RawMessage
		if err := json.Unmarshal(rawComments, &comments); err != nil {
			return err
		}
		for _, comment := range comments {
			for name := range comment {
				if name != "id" && name != "issue_id" && name != "author" && name != "text" && name != "created_at" {
					return fmt.Errorf("unknown comment field %q", name)
				}
			}
			if id, ok := comment["id"]; ok {
				var text string
				if err := json.Unmarshal(id, &text); err != nil {
					if _, err := strconv.ParseInt(string(id), 10, 64); err != nil {
						return fmt.Errorf("comment id must be a string or legacy int64")
					}
				} else if bytes.Equal(id, []byte("null")) {
					return fmt.Errorf("comment id must not be null")
				}
			}
		}
	}
	body, err := json.Marshal(fields)
	if err != nil {
		return err
	}
	var issue types.Issue
	if err := strictDecode(body, &issue); err != nil {
		return err
	}
	result.Issues = append(result.Issues, &issue)
	return nil
}

// Map decoding normally chooses the last duplicate key. Reject that ambiguity
// before the vocabulary and metadata decoders can discard source information.
func uniqueKeys(decoder *json.Decoder, raw []byte, depth int) error {
	if depth > 128 {
		return fmt.Errorf("legacy JSON nesting exceeds 128 levels")
	}
	token, err := checkedToken(decoder, raw)
	if err != nil {
		return err
	}
	delim, ok := token.(json.Delim)
	if !ok {
		return nil
	}
	if delim == '{' {
		seen := map[string]bool{}
		for decoder.More() {
			key, err := checkedToken(decoder, raw)
			if err != nil {
				return err
			}
			name, ok := key.(string)
			if !ok || seen[name] {
				return fmt.Errorf("duplicate or invalid JSON key %q", name)
			}
			seen[name] = true
			if err := uniqueKeys(decoder, raw, depth+1); err != nil {
				return err
			}
		}
	} else if delim == '[' {
		for decoder.More() {
			if err := uniqueKeys(decoder, raw, depth+1); err != nil {
				return err
			}
		}
	} else {
		return fmt.Errorf("unexpected JSON delimiter")
	}
	_, err = decoder.Token()
	return err
}

func strictDecode(raw []byte, value any) error {
	if err := exactFields(raw, reflect.TypeOf(value)); err != nil {
		return err
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	return decoder.Decode(value)
}

// encoding/json replaces lone surrogate escapes. Validate each original string
// token through the graph JSON door before decoding can change its contents.
// Legacy numeric comment IDs stay exact int64 spellings, outside Properties.
func checkedToken(decoder *json.Decoder, raw []byte) (json.Token, error) {
	start := decoder.InputOffset()
	token, err := decoder.Token()
	if err != nil {
		return nil, err
	}
	if _, ok := token.(string); ok {
		text := bytes.TrimLeft(raw[start:decoder.InputOffset()], " \t\r\n,:")
		if _, err := graph.CanonicalizeJSON(text); err != nil {
			return nil, err
		}
	}
	return token, nil
}

// DisallowUnknownFields still accepts case-insensitive aliases. Admit exact
// exported names before it or a compatibility unmarshaller can overwrite data.
func exactFields(raw []byte, typ reflect.Type) error {
	raw = bytes.TrimSpace(raw)
	if bytes.Equal(raw, []byte("null")) {
		switch typ.Kind() {
		case reflect.Pointer, reflect.Slice, reflect.Map, reflect.Interface:
			return nil
		default:
			return fmt.Errorf("null is unsupported for legacy %s", typ)
		}
	}
	for typ.Kind() == reflect.Pointer {
		typ = typ.Elem()
	}
	if typ == reflect.TypeOf(json.RawMessage{}) {
		return nil
	}
	raw = bytes.TrimSpace(raw)
	if len(raw) == 0 {
		return nil
	}
	if typ.Kind() == reflect.Struct && raw[0] == '{' {
		fields := map[string]reflect.Type{}
		for i := 0; i < typ.NumField(); i++ {
			field := typ.Field(i)
			if field.PkgPath != "" {
				continue
			}
			tag := strings.Split(field.Tag.Get("json"), ",")[0]
			if tag != "" && tag != "-" {
				fields[tag] = field.Type
			}
		}
		if typ == reflect.TypeOf(types.BondRef{}) {
			fields["proto_id"] = reflect.TypeOf("")
		}
		var values map[string]json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		if typ == reflect.TypeOf(types.BondRef{}) && values["source_id"] != nil && values["proto_id"] != nil {
			var current, legacy string
			if err := json.Unmarshal(values["source_id"], &current); err != nil {
				return err
			}
			if err := json.Unmarshal(values["proto_id"], &legacy); err != nil {
				return err
			}
			if current != legacy {
				return fmt.Errorf("conflicting BondRef source_id and proto_id")
			}
		}
		for key, value := range values {
			child, ok := fields[key]
			if !ok {
				return fmt.Errorf("unknown legacy field %q", key)
			}
			if err := exactFields(value, child); err != nil {
				return err
			}
		}
	} else if (typ.Kind() == reflect.Slice || typ.Kind() == reflect.Array) && raw[0] == '[' {
		var values []json.RawMessage
		if err := json.Unmarshal(raw, &values); err != nil {
			return err
		}
		for _, value := range values {
			if err := exactFields(value, typ.Elem()); err != nil {
				return err
			}
		}
	}
	return nil
}
