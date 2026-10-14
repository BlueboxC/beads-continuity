package uow

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/dbproxy/proxy"
	"github.com/steveyegge/beads/internal/storage/schema"
)

// NewReadOnlyProxyUOWProvider attaches to an existing database through an
// authenticated live proxy. It performs no bootstrap, migration or auto-start.
func NewReadOnlyProxyUOWProvider(ctx context.Context, root, database, user, password string, external *configfile.ExternalDoltConfig, expectedProjectID string) (UnitOfWorkProvider, error) {
	opts := proxy.OpenOpts{Backend: proxy.BackendLocalServer}
	tlsName := ""
	if external != nil {
		opts.Backend = proxy.BackendExternal
		opts.External = *external
		var err error
		tlsName, err = registerExternalTLSConfig(*external)
		if err != nil {
			return nil, err
		}
	}
	lookup := func(ctx context.Context) (proxy.Endpoint, error) {
		return proxy.GetExistingDatabaseProxyServerEndpointContext(ctx, root, opts)
	}
	ep, err := lookup(ctx)
	if err != nil {
		return nil, err
	}
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		current, err := lookup(ctx)
		if err != nil {
			return nil, err
		}
		return (&net.Dialer{}).DialContext(ctx, "tcp", current.Address())
	}
	pool, err := openDBWithPolicy(ctx, buildDSN(ep, database, user, password, tlsName), true, dial)
	if err != nil {
		return nil, err
	}
	p := &doltSQLProvider{db: pool, defaultBranch: defaultBranch, expectedProjectID: expectedProjectID, preview: true, readOnly: true, proxiedServerMode: true, strictReadOnly: true}
	uw, err := p.NewUOW(ctx)
	if err != nil {
		return nil, errors.Join(err, p.Close(ctx))
	}
	defer closeAttempt(ctx, uw)
	base := uw.(*baseUOW)
	conn := base.tx.(*doltServerTx).conn
	if err = assertSessionDatabase(ctx, conn, database); err == nil {
		var version, ignored int
		version, err = schema.CurrentVersion(ctx, conn)
		if err == nil {
			ignored, err = schema.CurrentIgnoredVersion(ctx, conn)
		}
		if err == nil && (version != schema.LatestVersion() || ignored != schema.LatestIgnoredVersion()) {
			err = fmt.Errorf("strict readonly requires current schema (%d/%d, need %d/%d); run an authorized writable migration first", version, ignored, schema.LatestVersion(), schema.LatestIgnoredVersion())
		}
	}
	if err == nil {
		err = checkTeamServerIdentity(ctx, conn, database, expectedProjectID)
	}
	if err != nil {
		return nil, errors.Join(err, p.Close(ctx))
	}
	return p, nil
}
