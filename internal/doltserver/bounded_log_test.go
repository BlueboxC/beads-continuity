package doltserver

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestBoundedLogTrimsHistoricalArchive(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	old := strings.Repeat("old", 100) + "recent diagnostic"
	require.NoError(t, os.WriteFile(path+".1", []byte(old), 0o600))
	w, err := OpenBoundedLog(path, 32, nil)
	require.NoError(t, err)
	require.NoError(t, w.Close())
	tail, err := os.ReadFile(path + ".1")
	require.NoError(t, err)
	require.Equal(t, old[len(old)-32:], string(tail), "startup rotation must not retain an unbounded old archive")
}

func TestBoundedLogWarningDoesNotAdmitUnsafePath(t *testing.T) {
	for _, symlink := range []bool{false, true} {
		name := "directory"
		if symlink {
			name = "symlink"
		}
		t.Run(name, func(t *testing.T) {
			root := t.TempDir()
			path, foreign := filepath.Join(root, "server.log"), filepath.Join(root, "foreign")
			require.NoError(t, os.WriteFile(foreign, []byte("keep foreign diagnostics"), 0o600))
			if symlink {
				if err := os.Symlink(foreign, path); err != nil {
					t.Skipf("symlink creation is unavailable: %v", err)
				}
			} else {
				require.NoError(t, os.Mkdir(path, 0o700))
			}
			warnings := 0
			writer, err := OpenBoundedLog(path, 32, func(error) { warnings++ })
			require.Error(t, err)
			require.Nil(t, writer)
			require.Zero(t, warnings, "admission failures cannot be downgraded to retention warnings")
			got, err := os.ReadFile(foreign)
			require.NoError(t, err)
			require.Equal(t, "keep foreign diagnostics", string(got))
		})
	}
}

func TestBoundedLogAccountsForBootstrapOutput(t *testing.T) {
	path := filepath.Join(t.TempDir(), "server.log")
	w, err := OpenBoundedLog(path, 32, nil)
	require.NoError(t, err)
	defer func() { require.NoError(t, w.Close()) }()
	bootstrap, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	require.NoError(t, err)
	defer func() { require.NoError(t, bootstrap.Close()) }()
	_, err = bootstrap.WriteString(strings.Repeat("b", 24))
	require.NoError(t, err)
	_, err = w.Write([]byte(strings.Repeat("s", 24)))
	require.NoError(t, err)
	info, err := os.Stat(path)
	require.NoError(t, err)
	require.LessOrEqual(t, info.Size(), int64(32), "bootstrap output on an inherited descriptor must count toward rotation")
	_, err = bootstrap.WriteString("next")
	require.NoError(t, err)
	got, err := os.ReadFile(path)
	require.NoError(t, err)
	require.Equal(t, strings.Repeat("s", 24)+"next", string(got), "truncation must preserve append on the inherited descriptor")
}
