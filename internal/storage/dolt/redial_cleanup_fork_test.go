package dolt

import (
	"context"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/doltserver"
)

func TestForkRedialFailureCleansOnlyNewChild(t *testing.T) {
	for _, startedByUs := range []bool{false, true} {
		t.Run(fmt.Sprint(startedByUs), func(t *testing.T) {
			t.Setenv("BEADS_TEST_MODE", "")
			t.Setenv("BEADS_DOLT_SHARED_SERVER", "")
			t.Setenv("GT_ROOT", "")
			originalDial, originalStop := dialServer, stopRejectedAutoStartedServer
			t.Cleanup(func() { dialServer, stopRejectedAutoStartedServer = originalDial, originalStop })
			dialServer = func(string, string, time.Duration) (net.Conn, error) { return nil, dialError(syscall.EPERM) }
			dir := t.TempDir()
			oldPort := []byte(" 14567 \n")
			if err := os.WriteFile(filepath.Join(dir, doltserver.PortFileName), oldPort, 0600); err != nil {
				t.Fatal(err)
			}
			stubEnsureRunningDetailedWithPortFileWrite(t, 14567, startedByUs)
			stops := 0
			stopRejectedAutoStartedServer = func(path string) error {
				if path != dir {
					t.Fatalf("stopped another workspace: %s", path)
				}
				stops++
				return nil
			}
			_, err := newServerMode(context.Background(), &Config{Database: "dial_refusal", BeadsDir: dir, Path: filepath.Join(dir, "dolt"), ServerHost: "127.0.0.1", ServerPort: 14567, AutoStart: true})
			if err == nil {
				t.Fatal("locally denied dial succeeded")
			}
			wantStops := 0
			if startedByUs {
				wantStops = 1
			}
			if stops != wantStops {
				t.Fatalf("stops=%d want=%d", stops, wantStops)
			}
			data, err := os.ReadFile(filepath.Join(dir, doltserver.PortFileName))
			if err != nil {
				t.Fatal(err)
			}
			if startedByUs && string(data) != string(oldPort) {
				t.Fatalf("old port bytes not restored: %q", data)
			}
			if !startedByUs && doltserver.ReadPortFile(dir) != 14567 {
				t.Fatalf("adopted server state removed: %q", data)
			}
		})
	}
}

func TestForkLocalProbePreservesSharedFile(t *testing.T) {
	t.Setenv("BEADS_TEST_MODE", "")
	original := dialProbe
	t.Cleanup(func() { dialProbe = original })
	for _, state := range []string{circuitOpen, circuitHalfOpen} {
		for _, errno := range []syscall.Errno{syscall.EPERM, syscall.EACCES} {
			t.Run(fmt.Sprint(state, errno), func(t *testing.T) {
				dialProbe = func(string, string, time.Duration) error { return dialError(errno) }
				cb := newTestCircuitBreaker(t)
				cb.writeState(circuitState{State: state, Failures: 5, TrippedAt: time.Now().Add(-2 * circuitCooldown)})
				before, err := os.ReadFile(cb.filePath)
				if err != nil {
					t.Fatal(err)
				}
				if cb.Allow() {
					t.Fatal("denied probe was accepted")
				}
				after, err := os.ReadFile(cb.filePath)
				if err != nil || string(after) != string(before) {
					t.Fatalf("shared state changed: %q %v", after, err)
				}
			})
		}
	}
}

func TestForkLocalPermissionsDoNotRecordRetryFailure(t *testing.T) {
	for _, errno := range []syscall.Errno{syscall.EPERM, syscall.EACCES, syscall.ECONNREFUSED} {
		t.Run(fmt.Sprint(errno), func(t *testing.T) {
			t.Setenv("BEADS_TEST_MODE", "")
			cb := newTestCircuitBreaker(t)
			store := &DoltStore{breaker: cb}
			_ = store.recordRetryFailure(context.Background(), fmt.Errorf("lost connection: %w", dialError(errno)))
			want := 0
			if errno == syscall.ECONNREFUSED {
				want = 1
			}
			if got := cb.readState().Failures; got != want {
				t.Fatalf("recorded=%d want=%d", got, want)
			}
		})
	}
}
