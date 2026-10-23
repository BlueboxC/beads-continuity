package util

import (
	mysql "github.com/go-sql-driver/mysql"
	"strings"
	"testing"
)

func TestDoltServerDSN_TLS(t *testing.T) {
	t.Run("config name takes precedence", func(t *testing.T) {
		dsn := DoltServerDSN{Host: "127.0.0.1", Port: 3306, User: "root", TLSConfigName: "beads-external-abc", TLSRequired: true}.String()
		if !strings.Contains(dsn, "tls=beads-external-abc") {
			t.Fatalf("dsn %q missing tls=beads-external-abc", dsn)
		}
	})

	t.Run("required without name emits tls=true", func(t *testing.T) {
		dsn := DoltServerDSN{Host: "127.0.0.1", Port: 3306, User: "root", TLSRequired: true}.String()
		if !strings.Contains(dsn, "tls=true") {
			t.Fatalf("dsn %q missing tls=true", dsn)
		}
	})

	t.Run("default disables tls", func(t *testing.T) {
		dsn := DoltServerDSN{Host: "127.0.0.1", Port: 3306, User: "root"}.String()
		if !strings.Contains(dsn, "tls=false") {
			t.Fatalf("dsn %q missing tls=false", dsn)
		}
	})

	t.Run("socket uses unix network", func(t *testing.T) {
		dsn := DoltServerDSN{Socket: "/var/run/dolt.sock", User: "root"}.String()
		if !strings.Contains(dsn, "unix(/var/run/dolt.sock)") {
			t.Fatalf("dsn %q missing unix socket", dsn)
		}
	})
}

// TestDoltServerDSNIPv6Host pins the address the driver dials for an IPv6
// listener host, which a managed proxied server's listener.host may be:
// "[::1]:3306", not "::1:3306" or "[[::1]]:3306", whether the host is written
// bare or bracketed. An IPv4 address joins as it always has.
func TestDoltServerDSNIPv6Host(t *testing.T) {
	for _, tc := range []struct{ host, want string }{
		{"::1", "[::1]:3306"},
		{"[::1]", "[::1]:3306"},
		{"127.0.0.1", "127.0.0.1:3306"},
	} {
		dsn := DoltServerDSN{Host: tc.host, Port: 3306, User: "root"}.String()
		cfg, err := mysql.ParseDSN(dsn)
		if err != nil {
			t.Fatalf("ParseDSN(%q) failed: %v", dsn, err)
		}
		if cfg.Addr != tc.want {
			t.Errorf("host %q: DSN %q dials %q, want %q", tc.host, dsn, cfg.Addr, tc.want)
		}
	}
}
