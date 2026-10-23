package dolt

import (
	"github.com/steveyegge/beads/internal/configfile"
	"testing"
)

func TestForkIPv6DoesNotManageIPv4Server(t *testing.T) {
	for _, h := range []string{"::1", "[::1]"} {
		cfg := &Config{ServerHost: h, AutoStart: true, Path: "/unopened", BeadsDir: "/unopened", Database: "existing"}
		if serverOpenCanAutoStart(cfg) {
			t.Errorf("would auto-start IPv4 server for %s", h)
		}
		if resolveLocalActiveDatabaseDir(cfg) != "" {
			t.Error("inferred local storage")
		}
		if !isExternalServerHost(h) {
			t.Error("must use external advice")
		}
		if !configfile.IsIPv6LoopbackHost(h) {
			t.Fatal(h)
		}
	}
}
