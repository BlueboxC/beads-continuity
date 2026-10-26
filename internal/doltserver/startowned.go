package doltserver

import (
	"context"
	"errors"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"sync"
	"time"
)

var errManagedPortInUse = errors.New("managed port belongs to another process")

// startedServer retains the child handle until Wait reaps it. Only this
// launched child and its detached group may be killed on startup failure.
// Successful launches also retain this handle for a later owned stop.
type startedServer struct {
	cmd      *exec.Cmd
	exited   chan struct{}
	beadsDir string
	port     int
}

var launchedServers sync.Map

func rememberStartedServer(dir string, port int, s *startedServer) {
	s.beadsDir, s.port = filepath.Clean(dir), port
	pid := s.cmd.Process.Pid
	launchedServers.Store(pid, s)
	go func() {
		<-s.exited
		launchedServers.CompareAndDelete(pid, s)
	}()
}

func locallyStartedServer(dir string, pid, port int) *startedServer {
	value, ok := launchedServers.Load(pid)
	if !ok {
		return nil
	}
	s := value.(*startedServer)
	if s.beadsDir != filepath.Clean(dir) || s.port != port || s.hasExited() {
		return nil
	}
	return s
}

// Use the retained child handle, so a reaped PID cannot select a replacement.
func (s *startedServer) stop(timeout time.Duration) error {
	if s.hasExited() {
		return nil
	}
	if err := terminateStartedProcess(s.cmd.Process); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-s.exited:
		return nil
	case <-time.After(timeout):
	}
	if err := s.cmd.Process.Kill(); err != nil && !errors.Is(err, os.ErrProcessDone) {
		return err
	}
	select {
	case <-s.exited:
		return nil
	case <-time.After(time.Second):
		return fmt.Errorf("timeout reaping launched Dolt server PID %d", s.cmd.Process.Pid)
	}
}

func launchServer(cmd *exec.Cmd) (*startedServer, error) {
	if err := cmd.Start(); err != nil {
		return nil, err
	}
	srv := &startedServer{cmd: cmd, exited: make(chan struct{})}
	go func() { _ = cmd.Wait(); close(srv.exited) }()
	return srv, nil
}

func (s *startedServer) hasExited() bool {
	select {
	case <-s.exited:
		return true
	default:
		return false
	}
}

func (s *startedServer) killAndWait() {
	if !s.hasExited() {
		killStartedGroup(s.cmd.Process.Pid)
		_ = s.cmd.Process.Kill()
	}
	select {
	case <-s.exited:
	case <-time.After(5 * time.Second):
	}
}

func waitForManagedReady(s *startedServer, host string, port int, timeout time.Duration) error {
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	addr := net.JoinHostPort(host, strconv.Itoa(port))
	unverified := false
	for ctx.Err() == nil {
		if s.hasExited() {
			return fmt.Errorf("dolt sql-server exited before readiness on port %d", port)
		}
		deadline, _ := ctx.Deadline()
		probeTimeout := min(500*time.Millisecond, time.Until(deadline))
		greeted, err := ProbeSQLServer("tcp", addr, probeTimeout) //nolint:gosec // G704: internal managed host/port
		if err == nil && greeted && ctx.Err() == nil {
			owned, known := listenerOwnership(ctx, s.cmd.Process.Pid, port)
			if s.hasExited() {
				return fmt.Errorf("dolt sql-server exited before readiness on port %d", port)
			}
			if ctx.Err() == nil {
				if owned || (!known && !requireListenerOwnership) {
					return nil
				}
				if known {
					return fmt.Errorf("%w on port %d", errManagedPortInUse, port)
				}
			}
			unverified = true
		}
		select {
		case <-ctx.Done():
		case <-s.exited:
		case <-time.After(100 * time.Millisecond):
		}
	}
	if unverified {
		return fmt.Errorf("timeout after %s: listener ownership at %s could not be verified", timeout, addr)
	}
	return fmt.Errorf("timeout after %s waiting for managed server at %s", timeout, addr)
}
