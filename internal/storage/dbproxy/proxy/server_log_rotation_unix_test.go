//go:build unix

package proxy

import (
	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/steveyegge/beads/internal/storage/dbproxy/util"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestForkExecChild_RotatesOversizedLogs(t *testing.T) {
	t.Setenv(doltserver.EnvMaxLogBytes, "1024")
	const operatorGeneration = "operator generation\n"
	for _, tc := range []struct {
		name                        string
		customLog                   bool
		serverSize, proxySize       int
		serverRotated, proxyRotated bool
	}{
		{name: "both oversized", serverSize: 4096, proxySize: 4096, serverRotated: true},
		{name: "only server oversized", serverSize: 4096, proxySize: 1024, serverRotated: true},
		{name: "only proxy oversized", serverSize: 1024, proxySize: 4096},
		{name: "oversized custom server log", customLog: true, serverSize: 4096, proxySize: 4096},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			childPath := filepath.Join(root, "echo-child.sh")
			require.NoError(t, os.WriteFile(childPath, []byte("#!/bin/sh\necho child-stdout\nexit 0\n"), 0o700))
			previous := ResolveExecutable
			ResolveExecutable = func() (string, error) { return childPath, nil }
			t.Cleanup(func() { ResolveExecutable = previous })

			serverLog := filepath.Join(root, "server.log")
			if tc.customLog {
				serverLog = filepath.Join(t.TempDir(), "operator.log")
				require.NoError(t, os.WriteFile(serverLog+".1", []byte(operatorGeneration), 0o600))
			}
			proxyLog := filepath.Join(root, LogFileName)
			oldServer := strings.Repeat("S", tc.serverSize-len("old-server\n")) + "old-server\n"
			oldProxy := strings.Repeat("P", tc.proxySize-len("old-proxy\n")) + "old-proxy\n"
			require.NoError(t, os.WriteFile(serverLog, []byte(oldServer), 0o600))
			require.NoError(t, os.WriteFile(proxyLog, []byte(oldProxy), 0o600))

			lock, err := util.TryLock(filepath.Join(root, LockFileName))
			require.NoError(t, err)
			opts := externalOpenOpts(root)
			opts.LogFilePath = serverLog
			opts.RotateLogFile = !tc.customLog
			child, err := forkExecChild(root, opts, opts.Port, "", lock)
			require.NoError(t, err)
			select {
			case err := <-child.done:
				require.NoError(t, err)
			case <-time.After(5 * time.Second):
				t.Fatal("stub proxy child did not exit")
			}

			serverBody, err := os.ReadFile(serverLog)
			require.NoError(t, err)
			if tc.serverRotated {
				rotated, err := os.ReadFile(serverLog + ".1")
				require.NoError(t, err)
				assert.Equal(t, oldServer, string(rotated))
				assert.Equal(t, "child-stdout\n", string(serverBody), "child stdout must go to a fresh server.log")
			} else {
				assert.Equal(t, oldServer+"child-stdout\n", string(serverBody))
				if tc.customLog {
					generation, err := os.ReadFile(serverLog + ".1")
					require.NoError(t, err)
					assert.Equal(t, operatorGeneration, string(generation), "the operator's own .1 must survive")
				} else {
					assert.NoFileExists(t, serverLog+".1")
				}
			}

			if tc.proxyRotated {
				rotated, err := os.ReadFile(proxyLog + ".1")
				require.NoError(t, err)
				assert.Equal(t, oldProxy, string(rotated))
				assert.NoFileExists(t, proxyLog)
			} else {
				assert.NoFileExists(t, proxyLog+".1")
				body, err := os.ReadFile(proxyLog)
				require.NoError(t, err)
				assert.Equal(t, oldProxy, string(body))
			}
		})
	}
}
