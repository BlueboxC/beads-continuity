package server

import (
	"bytes"
	"context"
	"io"
	"log"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/doltserver"
	"github.com/stretchr/testify/require"
)

func TestBackendLogDrainsAfterRotationFailure(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	file, err := doltserver.OpenBoundedLog(path, 32, nil)
	require.NoError(t, err)
	t.Cleanup(func() { require.NoError(t, file.Close()) })
	old := strings.Repeat("a", 32)
	_, err = file.Write([]byte(old))
	require.NoError(t, err)
	require.NoError(t, os.Mkdir(path+".1", 0o700))
	var warnings bytes.Buffer
	previous := log.Writer()
	log.SetOutput(&warnings)
	t.Cleanup(func() { log.SetOutput(previous) })
	w := &backendLog{WriteCloser: file, limit: 32}
	// A failed sink must not terminate os/exec's pipe readers or give the
	// backend EPIPE. Consume the complete stream while preserving the old log.
	n, err := io.Copy(w, strings.NewReader(strings.Repeat("new diagnostic\n", 10000)))
	require.NoError(t, err)
	require.Equal(t, int64(150000), n)
	_, err = w.Write([]byte("another failed record"))
	require.NoError(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, old, string(got))
	require.Equal(t, 1, strings.Count(warnings.String(), "server log write failed"))
}

func TestBackendLogKeepsNewestBytesOfLargePipeBuffer(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	file, err := doltserver.OpenBoundedLog(path, 32, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, file.Close()) }()
	w := &backendLog{WriteCloser: file, limit: 32}
	data := strings.Repeat("older", 1000) + "latest diagnostic\n"
	n, err := w.Write([]byte(data))
	require.NoError(t, err)
	require.Equal(t, len(data), n)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, data[len(data)-32:], string(got))
}

func TestDoltServerCustomLogsAndExplicitOptOutKeepAppendBehavior(t *testing.T) {
	for _, tc := range []struct {
		name, limit string
		owned       bool
	}{
		{"custom", "32", false}, {"disabled zero", "0", true}, {"disabled negative", "-1", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Setenv("BEADS_DOLT_LOG_MAX_BYTES", tc.limit)
			root := t.TempDir()
			config := filepath.Join(root, "config.yaml")
			require.NoError(t, os.WriteFile(config, []byte("log_level: debug\n"), 0o600))
			path := filepath.Join(t.TempDir(), "operator.log")
			var options []DoltServerOption
			if tc.owned {
				options = append(options, WithBoundedLog())
			}
			s, err := NewDoltServer("dolt", root, config, path, 0, "", options...)
			require.NoError(t, err)
			t.Cleanup(func() { require.NoError(t, s.Stop(context.Background())) })
			data := strings.Repeat("retain", 100)
			_, err = s.logFile.Write([]byte(data))
			require.NoError(t, err)
			got, err := os.ReadFile(path)
			require.NoError(t, err)
			require.Equal(t, data, string(got))
			require.NoFileExists(t, path+".1")
		})
	}
}
