package main

import (
	"context"
	"database/sql"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/storage/dbproxy/proxy"
	"github.com/stretchr/testify/require"
)

// This boundary catches a lost ownership option between OpenOpts, the detached
// executable, Cobra and NewDoltServer. A package-only log test cannot catch it.
func TestProxyChildBoundsOwnedBackendOutput(t *testing.T) {
	dolt, err := exec.LookPath("dolt")
	if err != nil {
		t.Skip("requires dolt sql-server")
	}
	bd := buildBDForInitTests(t)
	oldExecutable := proxy.ResolveExecutable
	proxy.ResolveExecutable = func() (string, error) { return bd, nil }
	t.Cleanup(func() { proxy.ResolveExecutable = oldExecutable })
	t.Setenv("BEADS_DOLT_LOG_MAX_BYTES", "1024")
	t.Setenv("DOLT_DISABLE_EVENT_FLUSH", "1")
	t.Setenv("BD_DEBUG", "")
	t.Setenv("HOME", t.TempDir())
	for _, owned := range []bool{true, false} {
		t.Run(fmt.Sprintf("owned=%t", owned), func(t *testing.T) {
			root := t.TempDir()
			path := filepath.Join(root, "server.log")
			if !owned {
				path = filepath.Join(t.TempDir(), "operator.log")
			}
			config := filepath.Join(root, "config.yaml")
			body := fmt.Sprintf("log_level: debug\nlistener:\n  host: 127.0.0.1\n  port: %d\n", freeLoopbackPort(t))
			require.NoError(t, os.WriteFile(config, []byte(body), 0o600))
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			ep, err := proxy.GetCreateDatabaseProxyServerEndpointContext(ctx, root, proxy.OpenOpts{
				Backend: proxy.BackendLocalServer, ConfigFilePath: config,
				LogFilePath: path, DoltBinPath: dolt, RotateLogFile: owned,
			})
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, proxy.Shutdown(root)) })
			for range 200 {
				conn, err := net.DialTimeout("tcp", ep.Address(), time.Second)
				require.NoError(t, err)
				require.NoError(t, conn.Close())
			}
			require.Eventually(t, func() bool {
				if _, err := os.Stat(path + ".1"); err == nil {
					return true
				}
				info, err := os.Stat(path)
				return err == nil && info.Size() > 1024
			}, 5*time.Second, 10*time.Millisecond)
			info, err := os.Stat(path)
			require.NoError(t, err)
			if owned {
				require.LessOrEqual(t, info.Size(), int64(1024))
				tail, err := os.Stat(path + ".1")
				require.NoError(t, err)
				require.LessOrEqual(t, tail.Size(), int64(1024))
			} else {
				require.Greater(t, info.Size(), int64(1024), "an operator log must retain its original append behavior")
				require.NoFileExists(t, path+".1")
			}
			db, err := sql.Open("mysql", "root@tcp("+ep.Address()+")/")
			require.NoError(t, err)
			require.NoError(t, db.PingContext(ctx))
			if owned {
				// An unwritable archive must not break the backend's output
				// pipes. Its one warning belongs in independent proxy.log.
				require.NoError(t, os.Remove(path+".1"))
				require.NoError(t, os.Mkdir(path+".1", 0o700))
				for range 200 {
					conn, err := net.DialTimeout("tcp", ep.Address(), time.Second)
					require.NoError(t, err)
					require.NoError(t, conn.Close())
				}
				proxyLog := filepath.Join(root, "proxy.log")
				require.Eventually(t, func() bool {
					data, _ := os.ReadFile(proxyLog)
					return strings.Contains(string(data), "backend log write failed")
				}, 5*time.Second, 10*time.Millisecond)
				require.NoError(t, db.PingContext(ctx), "failed log rotation must not kill SQL service")
				data, err := os.ReadFile(proxyLog)
				require.NoError(t, err)
				require.Equal(t, 1, strings.Count(string(data), "backend log write failed"))
				info, err := os.Stat(path)
				require.NoError(t, err)
				require.LessOrEqual(t, info.Size(), int64(1024))
			}
			require.NoError(t, db.Close())
		})
	}
}
