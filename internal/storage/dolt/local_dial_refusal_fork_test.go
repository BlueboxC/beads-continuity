// Adapted from gastownhall/beads PR #7864 at f74536c5576b.
package dolt

import (
	"context"
	"errors"
	"github.com/steveyegge/beads/internal/doltserver"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"
)

// dialError is the error a dial returns when connect fails with errno.
func dialError(errno syscall.Errno) error {
	return &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connect", errno)}
}

// GH#7214: a probe this process may not make at all (connect refused with
// EPERM or EACCES, as inside a sandbox that refuses localhost) says nothing
// about the server. It must not reset the cooldown, or a sandboxed caller
// would keep every process that can connect failing fast.
func TestCircuitBreaker_LocallyRefusedProbeKeepsCooldown(t *testing.T) {
	t.Setenv("BEADS_TEST_MODE", "")
	tests := []struct {
		name      string
		errno     syscall.Errno
		wantReset bool
	}{
		{"EPERM", syscall.EPERM, false},
		{"EACCES", syscall.EACCES, false},
		{"connection refused", syscall.ECONNREFUSED, true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			orig := dialProbe
			t.Cleanup(func() { dialProbe = orig })
			dialProbe = func(string, string, time.Duration) error { return dialError(tt.errno) }

			cb := newTestCircuitBreaker(t)
			for i := 0; i < circuitFailureThreshold; i++ {
				cb.RecordFailure()
			}
			cb.mu.Lock()
			state := cb.readState()
			state.TrippedAt = time.Now().Add(-circuitCooldown - time.Second)
			cb.writeState(state)
			trippedAt := cb.readState().TrippedAt
			cb.mu.Unlock()

			if cb.Allow() {
				t.Fatal("an open breaker whose probe failed must reject")
			}
			cb.mu.Lock()
			after := cb.readState()
			cb.mu.Unlock()
			if after.State != circuitOpen {
				t.Errorf("state = %q, want %q", after.State, circuitOpen)
			}
			if reset := !after.TrippedAt.Equal(trippedAt); reset != tt.wantReset {
				t.Errorf("cooldown reset = %v, want %v", reset, tt.wantReset)
			}
		})
	}
}

