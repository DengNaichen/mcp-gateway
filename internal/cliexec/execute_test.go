//go:build unix

package cliexec

import (
	"context"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"gatewaylab/internal/registry"
)

func TestRunPreservesLiteralCommentBody(t *testing.T) {
	src := fakeGH(t, `#!/bin/sh
test "$1 $2 $3 $5 $6" = 'issue comment -R=owner/repo -- 1' || exit 2
printf '%s' "${4#--body=}"
`)
	dataDir := privateTestDataDir(t)
	for _, body := range []string{"--admin", "$(id)"} {
		got, err := Run(t.Context(), dataDir, src, "issue.comment", map[string]any{"number": 1, "repo": "owner/repo", "body": body}, "test-token")
		if err != nil || got.(map[string]any)["output"] != body {
			t.Fatalf("body changed at execution: %v %v", got, err)
		}
	}
}

func TestRunBoundsOutputAndTimesOut(t *testing.T) {
	dataDir := privateTestDataDir(t)
	args := map[string]any{"number": 1, "repo": "owner/repo"}
	src := fakeGH(t, "#!/bin/sh\n/usr/bin/head -c 524289 /dev/zero\n")
	_, err := Run(t.Context(), dataDir, src, "pr.view", args, "test-token")
	assertCLIError(t, err, "cli_output_limit")

	src = fakeGH(t, "#!/bin/sh\nsleep 20 &\nwait\n")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = Run(ctx, dataDir, src, "pr.view", args, "test-token")
	assertCLIError(t, err, "cli_timeout")
	if elapsed := time.Since(started); elapsed > 2*time.Second {
		t.Fatalf("child process kept pipes open after group cancellation: %v", elapsed)
	}
}

func TestPinRejectsEnvironmentOutsideAllowlist(t *testing.T) {
	for _, key := range []string{"GH_DEBUG", "GH_HOST", "GH_ENTERPRISE_TOKEN", "HTTPS_PROXY", "PATH"} {
		_, err := Pin(registry.Server{Kind: "cli", Command: "gh", Env: map[string]string{"GH_TOKEN": "${GH_TOKEN}", key: "${" + key + "}"}}, "")
		if err == nil || !strings.Contains(err.Error(), "only the GH_TOKEN") {
			t.Fatalf("accepted %s: %v", key, err)
		}
	}
}

func fakeGH(t *testing.T, script string) registry.Server {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "gh")
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	src, err := Pin(registry.Server{Kind: "cli", Command: bin, Env: map[string]string{"GH_TOKEN": "${GH_TOKEN}"}}, "")
	if err != nil {
		t.Fatal(err)
	}
	return src
}

func privateTestDataDir(t *testing.T) string {
	t.Helper()
	dir := filepath.Join(t.TempDir(), "gateway-data")
	if err := os.Mkdir(dir, 0700); err != nil {
		t.Fatal(err)
	}
	return dir
}

