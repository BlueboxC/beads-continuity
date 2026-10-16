//go:build cgo

package embeddeddolt

import (
	"database/sql"
	"fmt"
	"testing"

	"github.com/stretchr/testify/require"
)

// The next revision query needs one row even before an issue has any versions.
// Guards the engine regression fixed by dolthub/go-mysql-server#4021.
func TestEmbeddedPrimaryPrefixAggregateCardinality(t *testing.T) {
	s, err := newStore(t.Context(), t.TempDir(), "fixture", "main", openStrict)
	require.NoError(t, err)
	t.Cleanup(func() { _ = s.Close() })
	require.NoError(t, s.withConn(t.Context(), true, func(tx *sql.Tx) error {
		_, err := tx.ExecContext(t.Context(), "CREATE TABLE revision_probe (issue_id VARCHAR(255) NOT NULL, revision BIGINT NOT NULL, PRIMARY KEY (issue_id, revision))")
		return err
	}))
	for _, seeded := range []bool{false, true} {
		if seeded {
			require.NoError(t, s.withConn(t.Context(), true, func(tx *sql.Tx) error {
				_, err := tx.ExecContext(t.Context(), "INSERT INTO revision_probe VALUES ('present', 4), ('present', 7)")
				return err
			}))
		}
		for _, aggregate := range []string{"MIN", "MAX"} {
			t.Run(fmt.Sprintf("seeded=%t/%s", seeded, aggregate), func(t *testing.T) {
				require.NoError(t, s.withConn(t.Context(), false, func(tx *sql.Tx) error {
					var next int64
					err := tx.QueryRowContext(t.Context(), "SELECT COALESCE("+aggregate+"(revision), 0) + 1 FROM revision_probe WHERE issue_id = ?", "missing").Scan(&next)
					if err != nil {
						return err
					}
					require.EqualValues(t, 1, next)
					return nil
				}))
			})
		}
	}
}