// GH#7214: a sandboxed bd may not connect to localhost at all. Its refused
// dials, before and after it adopts the tracked server, are not failures of
// the server the other bd processes reach, so they must not count toward
// tripping the breaker those processes read. That holds too where the open
// fails closed because auto-start answered on another port than a shared
// server's or an authoritative one.
func TestNewServerMode_LocalDialRefusalIsNotAServerFailure(t *testing.T) {
	const port, otherPort = 14567, 14568
	sharedServer := func(cfg *Config) { cfg.ServerPortSharedServer = true }
	authoritativePort := func(cfg *Config) { cfg.ServerPortSource = doltserver.PortSourceEnv }
	tests := []struct {
		name         string
		errno        syscall.Errno
		autoStart    bool
		startedPort  int           // the port auto-start answers on
		configure    func(*Config) // nil: no port source declares the port
		wantFailures int
		wantMsg      string // for an adopted server: nothing was auto-started
	}{
		{"EPERM, tracked server adopted", syscall.EPERM, true, port, nil, 0, "local policy"},
		{"EACCES, tracked server adopted", syscall.EACCES, true, port, nil, 0, "local policy"},
		{"connection refused, tracked server adopted", syscall.ECONNREFUSED, true, port, nil, 1, "bd dolt stop"},
		{"EPERM, auto-start off", syscall.EPERM, false, port, nil, 0, ""},
		{"connection refused, auto-start off", syscall.ECONNREFUSED, false, port, nil, 1, ""},
		{"EPERM, shared server's port", syscall.EPERM, true, otherPort, sharedServer, 0, ""},
		{"EACCES, shared server's port", syscall.EACCES, true, otherPort, sharedServer, 0, ""},
		{"connection refused, shared server's port", syscall.ECONNREFUSED, true, otherPort, sharedServer, 1, ""},
		{"EPERM, authoritative port", syscall.EPERM, true, otherPort, authoritativePort, 0, ""},
		{"EACCES, authoritative port", syscall.EACCES, true, otherPort, authoritativePort, 0, ""},
		{"connection refused, authoritative port", syscall.ECONNREFUSED, true, otherPort, authoritativePort, 1, ""},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Setenv("BEADS_TEST_MODE", "") // keep the real breaker
			breaker := newTestCircuitBreakerOnPort(t, port)
			origClean, origNew, origDial := cleanServerCircuitState, newServerCircuitBreaker, dialServer
			t.Cleanup(func() {
				cleanServerCircuitState, newServerCircuitBreaker, dialServer = origClean, origNew, origDial
			})
			cleanServerCircuitState = func() {}
			newServerCircuitBreaker = func(string, int, string) *circuitBreaker { return breaker }
			dialServer = func(string, string, time.Duration) (net.Conn, error) {
				return nil, dialError(tt.errno)
			}
			// Auto-start adopts a tracked server: the one the open dialed, or
			// one on another port.
			stubEnsureRunningDetailed(t, tt.startedPort, false, nil)

			beadsDir := t.TempDir()
			cfg := &Config{
				Database:   "dial_refusal",
				BeadsDir:   beadsDir,
				Path:       filepath.Join(beadsDir, "dolt"),
				ServerHost: "127.0.0.1",
				ServerPort: port,
				AutoStart:  tt.autoStart,
			}
			if tt.configure != nil {
				tt.configure(cfg)
			}
			_, err := newServerMode(context.Background(), cfg)
			if err == nil {
				t.Fatal("an open whose every dial fails must fail")
			}
			if tt.startedPort != port && !strings.Contains(err.Error(), "silently") {
				t.Errorf("open did not fail closed: %v", err)
			}
			breaker.mu.Lock()
			failures := breaker.readState().Failures
			breaker.mu.Unlock()
			if failures != tt.wantFailures {
				t.Errorf("breaker failures = %d, want %d", failures, tt.wantFailures)
			}
			if tt.wantMsg != "" {
				msg := err.Error()
				if !strings.Contains(msg, tt.wantMsg) || strings.Contains(msg, "auto-started") {
					t.Errorf("error for an adopted server = %q, want it to say %q and not \"auto-started\"", msg, tt.wantMsg)
				}
				// A dial the sandbox refused never reached the server, so its
				// log has nothing to explain.
				wantLog := tt.errno == syscall.ECONNREFUSED
				if gotLog := strings.Contains(msg, "Check logs"); gotLog != wantLog {
					t.Errorf("error for an adopted server = %q: names the server log = %v, want %v", msg, gotLog, wantLog)
				}
			}
		})
	}
}

// The error after auto-start's dial fails says "auto-started" only for a
// server this open started, and names the server log only for a dial the
// server refused: one local policy refused never reached it (GH#7214). It
// still wraps the dial error.
func TestAutoStartRedialError(t *testing.T) {
	const addr, logPath = "127.0.0.1:14567", "/repo/.beads/dolt-server.log"
	tests := []struct {
		name        string
		startedByUs bool
		errno       syscall.Errno
		want        []string
		notWant     []string
	}{
		{"started, connection refused", true, syscall.ECONNREFUSED,
			[]string{"auto-started but still unreachable", "Check logs: " + logPath},
			[]string{"local policy", "bd dolt stop"}},
		{"started, EPERM", true, syscall.EPERM,
			[]string{"auto-started but still unreachable", "local policy"},
			[]string{"Check logs", "bd dolt stop"}},
		{"adopted, connection refused", false, syscall.ECONNREFUSED,
			[]string{"Dolt server unreachable at", "Check logs: " + logPath, "bd dolt stop"},
			[]string{"auto-started", "local policy"}},
		{"adopted, EPERM", false, syscall.EPERM,
			[]string{"Dolt server unreachable at", "local policy"},
			[]string{"auto-started", "Check logs", "bd dolt stop"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := autoStartRedialError(addr, dialError(tt.errno), tt.startedByUs, logPath)
			msg := err.Error()
			for _, w := range tt.want {
				if !strings.Contains(msg, w) {
					t.Errorf("error %q does not say %q", msg, w)
				}
			}
			for _, w := range tt.notWant {
				if strings.Contains(msg, w) {
					t.Errorf("error %q says %q", msg, w)
				}
			}
			if !errors.Is(err, tt.errno) {
				t.Errorf("error %q does not wrap %v", msg, tt.errno)
			}
		})
	}
}
