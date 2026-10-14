package scripts_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"testing"
	"time"
)

const nixFixtureHash = "sha256-kNnps1bSj5o1mIDO3oJOjhrt1iJZvk75UT2kfqoVYJU="
const nixFixtureNewHash = "sha256-9X2X4euZdXnKOVnSq5HRPpCw5va2m+iuLYhG7SS7i9M="

// Exercise real shell status, filesystem restoration and workflow ordering.
// Nix/Docker are doubles; these tests neither build nor download packages.
func TestNixVendorHashScript(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("POSIX script fixture; native Windows Nix is not qualified")
	}
	mismatch := "error: hash mismatch in fixed-output derivation\n got:    " + nixFixtureNewHash + "\n"
	for _, tc := range []struct {
		name, output, verifyOutput, transport, sed string
		initialExit, verifyExit, builds            int
		ok, changed, failTee                       bool
	}{
		{name: "non hash failure", output: "error: requires a newer Go toolchain\n", initialExit: 37, builds: 1},
		{name: "hash mismatch without got", output: "error: hash mismatch\n", initialExit: 1, builds: 1},
		{name: "unparseable got", output: "error: hash mismatch\n got: NOTAHASH\n", initialExit: 1, builds: 1},
		{name: "verification failure", output: mismatch, initialExit: 1, verifyExit: 38, verifyOutput: "error: compile failed\n", builds: 2},
		{name: "successful update", output: mismatch, initialExit: 1, builds: 2, ok: true, changed: true},
		{name: "initial success verifies restored file", builds: 2, ok: true},
		{name: "restored file verification failure", verifyExit: 38, builds: 2},
		{name: "tee failure", output: mismatch, initialExit: 1, builds: 2, failTee: true},
		{name: "sed failure restores original", sed: "fail", builds: 0},
		{name: "BSD sed empty backup suffix", sed: "bsd", output: mismatch, initialExit: 1, builds: 2, ok: true, changed: true},
		{name: "GNU sed arguments", sed: "gnu", output: mismatch, initialExit: 1, builds: 2, ok: true, changed: true},
		{name: "Docker failure", transport: "docker", output: "error: unsafe repository ownership\n", initialExit: 39, builds: 1},
		{name: "Docker mounts paths with spaces", transport: "docker", output: mismatch, initialExit: 1, builds: 2, ok: true, changed: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, logs, env := nixVendorHashFixture(t)
			bin := filepath.Join(root, "bin")
			buildStub := `#!/bin/bash
set -eu
count=0
[ ! -f "$NIX_TEST_COUNT" ] || count=$(cat "$NIX_TEST_COUNT")
count=$((count + 1))
printf '%s\n' "$count" > "$NIX_TEST_COUNT"
cat default.nix >> "$NIX_TEST_OBSERVED_FILES"
if [ "$count" -eq 1 ]; then
 printf '%s' "$NIX_TEST_INITIAL_OUTPUT"
 exit "$NIX_TEST_INITIAL_EXIT"
fi
printf '%s' "$NIX_TEST_VERIFY_OUTPUT"
exit "$NIX_TEST_VERIFY_EXIT"
`
			name := "nix"
			if tc.transport == "docker" {
				name = "docker"
				buildStub = strings.Replace(buildStub, "count=0", `if [ "$1" = image ]; then exit 0; fi
[ "$1" = run ] || exit 80
for expected in "$NIX_TEST_ROOT:/workspace" "$NIX_TEST_ROOT:$NIX_TEST_ROOT" "$NIX_TEST_COMMON:$NIX_TEST_COMMON"; do
 found=false
 for arg in "$@"; do [ "$arg" != "$expected" ] || found=true; done
 [ "$found" = true ] || { echo "mount argument split: $expected" >&2; exit 81; }
done
count=0`, 1)
				mustWriteNixFixture(t, filepath.Join(root, ".git"), "gitdir: fixture\n", 0o644)
				common := filepath.Join(root, "common git with spaces")
				if err := os.Mkdir(common, 0o755); err != nil {
					t.Fatal(err)
				}
				mustWriteNixFixture(t, filepath.Join(bin, "git"), "#!/bin/bash\nprintf '%s\\n' \"$NIX_TEST_COMMON\"\n", 0o755)
				env = append(env, "NIX_TEST_COMMON="+common)
			}
			mustWriteNixFixture(t, filepath.Join(bin, name), buildStub, 0o755)
			if tc.failTee {
				mustWriteNixFixture(t, filepath.Join(bin, "tee"), "#!/bin/bash\ncat >/dev/null\nexit 74\n", 0o755)
			}
			if tc.sed != "" {
				mustWriteNixFixture(t, filepath.Join(bin, "sed"), `#!/bin/bash
set -eu
if [ "$1" = --version ]; then
 [ "$NIX_TEST_SED" != gnu ] || { printf 'GNU sed fixture\n'; exit 0; }
 exit 1
fi
if [ "$1" != -i ]; then exec /usr/bin/sed "$@"; fi
shift
if [ "$NIX_TEST_SED" = bsd ] || [ "$NIX_TEST_SED" = fail ]; then
 [ "$1" = "" ] || exit 82
 shift
fi
[ "$#" -eq 2 ] || exit 83
if [ "$NIX_TEST_SED" = fail ]; then printf 'partial write\n' > "$2"; exit 75; fi
/usr/bin/sed "$1" "$2" > "$2.fixture"
cat "$2.fixture" > "$2"
rm -f "$2.fixture"
`, 0o755)
			}
			env = append(env, "NIX_TEST_INITIAL_OUTPUT="+tc.output, "NIX_TEST_VERIFY_OUTPUT="+tc.verifyOutput,
				"NIX_TEST_INITIAL_EXIT="+strconv.Itoa(tc.initialExit), "NIX_TEST_VERIFY_EXIT="+strconv.Itoa(tc.verifyExit), "NIX_TEST_SED="+tc.sed)
			original := "# retain this comment\nvendorHash = \"" + nixFixtureHash + "\";\n"
			mustWriteNixFixture(t, filepath.Join(root, "default.nix"), original, 0o640)
			mustWriteNixFixture(t, filepath.Join(root, "default.nix.backup"), "operator backup\n", 0o600)
			output, err := runNixFixture(t, root, env, "bash ./update-nix-vendorhash.sh")
			if (err == nil) != tc.ok {
				t.Errorf("success=%v, want %v: %v\n%s", err == nil, tc.ok, err, output)
			}
			want := original
			if tc.changed {
				want = strings.Replace(original, nixFixtureHash, nixFixtureNewHash, 1)
			}
			assertNixFixtureFile(t, filepath.Join(root, "default.nix"), want)
			assertNixFixtureFile(t, filepath.Join(root, "default.nix.backup"), "operator backup\n")
			dirs, err := filepath.Glob(filepath.Join(logs, "beads-nix-vendorhash.*"))
			if err != nil {
				t.Fatal(err)
			}
			if tc.ok && len(dirs) != 0 {
				t.Errorf("successful run retained scratch: %v", dirs)
			}
			if !tc.ok {
				if len(dirs) != 1 {
					t.Fatalf("failed run recovery directory count=%d", len(dirs))
				}
				if tc.builds > 0 {
					if _, err := os.Stat(filepath.Join(dirs[0], "build.log")); err != nil {
						t.Errorf("missing diagnostic log: %v", err)
					}
				}
			}
			files, err := filepath.Glob(filepath.Join(root, "default.nix*"))
			if err != nil || len(files) != 2 {
				t.Errorf("unexpected backup artifacts: %v (%v)", files, err)
			}
			info, err := os.Stat(filepath.Join(root, "default.nix"))
			if err != nil {
				t.Fatal(err)
			}
			if info.Mode().Perm() != 0o640 {
				t.Errorf("mode=%o, want 640", info.Mode().Perm())
			}
			count, err := os.ReadFile(filepath.Join(root, "build-count"))
			if tc.builds == 0 {
				if !os.IsNotExist(err) {
					t.Errorf("unexpected Nix invocation: %s (%v)", count, err)
				}
			} else if err != nil || strings.TrimSpace(string(count)) != strconv.Itoa(tc.builds) {
				t.Errorf("build count=%s (%v), want %d", count, err, tc.builds)
			}
			if tc.initialExit == 0 && tc.builds == 2 {
				seen, err := os.ReadFile(filepath.Join(root, "observed-files"))
				if err != nil {
					t.Fatal(err)
				}
				if !strings.HasSuffix(string(seen), original) {
					t.Errorf("verification did not use the restored original:\n%s", seen)
				}
			}
		})
	}
}

