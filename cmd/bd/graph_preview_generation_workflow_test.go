//go:build cgo

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/steveyegge/beads/internal/configfile"
	"github.com/steveyegge/beads/internal/storage/embeddeddolt"
	"github.com/steveyegge/beads/internal/storage/graphstore"
)

// The two refusals that existed before generations were admitted by one
// predicate. A marker that is merely unsupported keeps these texts; only a
// well-formed newer generation earns a message of its own.
const (
	graphGenerationFirstGateRefusal  = "graph_mode marker and metadata disagree; automatic recovery is not supported"
	graphGenerationSecondGateRefusal = "graph_mode link metadata is missing, incomplete, or unsupported; no database was opened"
)

// graphGenerationRun runs bd in a disposable environment and returns its stdout,
// stderr and exit status. The environment is the whitelist graphPolicyCLI uses,
// so an operator's workspace, routing, credentials and telemetry never enter it;
// unlike graphPolicyCLI it hands back the message and status of a refusal.
func graphGenerationRun(t *testing.T, bd, work, home string, args ...string) (stdout, stderr string, exit int) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, bd, args...)
	cmd.Dir = work
	cmd.Env = []string{
		"PATH=" + os.Getenv("PATH"), "HOME=" + home,
		"TMPDIR=" + os.TempDir(), "TMP=" + os.TempDir(), "TEMP=" + os.TempDir(),
		"XDG_CONFIG_HOME=" + filepath.Join(home, ".config"),
		"XDG_CACHE_HOME=" + filepath.Join(home, ".cache"),
		"GIT_CONFIG_GLOBAL=" + filepath.Join(home, "missing-gitconfig"), "GIT_CONFIG_NOSYSTEM=1",
		"BD_DISABLE_METRICS=1", "BD_DISABLE_EVENT_FLUSH=1", "BD_NON_INTERACTIVE=1",
		"BEADS_DOLT_AUTO_START=0", "DOLT_METRICS_DISABLED=1", "NO_COLOR=1",
	}
	if developerDir := os.Getenv("DEVELOPER_DIR"); developerDir != "" {
		cmd.Env = append(cmd.Env, "DEVELOPER_DIR="+developerDir)
	}
	var out, errOut bytes.Buffer
	cmd.Stdout, cmd.Stderr = &out, &errOut
	err := cmd.Run()
	if ctx.Err() != nil {
		t.Fatalf("CLI did not complete within deadline: %v\n%s", ctx.Err(), errOut.String())
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatalf("run %v: %v", args, err)
	}
	if exitErr != nil {
		exit = exitErr.ExitCode()
	}
	return out.String(), errOut.String(), exit
}

// graphGenerationRefusal runs a command that admission must refuse and returns
// the refusal message, after checking it is the typed, non-retryable
// graph_not_initialized refusal with exit status 5 and no output on stdout.
func graphGenerationRefusal(t *testing.T, bd, work, home string, args ...string) string {
	t.Helper()
	stdout, stderr, exit := graphGenerationRun(t, bd, work, home, append(args, "--json")...)
	if exit != 5 || stdout != "" {
		t.Fatalf("%v: want an exit 5 refusal with empty stdout, got exit %d stdout=%q stderr=%q", args, exit, stdout, stderr)
	}
	var diagnostic struct {
		Code      string `json:"code"`
		Message   string `json:"message"`
		Retryable bool   `json:"retryable"`
	}
	if err := json.Unmarshal([]byte(stderr), &diagnostic); err != nil {
		t.Fatalf("%v: expected a typed refusal: %v\n%s", args, err, stderr)
	}
	if diagnostic.Code != "graph_not_initialized" || diagnostic.Retryable {
		t.Fatalf("%v: want a non-retryable graph_not_initialized refusal, got %+v", args, diagnostic)
	}
	return diagnostic.Message
}

// graphGenerationExec runs one statement against the workspace's embedded
// database and closes it again, for the one edit the CLI cannot make. The
// database is closed before returning because no bd process can open it while
// this process holds it.
func graphGenerationExec(t *testing.T, cfg *configfile.Config, statement string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	db, closeDB, err := embeddeddolt.OpenSQL(ctx, filepath.Join(cfg.GraphWorkspace, "embeddeddolt"), cfg.DoltDatabase, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := closeDB(); err != nil {
			t.Error(err)
		}
	}()
	if _, err := db.ExecContext(ctx, statement); err != nil {
		t.Fatal(err)
	}
}

