//go:build unix

package cliexec

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"gatewaylab/internal/registry"
)

func TestRunPreservesLiteralCommentBody(t *testing.T) {
	src := fakeGH(t, `#!/bin/sh
test "$1 $2 $3 $5 $6" = 'issue comment -R=owner/repo -- 1' || exit 2
printf '%s' "${4#--body=}"
`)
	for _, body := range []string{"--admin", "$(id)"} {
		got, err := Run(t.Context(), src, "issue.comment", map[string]any{"number": 1, "repo": "owner/repo", "body": body}, "test-token")
		if err != nil || got.(map[string]any)["output"] != body {
			t.Fatalf("body changed at execution: %v %v", got, err)
		}
	}
}

func TestRunBoundsOutputAndTimesOut(t *testing.T) {
	args := map[string]any{"number": 1, "repo": "owner/repo"}
	src := fakeGH(t, "#!/bin/sh\n/usr/bin/head -c 524289 /dev/zero\n")
	_, err := Run(t.Context(), src, "pr.view", args, "test-token")
	assertCLIError(t, err, "cli_output_limit")

	src = fakeGH(t, "#!/bin/sh\nsleep 20 &\nwait\n")
	ctx, cancel := context.WithTimeout(t.Context(), 200*time.Millisecond)
	defer cancel()
	started := time.Now()
	_, err = Run(ctx, src, "pr.view", args, "test-token")
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
