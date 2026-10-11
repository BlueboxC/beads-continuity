package main

import "github.com/steveyegge/beads/internal/storage/graphstore"

func graphPreviewGenerationSchema(marker []byte, version int) bool {
	if string(marker) == graphPreviewGeneration {
		return version == graphstore.SchemaVersion
	}
	return graphPreviewGenerationSupported(marker) && version == graphstore.LegacySchemaVersion
}

func graphPreviewIssueTypeURL(scope string) string {
	version := graphstore.SchemaVersion
	if graphPreviewConfig != nil {
		version = graphPreviewConfig.GraphSchemaVersion
	}
	return graphstore.IssueTypeURLForSchema(scope, version)
}
