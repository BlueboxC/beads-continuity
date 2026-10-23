package main

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage"
	"github.com/stretchr/testify/require"
)

func legacyBackendFixture(t *testing.T, backend string) string {
	t.Helper()
	d := t.TempDir()
	data, err := json.Marshal(map[string]any{"backend": backend, "operator_extension": map[string]any{"keep": true}})
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(d, configfile.LegacyConfigFileName), data, 0o600))
	return d
}

func TestFactoriesRefuseLegacyBackendWithoutMigration(t *testing.T) {
	factories := map[string]func(context.Context, string) (storage.DoltStorage, error){
		"write": newDoltStoreFromConfig,
		"read": func(c context.Context, d string) (storage.DoltStorage, error) {
			return newReadOnlyStoreFromConfig(c, d)
		},
		"preview": newPreviewStoreFromConfig,
	}
	for _, backend := range []string{"postgres", "mysql", "sqlite", "unknown-backend"} {
		for name, open := range factories {
			t.Run(backend+"/"+name, func(t *testing.T) {
				d := legacyBackendFixture(t, backend)
				before := snapshotMigrationTree(t, d)
				s, err := open(t.Context(), d)
				require.Error(t, err)
				require.Nil(t, s)
				require.Contains(t, err.Error(), backend)
				require.Equal(t, before, snapshotMigrationTree(t, d))
			})
		}
	}
}

func TestDoltHelperRefusesRegisteredLegacyBackendWithoutMigration(t *testing.T) {
	const backend = "legacy-registered"
	registerContractBackend(t, backend)
	d := legacyBackendFixture(t, backend)
	before := snapshotMigrationTree(t, d)
	_, err := loadDoltBackendConfig(d)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), backend), err)
	require.Equal(t, before, snapshotMigrationTree(t, d))
}

func TestLegacyRegisteredFactoryDispatchRemainsAvailable(t *testing.T) {
	const backend = "legacy-dispatch"
	registerContractBackend(t, backend)
	for _, readonly := range []bool{false, true} {
		d := legacyBackendFixture(t, backend)
		if readonly {
			_, err := newReadOnlyStoreFromConfig(t.Context(), d)
			require.ErrorIs(t, err, errRegistryReadOnly)
		} else {
			_, err := newDoltStoreFromConfig(t.Context(), d)
			require.ErrorIs(t, err, errRegistryReadWrite)
		}
	}
}
