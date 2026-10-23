package doltutil

import (
	mysql "github.com/go-sql-driver/mysql"
	"testing"
)

func TestForkIPv6ServerDSN(t *testing.T) {
	for _, h := range []string{"::1", "[::1]"} {
		c, err := mysql.ParseDSN((ServerDSN{Host: h, Port: 43211, User: "root"}).String())
		if err != nil {
			t.Fatal(err)
		}
		if c.Addr != "[::1]:43211" {
			t.Errorf("host %q dials %q", h, c.Addr)
		}
	}
}