func TestRunStagesWithPrivatePermissionsAndCleansUp(t *testing.T) {
	dataDir := privateTestDataDir(t)
	canonical, err := filepath.EvalSymlinks(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", t.TempDir())
	src := fakeGH(t, `#!/bin/sh
for path in "$HOME" "$HOME/bin" "$0"; do
  [ -n "$(find "$path" -prune -perm 0500)" ] || exit 10
done
for path in "$(dirname "$HOME")" "$XDG_CONFIG_HOME" "$GH_CONFIG_DIR" "$TMPDIR"; do
  [ -d "$path" ] && [ -n "$(find "$path" -prune -perm 0700)" ] || exit 11
done
[ "$GIT_DIR" = "$HOME/.no-repository" ] && [ ! -e "$GIT_DIR" ] || exit 12
if [ "$(id -u)" -ne 0 ]; then
  if ( : > "$HOME/new-entry" ) 2>/dev/null; then exit 13; fi
  if ( : > "$HOME/bin/new-entry" ) 2>/dev/null; then exit 14; fi
fi
: > "$TMPDIR/fixture" || exit 15
printf '%s' "$HOME"
`)
	out, err := Run(t.Context(), dataDir, src, "issue.comment", map[string]any{"number": 1, "repo": "owner/repo", "body": "ok"}, "test-token")
	if err != nil {
		t.Fatal(err)
	}
	stage := out.(map[string]any)["output"].(string)
	if filepath.Dir(stage) != canonical || !strings.HasPrefix(filepath.Base(stage), "cliexec-run-") {
		t.Fatalf("stage %q is outside the gateway data directory %q", stage, canonical)
	}
	if _, err := os.Stat(stage); !os.IsNotExist(err) {
		t.Fatalf("stage remained after execution: %v", err)
	}
	if err := os.Chmod(dataDir, 0755); err != nil {
		t.Fatal(err)
	}
	if _, err := makeRunDir(dataDir); err == nil || !strings.Contains(err.Error(), "0700") {
		t.Fatalf("accepted a data directory with broader permissions: %v", err)
	}
}

func TestStagedBinaryRevalidatesIdentityAndHash(t *testing.T) {
	src := fakeGH(t, "#!/bin/sh\nexit 0\n")
	originalBytes, err := os.ReadFile(src.Command)
	if err != nil {
		t.Fatal(err)
	}

	for _, change := range []string{"same bytes on different inode", "changed bytes on same inode", "symlink to original inode"} {
		t.Run(change, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "gh")
			anchor, initial, err := snapshotBinary(src, path)
			if err != nil {
				t.Fatal(err)
			}
			defer anchor.Close()
			if err := verifySnapshot(path, initial, src.CommandSHA256); err != nil {
				t.Fatalf("verified snapshot was rejected: %v", err)
			}
			switch change {
			case "same bytes on different inode":
				if err := os.Remove(path); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, originalBytes, 0500); err != nil {
					t.Fatal(err)
				}
			case "changed bytes on same inode":
				if err := os.Chmod(path, 0700); err != nil {
					t.Fatal(err)
				}
				if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 1\n"), 0500); err != nil {
					t.Fatal(err)
				}
				if err := os.Chmod(path, 0500); err != nil {
					t.Fatal(err)
				}
			case "symlink to original inode":
				original := path + "-original"
				if err := os.Rename(path, original); err != nil {
					t.Fatal(err)
				}
				if err := os.Symlink(original, path); err != nil {
					t.Fatal(err)
				}
			}
			assertCLIError(t, verifySnapshot(path, initial, src.CommandSHA256), "cli_binary_changed")
		})
	}
}

func TestHashRejectsFIFOWithoutWaitingForWriter(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pipe")
	if err := syscall.Mkfifo(path, 0700); err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		_, err := digestExecutable(path, io.Discard)
		done <- err
	}()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("FIFO accepted as an executable")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("opening a FIFO waited on a writer")
	}
}

func TestStagePermissionsUnderIndependentUID(t *testing.T) {
	if os.Geteuid() != 0 {
		t.Skip("requires root to start the probe with a different effective UID")
	}
	public, err := os.MkdirTemp("/tmp", "cliexec-permissions-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(public)
	if err := os.Chmod(public, 0755); err != nil {
		t.Fatal(err)
	}
	dataDir := filepath.Join(public, "gateway-data")
	if err := os.Mkdir(dataDir, 0700); err != nil {
		t.Fatal(err)
	}
	stage, err := makeRunDir(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	restore, err := sealDirectory(stage)
	if err != nil {
		t.Fatal(err)
	}
	defer restore()
	check := func(path string) {
		t.Helper()
		cmd := exec.CommandContext(t.Context(), "/bin/sh", "-c", `[ -x "$1" ] && [ ! -r "$2" ] && [ ! -w "$2" ] && [ ! -x "$2" ]`, "check", public, path)
		cmd.SysProcAttr = &syscall.SysProcAttr{Credential: &syscall.Credential{Uid: 65534, Gid: 65534}}
		if output, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("independent UID had directory access or could not start: %v %s", err, output)
		}
	}
	check(dataDir)
	// Give the probe traversal of the gateway directory to check the staged
	// directory's mode independently of the 0700 ancestor.
	if err := os.Chmod(dataDir, 0711); err != nil {
		t.Fatal(err)
	}
	check(stage)
}