// graphGenerationQueryInt reads one integer from the workspace's embedded
// database, closing it again before returning.
func graphGenerationQueryInt(t *testing.T, cfg *configfile.Config, query string) int {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 90*time.Second)
	defer cancel()
	db, closeDB, err := embeddeddolt.OpenSQL(ctx, filepath.Join(cfg.GraphWorkspace, "embeddeddolt"), cfg.DoltDatabase, "main")
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		if err := closeDB(); err != nil {
			t.Error(err)
		}
	}()
	var value int
	if err := db.QueryRowContext(ctx, query).Scan(&value); err != nil {
		t.Fatal(err)
	}
	return value
}

// This workflow follows one embedded workspace through the generations of its
// format marker: what a fresh init writes, which generations admission accepts and
// reads, which it refuses and with what message, and that a newer generation is
// refused before the store is touched. The marker is engine independent (one
// writer, one admission function), so there is no server variant.
func TestGraphPreviewMarkerGenerationWorkflow(t *testing.T) {
	bd := buildBDUnderTest(t)
	work, home := t.TempDir(), t.TempDir()
	graphPolicyCLI(t, bd, work, home, nil, "", "init", "--graph-mode", "link", "--scope-url", "https://example.invalid/generation/", "--skip-hooks", "--skip-agents", "--non-interactive")
	dir := filepath.Join(work, ".beads")
	marker := filepath.Join(dir, graphPreviewMarker)
	raw, err := os.ReadFile(marker)
	if err != nil || string(raw) != graphPreviewGeneration {
		t.Fatalf("fresh marker: %q %v", raw, err)
	}
	cfg, err := configfile.LoadForDiscovery(dir)
	if err != nil || cfg.GraphSchemaVersion != graphstore.SchemaVersion {
		t.Fatalf("fresh schema: %v", err)
	}
	out := graphPolicyCLI(t, bd, work, home, nil, "", "types", "--json")
	var installed struct {
		Result struct{ BeadTypes, LinkTypes []json.RawMessage }
	}
	if err = json.Unmarshal([]byte(out), &installed); err != nil {
		t.Fatal(err)
	}
	if len(installed.Result.BeadTypes) != 23 || len(installed.Result.LinkTypes) != 32 {
		t.Fatalf("generation13 Type inventory: %s", out)
	}
	refuse := func(value, want string, args ...string) {
		t.Helper()
		writeFile(t, marker, []byte(value))
		before := legacyUpgradeTreeDigest(t, dir)
		if got := graphGenerationRefusal(t, bd, work, home, args...); got != want {
			t.Fatalf("%q: got %q want %q", value, got, want)
		}
		if legacyUpgradeTreeDigest(t, dir) != before {
			t.Fatal("marker refusal changed workspace")
		}
	}
	for _, value := range []string{"link-preview-v5\n", "link-preview-v6\n", "link-preview-v7\n", "link-preview-v12\n", "link-preview-v13", "link-preview-v013\n", "link-preview-v13\n\n", ""} {
		refuse(value, graphGenerationFirstGateRefusal, "types")
	}
	refuse("link-preview-v7\n", graphGenerationFirstGateRefusal, "remember", "refused", "--id", "beads/refused")
	future := "graph_mode workspace format link-preview-v14 is newer than this bd supports (link-preview-v13); upgrade bd; no database was opened"
	refuse("link-preview-v14\n", future, "types")
	if err = os.Remove(marker); err != nil {
		t.Fatal(err)
	}
	before := legacyUpgradeTreeDigest(t, dir)
	if got := graphGenerationRefusal(t, bd, work, home, "types"); got != graphGenerationSecondGateRefusal || legacyUpgradeTreeDigest(t, dir) != before {
		t.Fatalf("missing marker: %q", got)
	}
	data := filepath.Join(dir, "embeddeddolt")
	away := data + ".away"
	if err = os.Rename(data, away); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := os.Rename(away, data); err != nil {
			t.Error(err)
		}
	})
	writeFile(t, marker, []byte(graphPreviewGeneration))
	stdout, stderr, exit := graphGenerationRun(t, bd, work, home, "types", "--json")
	if exit == 0 || stdout != "" || strings.Contains(stderr, graphGenerationFirstGateRefusal) || strings.Contains(stderr, graphGenerationSecondGateRefusal) {
		t.Fatalf("current marker did not reach store check: %d %q %q", exit, stdout, stderr)
	}
	refuse("link-preview-v14\n", future, "types")
}
