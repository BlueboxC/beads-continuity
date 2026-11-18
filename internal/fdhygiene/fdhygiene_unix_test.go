//go:build unix

package fdhygiene

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"golang.org/x/sys/unix"
)

// openLeakedFD opens a file the way a caller outside Go would leave one: no
// O_CLOEXEC, so it survives an exec. Returns the raw descriptor.
func openLeakedFD(t *testing.T) int {
	t.Helper()
	path := filepath.Join(t.TempDir(), "leak.lock")
	if err := os.WriteFile(path, []byte("x"), 0o600); err != nil {
		t.Fatalf("seeding %s: %v", path, err)
	}
	fd, err := unix.Open(path, unix.O_RDONLY, 0)
	if err != nil {
		t.Fatalf("open %s without O_CLOEXEC: %v", path, err)
	}
	t.Cleanup(func() { _ = unix.Close(fd) })

	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatalf("F_GETFD on fd %d: %v", fd, err)
	}
	if flags&unix.FD_CLOEXEC != 0 {
		t.Fatalf("fd %d unexpectedly opened close-on-exec; the fixture proves nothing", fd)
	}
	return fd
}

func TestMarkInheritedCloexec_MarksLeakedFD(t *testing.T) {
	fd := openLeakedFD(t)

	marked := MarkInheritedCloexec()

	var found bool
	for _, m := range marked {
		if m == fd {
			found = true
		}
	}
	if !found {
		t.Errorf("fd %d not reported as marked; got %v", fd, marked)
	}

	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil {
		t.Fatalf("F_GETFD on fd %d: %v", fd, err)
	}
	if flags&unix.FD_CLOEXEC == 0 {
		t.Errorf("fd %d still lacks FD_CLOEXEC after marking", fd)
	}
}

// TestMarkInheritedCloexec_LeavesStdioAlone guards the carve-out: os/exec
// rewires 0/1/2 per-child, so marking bd's own stdio would change unrelated
// spawns.
func TestMarkInheritedCloexec_LeavesStdioAlone(t *testing.T) {
	before := make([]int, 3)
	for fd := range before {
		flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			t.Skipf("stdio fd %d not open under this test runner: %v", fd, err)
		}
		before[fd] = flags
	}

	for _, fd := range MarkInheritedCloexec() {
		if fd <= 2 {
			t.Errorf("MarkInheritedCloexec reported stdio fd %d as marked", fd)
		}
	}

	for fd, want := range before {
		got, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
		if err != nil {
			t.Fatalf("F_GETFD on stdio fd %d: %v", fd, err)
		}
		if got != want {
			t.Errorf("stdio fd %d flags changed: %d -> %d", fd, want, got)
		}
	}
}

// TestMarkInheritedCloexec_Idempotent checks the second call is a no-op, which
// is what makes calling it on every spawn cheap and safe.
func TestMarkInheritedCloexec_Idempotent(t *testing.T) {
	openLeakedFD(t)

	if first := MarkInheritedCloexec(); len(first) == 0 {
		t.Fatal("first call marked nothing; fixture fd was not seen")
	}
	if second := MarkInheritedCloexec(); len(second) != 0 {
		t.Errorf("second call marked %v, want nothing left to mark", second)
	}
}

const (
	childFDProbeEnv    = "BEADS_TEST_FDHYGIENE_PROBE_FD"
	childFDProbePrefix = "fdhygiene fd identity: "
)

// TestMarkInheritedCloexec_ChildDoesNotInherit covers GH#4634 with a real exec.
// The child may reuse the fd number, but must lose the original file.
func TestMarkInheritedCloexec_ChildDoesNotInherit(t *testing.T) {
	if raw, ok := os.LookupEnv(childFDProbeEnv); ok {
		fd, err := strconv.Atoi(raw)
		if err != nil || fd <= 2 {
			t.Fatalf("invalid probe fd %q", raw)
		}
		fmt.Println(childFDProbePrefix + fdIdentity(t, fd))
		return
	}

	fd := openLeakedFD(t)
	original := fdIdentity(t, fd)
	if got := childFDIdentity(t, fd); got != original {
		t.Fatalf("positive control: fd %d identity in unsanitized child = %s, want %s", fd, got, original)
	}

	MarkInheritedCloexec()

	flags, err := unix.FcntlInt(uintptr(fd), unix.F_GETFD, 0)
	if err != nil || flags&unix.FD_CLOEXEC == 0 {
		t.Fatalf("fd %d flags after marking = %d, %v, want FD_CLOEXEC", fd, flags, err)
	}
	if got := childFDIdentity(t, fd); got == original {
		t.Errorf("fd %d still refers to the original file after exec (device:inode %s)", fd, got)
	}
}

func fdIdentity(t *testing.T, fd int) string {
	t.Helper()
	var st unix.Stat_t
	if err := unix.Fstat(fd, &st); err != nil {
		if err == unix.EBADF {
			return "closed"
		}
		t.Fatalf("Fstat on fd %d: %v", fd, err)
	}
	return fmt.Sprintf("%d:%d", st.Dev, st.Ino)
}

func childFDIdentity(t *testing.T, fd int) string {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatalf("locate test executable: %v", err)
	}
	cmd := exec.Command(exe, "-test.run=^TestMarkInheritedCloexec_ChildDoesNotInherit$")
	cmd.Env = append(os.Environ(), childFDProbeEnv+"="+strconv.Itoa(fd))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("probing child fd %d: %v\n%s", fd, err, out)
	}
	for _, line := range strings.Split(string(out), "\n") {
		if identity, ok := strings.CutPrefix(line, childFDProbePrefix); ok {
			return identity
		}
	}
	t.Fatalf("child did not report fd %d identity:\n%s", fd, out)
	return ""
}
