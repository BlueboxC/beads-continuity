//go:build cgo

package main

import (
	"database/sql"
	"fmt"
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/testutil"
	"github.com/stretchr/testify/require"
)

func proxiedIdentityFixture(t *testing.T) (string, int, string, *sql.DB) {
	t.Helper()
	portText := os.Getenv("BEADS_GRAPH_TEST_SERVER_PORT")
	if portText == "" {
		requireProxiedServerEnv(t)
		portText = testutil.StartIsolatedDoltContainer(t)
	}
	port, err := strconv.Atoi(portText)
	require.NoError(t, err)
	require.Greater(t, port, 0)
	require.LessOrEqual(t, port, 65535)
	bd := buildEmbeddedBD(t)
	database := fmt.Sprintf("bd_identity_%d", time.Now().UnixNano())
	db, err := sql.Open("mysql", fmt.Sprintf("root:@tcp(127.0.0.1:%d)/?parseTime=true", port))
	require.NoError(t, err)
	t.Cleanup(func() {
		_, _ = db.Exec("DROP DATABASE IF EXISTS `" + database + "`")
		_ = db.Close()
	})
	bdProxiedInit(t, bd, "identity", "--database", database,
		"--proxied-server-external-host", "127.0.0.1", "--proxied-server-external-port", portText)
	return bd, port, database, db
}

func TestProxiedInitAdoptsSharedProjectIdentity(t *testing.T) {
	bd, port, database, db := proxiedIdentityFixture(t)
	var projectID string
	require.NoError(t, db.QueryRow("SELECT value FROM `"+database+"`.metadata WHERE `key` = '_project_id'").Scan(&projectID))
	require.NotEmpty(t, projectID)
	for _, team := range []bool{false, true} {
		t.Run(fmt.Sprintf("team-server=%v", team), func(t *testing.T) {
			args := []string{"--database", database, "--proxied-server-external-host", "127.0.0.1", "--proxied-server-external-port", strconv.Itoa(port)}
			if team {
				args = append(args, "--team-server")
			}
			p := bdProxiedInit(t, bd, "identity", args...)
			cfg, err := configfile.Load(p.beadsDir)
			require.NoError(t, err)
			require.Equal(t, projectID, cfg.ProjectID)
		})
	}
}

func TestProxiedInitPreservesSharedTracking(t *testing.T) {
	bd, port, database, db := proxiedIdentityFixture(t)
	var count int
	require.NoError(t, db.QueryRow("SELECT COUNT(*) FROM `"+database+"`.metadata WHERE `key` IN ('repo_id', 'clone_id', 'last_import_time')").Scan(&count))
	require.Zero(t, count, "fresh init must not seed per-clone state in a shared database")
	for _, key := range []string{"repo_id", "clone_id", "last_import_time"} {
		_, err := db.Exec("REPLACE INTO `"+database+"`.metadata (`key`, value) VALUES (?, ?)", key, "historical-"+key)
		require.NoError(t, err)
	}
	p := bdProxiedInit(t, bd, "identity", "--database", database,
		"--proxied-server-external-host", "127.0.0.1", "--proxied-server-external-port", strconv.Itoa(port))
	for _, key := range []string{"repo_id", "clone_id", "last_import_time"} {
		var value string
		require.NoError(t, db.QueryRow("SELECT value FROM `"+database+"`.metadata WHERE `key` = ?", key).Scan(&value))
		require.Equal(t, "historical-"+key, value, key)
	}
	version, err := os.ReadFile(filepath.Join(p.beadsDir, ".local_version"))
	require.NoError(t, err)
	require.Contains(t, string(version), Version)
}
