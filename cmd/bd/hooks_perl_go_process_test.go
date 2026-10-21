package main

import (
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestHookPerlGoChild(t *testing.T) {
	if os.Getenv("HOOK_GO_CHILD") != "1" {
		return
	}
	if os.Getenv("HOOK_GO_IGNORE_TERM") == "1" {
		signal.Ignore(syscall.SIGTERM)
	}
	if err := os.WriteFile(os.Getenv("HOOK_GO_PID"), []byte(strconv.Itoa(os.Getpid())), 0o600); err != nil {
		t.Fatal(err)
	}
	fmt.Println("go-hook-started")
	for {
		time.Sleep(time.Hour)
	}
}

func TestGeneratedHookPerlSupervisesGo(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX signal supervision")
	}
	perl := findHookProcessPerl(t)
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	for _, ignoreTerm := range []string{"0", "1"} {
		t.Run("ignore-term="+ignoreTerm, func(t *testing.T) {
			pidFile := filepath.Join(t.TempDir(), "pid")
			t.Setenv("HOOK_GO_CHILD", "1")
			t.Setenv("HOOK_GO_PID", pidFile)
			t.Setenv("HOOK_GO_IGNORE_TERM", ignoreTerm)
			t.Cleanup(func() {
				data, _ := os.ReadFile(pidFile)
				pid, _ := strconv.Atoi(string(data))
				if pid > 0 {
					if p, err := os.FindProcess(pid); err == nil {
						_ = p.Kill()
					}
				}
			})
			timeout := "1"
			result := runGeneratedHookProcess(t, hookProcessCase{
				fixtures: []hookProcessFixture{{name: "timeout", body: hookProcessIncompatibleTimeoutStub}, {name: "gtimeout", body: hookProcessIncompatibleGtimeoutStub}},
				bdBody:   "#!/bin/sh\nexec " + hookProcessShellQuote(binary) + " -test.run='^TestHookPerlGoChild$'\n",
				timeout:  &timeout, pathTail: perl,
			})
			if result.exitCode != 0 || !strings.Contains(result.output, "go-hook-started") || !strings.Contains(result.output, "timed out after 1s") || result.elapsed > 6*time.Second {
				t.Fatalf("Go child was not bounded: %+v", result)
			}
			data, err := os.ReadFile(pidFile)
			if err != nil {
				t.Fatal(err)
			}
			pid, err := strconv.Atoi(string(data))
			if err != nil {
				t.Fatal(err)
			}
			p, err := os.FindProcess(pid)
			if err == nil && p.Signal(syscall.Signal(0)) == nil {
				t.Fatalf("child %d remains alive or unreaped", pid)
			}
		})
	}
}
