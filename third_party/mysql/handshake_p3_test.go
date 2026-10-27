package mysql

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"testing"
	"time"
)

func p3ReadGreeting(payload []byte) ([]byte, capabilityFlag, extendedCapabilityFlag, string, error) {
	conn, mc := newRWMockConn(0)
	conn.data = append([]byte{byte(len(payload)), byte(len(payload) >> 8), 0, 0}, payload...)
	conn.maxReads = 1
	return mc.readHandshakePacket()
}

func p3Greeting() []byte {
	p := append([]byte{10}, []byte("8.0.0\x00")...)
	p = append(p, 1, 0, 0, 0)
	p = append(p, []byte("12345678")...)
	p = append(p, 0, 1, 130)          // CLIENT_MYSQL | CLIENT_PROTOCOL_41 | CLIENT_SECURE_CONNECTION
	p = append(p, 33, 2, 0, 8, 0, 21) // charset, status, upper flags, auth length
	p = append(p, make([]byte, 10)...)
	p = append(p, []byte("abcdefghijkl\x00mysql_native_password\x00")...)
	return p
}

func TestP3MinimalGreeting(t *testing.T) {
	_, _, _, _, err := p3ReadGreeting(bytes.Repeat([]byte{10}, 8))
	if !errors.Is(err, ErrMalformPkt) {
		t.Fatalf("got %v, want ErrMalformPkt", err)
	}
}

func TestP3TruncatedGreetings(t *testing.T) {
	p := p3Greeting()
	// The parser supports a short greeting ending after lower capabilities,
	// or a full greeting through the second salt's terminating byte.
	shortEnd := 1 + len("8.0.0\x00") + 4 + 8 + 1 + 2
	fullEnd := shortEnd + 16 + 13
	for n := 1; n < fullEnd; n++ {
		if n == shortEnd {
			continue
		}
		t.Run(fmt.Sprint(n), func(t *testing.T) {
			_, _, _, _, err := p3ReadGreeting(p[:n])
			if !errors.Is(err, ErrMalformPkt) {
				t.Fatalf("got %v, want ErrMalformPkt", err)
			}
		})
	}
	for _, p := range [][]byte{{10}, {10, '8', '.', '0'}, append([]byte{10}, bytes.Repeat([]byte{'x'}, 100)...)} {
		_, _, _, _, err := p3ReadGreeting(p)
		if !errors.Is(err, ErrMalformPkt) {
			t.Fatalf("unterminated version: %v", err)
		}
	}
}

func TestP3ValidGreetings(t *testing.T) {
	p := p3Greeting()
	shortEnd := 22
	for _, tc := range []struct {
		name         string
		packet       []byte
		auth, plugin string
		caps         capabilityFlag
		ext          extendedCapabilityFlag
	}{
		{"short", p[:shortEnd], "12345678", "", clientMySQL | clientProtocol41 | clientSecureConn, 0},
		{"full", p, "12345678abcdefghijkl", "mysql_native_password", clientMySQL | clientProtocol41 | clientSecureConn | clientPluginAuth, 0},
		{"plugin-without-nul", p[:len(p)-1], "12345678abcdefghijkl", "mysql_native_password", clientMySQL | clientProtocol41 | clientSecureConn | clientPluginAuth, 0},
		{"no-plugin", p[:shortEnd+29], "12345678abcdefghijkl", "", clientMySQL | clientProtocol41 | clientSecureConn | clientPluginAuth, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			a, c, e, plugin, err := p3ReadGreeting(tc.packet)
			if err != nil || string(a) != tc.auth || c != tc.caps || e != tc.ext || plugin != tc.plugin {
				t.Fatalf("auth=%q caps=%x ext=%x plugin=%q err=%v", a, c, e, plugin, err)
			}
		})
	}
	// MariaDB's extension occupies the last four reserved bytes.
	maria := bytes.Clone(p)
	maria[20] &^= 1
	binary.LittleEndian.PutUint32(maria[34:38], 1)
	a, c, e, plugin, err := p3ReadGreeting(maria)
	if err != nil || string(a) != "12345678abcdefghijkl" || c&clientMySQL != 0 || e != 1 || plugin != "mysql_native_password" {
		t.Fatalf("MariaDB: auth=%q caps=%x ext=%x plugin=%q err=%v", a, c, e, plugin, err)
	}
}

func TestP3PublicPingRejectsMinimalGreeting(t *testing.T) {
	cfg := NewConfig()
	cfg.DialFunc = func(ctx context.Context, _, _ string) (net.Conn, error) {
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			_ = server.SetDeadline(time.Now().Add(time.Second))
			_, _ = server.Write([]byte{8, 0, 0, 0, 10, 10, 10, 10, 10, 10, 10, 10})
		}()
		return client, nil
	}
	connector, err := NewConnector(cfg)
	if err != nil {
		t.Fatal(err)
	}
	db := sql.OpenDB(connector)
	defer db.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	if err := db.PingContext(ctx); !errors.Is(err, ErrMalformPkt) {
		t.Fatalf("PingContext got %v, want ErrMalformPkt", err)
	}
}