func TestNixVendorHashWorkflowIndependentBuild(t *testing.T) {
	job := readCIWorkflow(t, "update-vendor-hash.yml").job(t, "update-vendor-hash")
	const stepName = "Verify the build independently of the script's exit status"
	step := job.step(t, stepName)
	if job.ContinueOnError || step.If != "" || (step.ContinueOnError != nil && step.ContinueOnError != false) {
		t.Fatal("independent Nix build may be skipped or ignored")
	}
	if strings.TrimSpace(step.Run) != "nix build .#default" {
		t.Fatalf("independent build=%q", step.Run)
	}
	indices := map[string]int{}
	for i, s := range job.Steps {
		indices[s.Name] = i
	}
	for _, name := range []string{"Update vendorHash", "Detect whether hash changed", "Commit and push hash update"} {
		if _, ok := indices[name]; !ok {
			t.Fatalf("missing workflow step %s", name)
		}
	}
	if !(indices["Update vendorHash"] < indices[stepName] && indices[stepName] < indices["Detect whether hash changed"] && indices[stepName] < indices["Commit and push hash update"]) {
		t.Fatal("verify before reporting a match or committing")
	}
	if runtime.GOOS == "windows" {
		t.Skip("workflow shell execution requires POSIX")
	}
	root, _, env := nixVendorHashFixture(t)
	mustWriteNixFixture(t, filepath.Join(root, "bin", "nix"), "#!/bin/bash\necho independent-build-refused >&2\nexit 37\n", 0o755)
	out, err := runNixFixture(t, root, env, step.Run)
	if err == nil || !strings.Contains(out, "independent-build-refused") {
		t.Fatalf("independent build failed to refuse: %v\n%s", err, out)
	}
}

