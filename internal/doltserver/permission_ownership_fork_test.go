//go:build !windows

package doltserver

import (
	"os"
	"strconv"
	"syscall"
	"testing"
)

func TestForkManagedOwnershipRejectsPermissionOnlyLiveness(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(pidPath(dir), []byte(strconv.Itoa(os.Getpid())), 0600); err != nil {
		t.Fatal(err)
	}
	if err := writePortFile(dir, 14567); err != nil {
		t.Fatal(err)
	}
	original := signalZero
	t.Cleanup(func() { signalZero = original })
	signalZero = func(int) error { return os.NewSyscallError("kill", syscall.EPERM) }
	if !isProcessAlive(os.Getpid()) {
		t.Fatal("EPERM must retain liveness")
	}
	if ManagesLiveServerOnPort(dir, 14567) {
		t.Fatal("permission-only liveness cannot prove workspace ownership")
	}
	if readPortFile(dir) != 14567 {
		t.Fatal("ownership inspection changed state")
	}
}
