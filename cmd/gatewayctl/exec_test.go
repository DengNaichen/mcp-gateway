package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"slices"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/workspace"
)

func TestGatewayctlExecSubprocess(t *testing.T) {
	args := os.Getenv("GATEWAYCTL_EXEC_TEST_ARGS")
	if args == "" {
		return
	}
	var argv []string
	if err := json.Unmarshal([]byte(args), &argv); err != nil {
		t.Fatal(err)
	}
	os.Args = append([]string{"gatewayctl"}, argv...)
	main()
	os.Exit(0)
}

func TestGatewayctlExecScopeAndApprovalExit(t *testing.T) {
	dataDir := t.TempDir()
	t.Setenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"), dataDir)
	t.Setenv("GATEWAY_TOKEN", "test-gateway-token")
	key, err := workspace.EnsureScopeKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}
	expectedArgv := []string{"pr", "merge", "1", "-R=owner/repo", "--squash"}
	var calls atomic.Int32
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.URL.Path != "/api/cli/exec" || r.Method != "POST" || r.Header.Get("Authorization") != "Bearer test-gateway-token" {
			t.Errorf("wrong exec request: %s %s", r.Method, r.URL.Path)
			w.WriteHeader(400)
			return
		}
		raw, err := io.ReadAll(r.Body)
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		var in apitypes.CLIExecRequest
		if err := json.Unmarshal(raw, &in); err != nil || in.ServiceID != "gh" || in.SkillID != "release" || !slices.Equal(in.Argv, expectedArgv) {
			t.Errorf("incorrect exec body: %s (%v)", raw, err)
			w.WriteHeader(400)
			return
		}
		token := r.Header.Get("X-Gateway-Scope")
		hash, _, _, err := workspace.CanonicalArgsHashJSON(raw, workspace.CurrentContextField())
		if err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		if _, err := workspace.VerifyToken(key, token, workspace.VerifyParams{
			Channel: buildinfo.Current().Channel, URLAgent: "gatewayctl", AllowedOps: []string{"cli_exec"}, ArgsHash: hash,
		}, time.Now()); err != nil {
			t.Errorf("token did not bind the sent body: %v", err)
			w.WriteHeader(400)
			return
		}
		if in.ApprovalID != "" && in.ApprovalID != "apv_test" {
			t.Errorf("wrong approval ID: %q", in.ApprovalID)
			w.WriteHeader(400)
			return
		}
		status := "approval_required"
		if in.ApprovalID != "" {
			status = "approval_pending"
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(202)
		if err := json.NewEncoder(w).Encode(apitypes.InvokeResult{
			Status: status, Protocol: "cli", Approval: &apitypes.ApprovalChallenge{ApprovalID: "apv_test"},
		}); err != nil {
			t.Error(err)
		}
	}))
	defer host.Close()

	for _, status := range []string{"approval_required", "approval_pending"} {
		args := []string{"-url", host.URL, "exec", "-skill", "release"}
		if status == "approval_pending" {
			args = append(args, "-approval", "apv_test")
		}
		args = append(append(args, "--"), expectedArgv...)
		out, stderr, code := execGatewayctl(t, args)
		var challenge struct {
			Status       string `json:"status"`
			ApprovalID   string `json:"approval_id"`
			RetryCommand string `json:"retry_command"`
		}
		if err := json.Unmarshal(out, &challenge); err != nil || code != 75 || stderr != "" ||
			challenge.Status != status || challenge.ApprovalID != "apv_test" || !strings.Contains(challenge.RetryCommand, "-approval=apv_test") {
			t.Fatalf("challenge exit=%d stdout=%s stderr=%s err=%v", code, out, stderr, err)
		}
	}
	t.Setenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"), t.TempDir())
	_, stderr, code := execGatewayctl(t, append([]string{"-url", host.URL, "exec", "-skill", "release", "--"}, expectedArgv...))
	if code != 1 || !strings.Contains(stderr, "could not read scope.key") || calls.Load() != 2 {
		t.Fatalf("missing scope key: exit=%d stderr=%q requests=%d", code, stderr, calls.Load())
	}
}

func TestGatewayctlExecScopeOff(t *testing.T) {
	t.Setenv(buildinfo.EnvironmentKey("GATEWAY_DATA_DIR"), t.TempDir())
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Gateway-Scope") != "" {
			t.Error("scope off request carried a signature")
		}
		w.Header().Set("Content-Type", "application/json")
		if err := json.NewEncoder(w).Encode(apitypes.InvokeResult{Protocol: "cli", ToolID: "other.repo.view", Data: map[string]string{"name": "owner/repo"}}); err != nil {
			t.Error(err)
		}
	}))
	defer host.Close()
	out, stderr, code := execGatewayctl(t, []string{"-url", host.URL, "exec", "-scope", "off", "-service", "other", "repo", "view", "owner/repo"})
	if code != 0 || stderr != "" || !bytes.Contains(out, []byte("other.repo.view")) {
		t.Fatalf("scope off: exit=%d stdout=%s stderr=%q", code, out, stderr)
	}
}

func execGatewayctl(t *testing.T, args []string) ([]byte, string, int) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.CommandContext(t.Context(), os.Args[0], "-test.run=^TestGatewayctlExecSubprocess$")
	cmd.Env = append(os.Environ(), "GATEWAYCTL_EXEC_TEST_ARGS="+string(raw))
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	out, err := cmd.Output()
	if err == nil {
		return out, stderr.String(), 0
	}
	var e *exec.ExitError
	if errors.As(err, &e) {
		return out, stderr.String(), e.ExitCode()
	}
	t.Fatal(err)
	return nil, "", -1
}