func nixVendorHashFixture(t *testing.T) (string, string, []string) {
	t.Helper()
	root := filepath.Join(t.TempDir(), "Nix workspace with spaces")
	logs := t.TempDir()
	for _, d := range []string{root, filepath.Join(root, "bin"), filepath.Join(root, "home")} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	for _, name := range []string{"bash", "cat", "cp", "grep", "head", "mktemp", "mv", "rm", "sed", "tail", "tee"} {
		tool, err := exec.LookPath(name)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.Symlink(tool, filepath.Join(root, "bin", name)); err != nil {
			t.Fatal(err)
		}
	}
	source, err := os.ReadFile(filepath.Join(sourceRepoRoot(t), "scripts", "update-nix-vendorhash.sh"))
	if err != nil {
		t.Fatal(err)
	}
	// Fence legacy absolute log names during red runs; status/rollback code is unchanged.
	script := strings.ReplaceAll(string(source), "/tmp/nix-", filepath.ToSlash(logs)+"/nix-")
	mustWriteNixFixture(t, filepath.Join(root, "update-nix-vendorhash.sh"), script, 0o755)
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		t.Fatal(err)
	}
	env := []string{"PATH=" + filepath.Join(root, "bin"), "HOME=" + filepath.Join(root, "home"), "TMPDIR=" + logs, "NIX_TEST_ROOT=" + canonical, "NIX_TEST_COUNT=" + filepath.Join(root, "build-count"), "NIX_TEST_OBSERVED_FILES=" + filepath.Join(root, "observed-files"), "GIT_CONFIG_NOSYSTEM=1", "GIT_CONFIG_GLOBAL=/dev/null"}
	return root, logs, env
}

func runNixFixture(t *testing.T, root string, env []string, command string) (string, error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "/bin/bash", "--noprofile", "--norc", "-e", "-c", command)
	cmd.Dir = root
	cmd.Env = env
	cmd.WaitDelay = time.Second
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func mustWriteNixFixture(t *testing.T, path, content string, mode os.FileMode) {
	t.Helper()
	if info, err := os.Lstat(path); err == nil && info.Mode()&os.ModeSymlink != 0 {
		if err := os.Remove(path); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(path, []byte(content), mode); err != nil {
		t.Fatal(err)
	}
}
func assertNixFixtureFile(t *testing.T, path, want string) {
	t.Helper()
	got, err := os.ReadFile(path)
	if err != nil || string(got) != want {
		t.Errorf("%s=%q (%v), want %q", path, got, err, want)
	}
}
