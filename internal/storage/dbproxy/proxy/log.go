package proxy

import "github.com/steveyegge/beads/internal/doltserver"

const maxProxyLogBytes int64 = 4 * 1024 * 1024

type proxyLog = doltserver.BoundedLog

func openProxyLog(path string, limit int64) (*proxyLog, error) {
	return doltserver.OpenBoundedLog(path, limit, nil)
}
