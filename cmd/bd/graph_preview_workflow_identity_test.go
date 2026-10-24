package main

import (
	"encoding/json"
	"slices"
	"strings"
	"testing"
)

// Fresh native Issues expose their native ID; private authoring paths are not
// public identities. Keep workflow assertions tied to the actual create result.
func graphCaptureWorkflowIdentity(t *testing.T, args []string, output string, identities map[string]string) {
	t.Helper()
	if len(args) == 0 || args[0] != "create" {
		return
	}
	var envelope struct{ Result struct{ ID string } }
	if json.Unmarshal([]byte(output), &envelope) != nil || envelope.Result.ID == "" {
		return
	}
	for i, arg := range args {
		path := ""
		if arg == "--id" && i+1 < len(args) {
			path = args[i+1]
		}
		if strings.HasPrefix(arg, "--id=") {
			path = strings.TrimPrefix(arg, "--id=")
		}
		if path != "" {
			if !strings.Contains(path, "/") {
				path = "beads/" + path
			}
			identities[path] = envelope.Result.ID
			return
		}
	}
}
func graphWorkflowIdentity(identities map[string]string, scope, path string) string {
	if id, ok := identities[path]; ok {
		return id
	}
	return scope + path
}

func graphSortedWorkflowIDs(ids []string) []string { slices.Sort(ids); return ids }

func graphNormalizeWorkflowPaths(paths []string, identities map[string]string, scope string) []string {
	for i, id := range paths {
		for private, public := range identities {
			if id == public || id == strings.TrimPrefix(public, scope) {
				paths[i] = private
				break
			}
		}
	}
	return paths
}
