package graphstore

import (
	"encoding/json"
	"fmt"
	"time"

	graph "github.com/steveyegge/beads/graphops"
	"github.com/steveyegge/beads/internal/memoryapi"
	"github.com/steveyegge/beads/internal/storage"
)

type preparedLegacyMemory struct {
	sourceKey  string
	request    CreateRequest
	record     Record
	properties []byte
}

func (s *Store) prepareLegacyImportMemories(mounted legacyImportMount, actor string) ([]preparedLegacyMemory, error) {
	if len(mounted.Memories) == 0 {
		return nil, nil
	}
	catalog, err := s.selectedTypes()
	if err != nil {
		return nil, err
	}
	result := make([]preparedLegacyMemory, 0, len(mounted.Memories))
	for _, memory := range mounted.Memories {
		publicID := mounted.memoryIDs[memory.Key]
		request, err := PrepareMemoryIdentity(CreateRequest{PublicID: publicID, Actor: actor})
		if err != nil {
			return nil, err
		}
		publicURL, err := graph.CanonicalPublicResourceURL(s.ScopeURL(), publicID)
		if err != nil {
			return nil, err
		}
		revision, err := freshToken()
		if err != nil {
			return nil, err
		}
		record := Record{PublicID: publicID, ID: publicURL, Type: MemoryTypeURL(s.ScopeURL()), Revision: revision, Version: revision,
			Properties: Properties{Title: memoryapi.TitleFromBody(memory.Value), Body: memory.Value}, Metadata: json.RawMessage(`{}`), Owned: []json.RawMessage{},
			Attribution: Attribution{Actor: actor, Status: "claimed", RecordedAt: time.Now().UTC().Format(time.RFC3339Nano)}}
		properties, err := canonicalJSON(record.Properties)
		if err != nil {
			return nil, err
		}
		if !catalog.memoryType(record.Type) {
			return nil, fmt.Errorf("%w: Memory %q requires an installed Memory-family Type", storage.ErrValidation, memory.Key)
		}
		if err := catalog.validateProperties(record.Type, properties, graph.KindBead); err != nil {
			return nil, fmt.Errorf("%w: Memory %q properties: %v", storage.ErrValidation, memory.Key, err)
		}
		result = append(result, preparedLegacyMemory{sourceKey: memory.Key, request: request, record: record, properties: properties})
	}
	return result, nil
}
