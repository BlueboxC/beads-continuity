package dolt

import (
	"context"
	"fmt"
	"os"
	"strings"
)

// serverEnvCredentialsNote completes the error of a push that failed on the
// SQL route when this bd holds the remote's credentials. In server mode CALL
// DOLT_PUSH runs in the Dolt server's process, which takes DOLT_REMOTE_PASSWORD
// (for the remote user bd passes) and the cloud-storage variables of the
// remote URL's scheme (cloudAuthSchemeMap) from its own environment: bd sets
// them only in its own process (withRemoteOperationEnv), which a server bd
// does not run never sees. The note names the ones bd holds, so a push that a
// server without them refuses says where they must be set. It is empty when
// bd holds none, and in embedded mode, where the push runs in this process.
func (s *DoltStore) serverEnvCredentialsNote(ctx context.Context, remote string, creds *remoteCredentials) string {
	if !s.serverMode {
		return ""
	}
	var held []string
	if creds != nil && creds.username != "" && creds.password != "" {
		held = append(held, "DOLT_REMOTE_PASSWORD")
	}
	if remotes, err := s.ListRemotes(ctx); err == nil {
		for _, r := range remotes {
			if r.Name != remote {
				continue
			}
			for _, prefix := range envPrefixesForRemoteURL(r.URL) {
				// DOLT_REMOTE_ is the credential pair itself, named above
				// when the push passes its user.
				if prefix != "DOLT_REMOTE_" && hasCloudEnvPrefix(prefix) {
					held = append(held, prefix+"*")
				}
			}
		}
	}
	if len(held) == 0 {
		return ""
	}
	return fmt.Sprintf("; CALL DOLT_PUSH runs in the Dolt server's process, which takes %s from its own environment, not from bd's", strings.Join(held, " and "))
}
func hasCloudEnvPrefix(prefix string) bool {
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, prefix) {
			return true
		}
	}
	return false
}
