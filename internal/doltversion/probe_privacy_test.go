package doltversion

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"testing"
)

func TestProbeIsolatesDoltPrivacySettings(t *testing.T) {
	caller := t.TempDir()
	cfgdir := filepath.Join(caller, ".dolt")
	if err := os.Mkdir(cfgdir, 0700); err != nil {
		t.Fatal(err)
	}
	cfg := filepath.Join(cfgdir, "config_global.json")
	preimage := []byte(`{"metrics.disabled":"false","versioncheck.disabled":"false"}`)
	if err := os.WriteFile(cfg, preimage, 0600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("DOLT_ROOT_PATH", caller)
	t.Setenv("DOLT_DISABLE_EVENT_FLUSH", "0")
	observed := filepath.Join(caller, "observed-root")
	posix := `#!/bin/sh
set -eu
[ "$(pwd -P)" = "$(cd "$DOLT_ROOT_PATH" && pwd -P)" ]
[ "$DOLT_DISABLE_EVENT_FLUSH" = 1 ]
grep -q '"metrics.disabled":"true"' .dolt/config_global.json
grep -q '"versioncheck.disabled":"true"' .dolt/config_global.json
printf '%s' "$DOLT_ROOT_PATH" > ` + strconv.Quote(observed) + `
echo 'dolt version 2.2.0'
`
	batch := "@echo off\r\n" +
		"if /I not \"%CD%\"==\"%DOLT_ROOT_PATH%\" exit /b 21\r\n" +
		"if not \"%DOLT_DISABLE_EVENT_FLUSH%\"==\"1\" exit /b 22\r\n" +
		"findstr /C:metrics.disabled .dolt\\config_global.json | findstr /C:true > nul || exit /b 23\r\n" +
		"findstr /C:versioncheck.disabled .dolt\\config_global.json | findstr /C:true > nul || exit /b 24\r\n" +
		"<nul set /p =%DOLT_ROOT_PATH% > \"" + observed + "\"\r\n" +
		"echo dolt version 2.2.0\r\n"
	stub := writeExecStub(t, t.TempDir(), "dolt", posix, batch)
	id, err := Probe(context.Background(), stub)
	if err != nil || id.Version.String() != "2.2.0" {
		t.Fatalf("probe: %#v %v", id, err)
	}
	root, err := os.ReadFile(observed)
	if err != nil || string(root) == caller {
		t.Fatalf("root was not isolated: %q %v", root, err)
	}
	if _, err := os.Stat(string(root)); !os.IsNotExist(err) {
		t.Fatalf("private root retained after probe: %v", err)
	}
	b, err := os.ReadFile(cfg)
	if err != nil || string(b) != string(preimage) {
		t.Fatalf("caller configuration changed: %q %v", b, err)
	}
}
