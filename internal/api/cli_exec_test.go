//go:build unix

package api

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/policy"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/scope"
)

const cliExecPath = "/api/cli/exec"

func TestCLIIsolationApprovalSkillsAndAudit(t *testing.T) {
	t.Setenv("GH_TOKEN", "")
	t.Setenv("GH_HOST", "example.invalid")
	t.Setenv("GH_ENTERPRISE_TOKEN", "host-enterprise-token")
	t.Setenv("GH_DEBUG", "api")
	t.Setenv("HTTPS_PROXY", "http://example.invalid")
	t.Setenv("GIT_CONFIG_COUNT", "1")
	t.Setenv("GIT_CONFIG_KEY_0", "gateway.fixture")
	t.Setenv("GIT_CONFIG_VALUE_0", "host-git-config")

	f := newActorFixtureWithOptions(t, false, gateway.Options{LocalAdminToken: adminTestToken})
	if err := os.Chmod(f.svcs.DataDir, 0700); err != nil {
		t.Fatal(err)
	}
	cwd := t.TempDir()
	if err := os.Mkdir(filepath.Join(cwd, ".git"), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cwd, ".git", "config"), []byte("[gateway]\nfixture = malicious-cwd\n"), 0600); err != nil {
		t.Fatal(err)
	}
	t.Chdir(cwd)

	repo, err := f.svcs.Scope.SaveRepository(t.Context(), scope.SaveRepositoryRequest{Name: "CLI project", Path: cwd})
	if err != nil {
		t.Fatal(err)
	}
	if err := f.svcs.Scope.SetScopeMode(true); err != nil {
		t.Fatal(err)
	}
	const secret = "gateway-only-m3+/=?&"
	variants := []string{secret, url.QueryEscape(secret), url.PathEscape(secret),
		base64.StdEncoding.EncodeToString([]byte(secret)), base64.RawURLEncoding.EncodeToString([]byte(secret))}
	fixtureJSON, err := json.Marshal(map[string]any{
		"number": 1, "title": strings.Join(variants, " "), "body": "configured field redaction", "extra": "outside output allowlist",
	})
	if err != nil {
		t.Fatal(err)
	}
	script := fmt.Sprintf(`#!/bin/sh
[ "$GH_TOKEN" = '%s' ] || exit 11
[ "$PWD" = "$HOME" ] && [ ! -e .git/config ] || exit 12
[ "$PWD" != '%s' ] || exit 13
[ "$XDG_CONFIG_HOME" = "$HOME/config" ] && [ "$GH_CONFIG_DIR" = "$HOME/gh" ] || exit 14
[ "$TMPDIR" = "$HOME/tmp" ] && [ -d "$TMPDIR" ] || exit 15
[ "$PATH" = /usr/bin:/bin ] && [ "$GIT_CONFIG_NOSYSTEM" = 1 ] && [ "$GIT_CONFIG_GLOBAL" = /dev/null ] || exit 16
[ -z "${GH_HOST+x}${GH_DEBUG+x}${GH_ENTERPRISE_TOKEN+x}${HTTPS_PROXY+x}${GIT_CONFIG_COUNT+x}${GATEWAY_ADMIN_TOKEN+x}" ] || exit 17
printf '%%s' "$GH_TOKEN" >&2
case "$1 $2 $3 $4 $5 $6" in
  'pr view -R=owner/repo --json=number,title,body,state,url -- 1')
    printf '%%s\n' '%s' ;;
  'pr merge -R=owner/repo --squash=true -- 1')
    printf 'merged\n' ;;
  *) exit 18 ;;
esac
`, secret, cwd, fixtureJSON)
	dir := t.TempDir()
	bin := filepath.Join(dir, "fixture-binary")
	if err := os.WriteFile(bin, []byte(script), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(bin, filepath.Join(dir, "gh")); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	server := apitypes.Server{ID: "gh", Name: "GitHub", Kind: "cli", Command: "gh", Enabled: true,
		Env: map[string]string{"GH_TOKEN": "${GH_TOKEN}"}, RedactFields: []string{"body"}}
	save := func(in apitypes.Server) *httptest.ResponseRecorder {
		return localApprovalRequest(f, "PUT", "/api/servers", string(mustJSON(t, in)))
	}
	saved := decodeM1Response[apitypes.Server](t, save(server), 200)
	resolved, err := filepath.EvalSymlinks(bin)
	if err != nil || saved.Command != resolved || saved.CommandSHA256 != fmt.Sprintf("%x", sha256.Sum256([]byte(script))) {
		t.Fatalf("invalid pin: %+v %v", saved, err)
	}
	tools := []string{}
	for _, tool := range f.svcs.Registry.Snapshot().Tools {
		if tool.ServerID == "gh" {
			if tool.Enabled || tool.ReadOnly == (strings.HasSuffix(tool.ID, ".merge") || strings.HasSuffix(tool.ID, ".comment")) {
				t.Fatalf("incorrect defaults: %+v", tool)
			}
			tools = append(tools, tool.ID)
		}
	}
	if len(tools) != 8 {
		t.Fatalf("generated tools: %v", tools)
	}
	decodeM1Response[apitypes.Saved](t, localApprovalRequest(f, "POST", "/api/servers/gh/tools",
		string(mustJSON(t, apitypes.EnableToolsRequest{IDs: tools}))), 200)
	for _, tool := range f.svcs.Registry.Snapshot().Tools {
		if tool.ID == "gh.pr.merge" {
			tool.ReadOnly = true
			decodeM1Response[apitypes.Error](t, localApprovalRequest(f, "PUT", "/api/tools", string(mustJSON(t, wireTool(tool)))), 400)
		}
	}
	decodeM1Response[apitypes.Error](t, localApprovalRequest(f, "POST", "/api/servers/gh/sync", ""), 400)
	decodeM1Response[apitypes.Error](t, localApprovalRequest(f, "PUT", "/api/maintenance/sources", `{"server_id":"gh","format":"pi"}`), 400)
	if _, err := f.svcs.Scope.SaveScopeRule(scope.ScopeRule{RepoKey: repo.ID, Grants: []scope.ServiceGrant{{ServerID: "gh", Access: "rw"}}}); err != nil {
		t.Fatal(err)
	}
	if _, err := f.g.SavePolicyRule(policy.Rule{Enabled: true, Effect: policy.EffectApprove, RepoKey: repo.ID, ServiceID: "gh", Access: "rw"}); err != nil {
		t.Fatal(err)
	}

	view := map[string]any{"service_id": "gh", "argv": []string{"pr", "view", "1", "-R", "owner/repo"}}
	invoke := func(args map[string]any) *httptest.ResponseRecorder {
		t.Helper()
		return signedM1Request(t, f, cwd, "POST", cliExecPath, "cli_exec", args)
	}
	wrongScope := signedM1Request(t, f, cwd, "POST", cliExecPath, "invoke_tool", view)
	requireApprovalError(t, wrongScope, "scope_invalid")
	requireApprovalError(t, f.call(t, "POST", cliExecPath, "", string(mustJSON(t, view))), "scope_required")
	t.Setenv("GH_TOKEN", "unexpected-host-token")
	status := decodeM1Response[apitypes.ServerCredentials](t, localApprovalRequest(f, "GET", "/api/servers/gh/credentials", ""), 200)
	if len(status.Credentials) != 1 || status.Credentials[0].Configured || status.Credentials[0].Saved || !status.Credentials[0].Environment {
		t.Fatalf("host token affected gateway credential status: %+v", status)
	}
	requireCLIError(t, invoke(view), 400, "cli_credential_missing")
	t.Setenv("GH_TOKEN", "")
	credentialBody := string(mustJSON(t, apitypes.SaveCredentialsRequest{Values: map[string]string{"GH_TOKEN": secret}}))
	decodeM1Response[apitypes.Saved](t, localApprovalRequest(f, "PUT", "/api/servers/gh/credentials", credentialBody), 200)

	// PATH changes after registration must have no effect on which bytes run.
	t.Setenv("PATH", t.TempDir()+":/usr/bin:/bin")
	ro := decodeM1Response[apitypes.InvokeResult](t, invoke(view), 200)
	row := ro.Data.(map[string]any)
	if ro.Protocol != "cli" || row["number"] != float64(1) || row["body"] != "[redacted]" || row["extra"] != nil ||
		row["title"] != strings.TrimSpace(strings.Repeat("[redacted] ", len(variants))) {
		t.Fatalf("incorrect controlled response: %+v", ro)
	}
	view["skill_id"] = "bad/id"
	decodeM1Response[apitypes.Error](t, invoke(view), 400)
	view["skill_id"] = "unregistered"
	requireApprovalError(t, invoke(view), "policy_denied")
	denials := decodeM1Response[apitypes.EventPage](t,
		localApprovalRequest(f, "GET", "/api/events/query?protocol=cli&service_id=gh&action=gh.pr.view&skill_id=unregistered&repo_id="+repo.ID, ""), 200)
	if len(denials.Items) != 1 || denials.Items[0].RuleID != "skill:unregistered" || denials.Items[0].Decision != "deny" ||
		!bytes.Contains(denials.Items[0].Detail, []byte(`"skill_trust":"declared"`)) {
		t.Fatalf("missing audit denial: %+v", denials)
	}
	skill := apitypes.Skill{ID: "review", Enabled: true, AllowedRepos: []string{repo.ID}, AllowedServices: []string{"gh"}}
	saveSkill := func() {
		t.Helper()
		s := decodeM1Response[apitypes.Skill](t, localApprovalRequest(f, "PUT", "/api/skills", string(mustJSON(t, skill))), 200)
		if s.UpdatedAt == "" {
			t.Fatal("saved skill without timestamp")
		}
	}
	saveSkill()
	view["skill_id"] = "review"
	decodeM1Response[apitypes.InvokeResult](t, invoke(view), 200)
	wrongRepo := signedM1Request(t, f, t.TempDir(), "POST", cliExecPath, "cli_exec", view)
	decodeM1Response[apitypes.Error](t, wrongRepo, 403)
	boot := decodeM1Response[apitypes.Bootstrap](t, localApprovalRequest(f, "GET", "/api/bootstrap", ""), 200)
	if len(boot.Skills) != 1 || boot.Skills[0].ID != "review" {
		t.Fatalf("bootstrap skills: %+v", boot.Skills)
	}

	merge := map[string]any{"service_id": "gh", "argv": []string{"pr", "merge", "-R=owner/repo", "1", "--squash"}, "skill_id": "review"}
	id := requireChallenge(t, invoke(merge), "approval_required")
	merge["approval_id"] = id
	if got := requireChallenge(t, invoke(merge), "approval_pending"); got != id {
		t.Fatal("approval changed on retry")
	}
	decideLocal(t, f, id, "approve")
	delete(merge, "skill_id")
	requireApprovalError(t, invoke(merge), "approval_invalid")
	merge["skill_id"] = "review"
	// A matching typed API invocation can consume the same approval.
	typed := map[string]any{"tool_id": "gh.pr.merge", "arguments": map[string]any{"repo": "owner/repo", "method": "squash", "number": 1},
		"skill_id": "review", "approval_id": id}
	rw := decodeM1Response[apitypes.InvokeResult](t, signedM1Request(t, f, cwd, "POST", "/api/invoke", "invoke_tool", typed), 200)
	if rw.Data.(map[string]any)["output"] != "merged" {
		t.Fatalf("approved write did not execute: %+v", rw)
	}
	requireApprovalError(t, invoke(merge), "approval_invalid")

	delete(merge, "approval_id")
	id = requireChallenge(t, invoke(merge), "approval_required")
	decideLocal(t, f, id, "approve")
	merge["approval_id"] = id
	marker := filepath.Join(dir, "replacement-ran")
	updated := []byte(strings.Replace(script, "#!/bin/sh\n", fmt.Sprintf("#!/bin/sh\nprintf ran > '%s'\n", marker), 1))
	if err := os.WriteFile(bin, updated, 0700); err != nil {
		t.Fatal(err)
	}
	requireCLIError(t, invoke(view), 409, "cli_binary_changed")
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("replacement ran before the explicit repin: %v", err)
	}
	mismatch := decodeM1Response[apitypes.EventPage](t,
		localApprovalRequest(f, "GET", "/api/events/query?protocol=cli&action=gh.pr.view&status=error&skill_id=review&repo_id="+repo.ID, ""), 200)
	if len(mismatch.Items) != 1 || !bytes.Contains(mismatch.Items[0].Detail, []byte("cli_binary_changed")) {
		t.Fatalf("missing binary-change audit: %+v", mismatch)
	}
	saved.CommandSHA256 = ""
	requireCLIError(t, save(saved), 409, "cli_binary_changed")
	saved.CommandSHA256 = fmt.Sprintf("%x", sha256.Sum256(updated))
	decodeM1Response[apitypes.Server](t, save(saved), 200)
	requireApprovalError(t, invoke(merge), "approval_invalid")
	decodeM1Response[apitypes.InvokeResult](t, invoke(view), 200)

	delete(merge, "approval_id")
	id = requireChallenge(t, invoke(merge), "approval_required")
	decideLocal(t, f, id, "approve")
	merge["approval_id"] = id
	decodeM1Response[apitypes.InvokeResult](t, invoke(merge), 200)

	delete(merge, "approval_id")
	id = requireChallenge(t, invoke(merge), "approval_required")
	decideLocal(t, f, id, "approve")
	merge["approval_id"] = id
	skill.Enabled = false
	saveSkill()
	requireApprovalError(t, invoke(merge), "policy_denied")
	decodeM1Response[apitypes.Deleted](t, localApprovalRequest(f, "DELETE", "/api/skills/review", ""), 200)
	list := decodeM1Response[[]apitypes.Skill](t, localApprovalRequest(f, "GET", "/api/skills", ""), 200)
	if len(list) != 0 {
		t.Fatalf("skill still listed after deletion: %+v", list)
	}

	files, err := filepath.Glob(filepath.Join(f.svcs.DataDir, "gateway.db*"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no SQLite files: %v", err)
	}
	for _, path := range files {
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		for _, variant := range variants {
			if bytes.Contains(raw, []byte(variant)) {
				t.Fatalf("found an unredacted credential variant in %s", filepath.Base(path))
			}
		}
	}
}

func requireCLIError(t *testing.T, out *httptest.ResponseRecorder, status int, code string) {
	t.Helper()
	if e := decodeM1Response[apitypes.Error](t, out, status); e.Code != code {
		t.Fatalf("got %+v, want %s", e, code)
	}
}

func TestSkillAdminAndMCPAdmission(t *testing.T) {
	f := newActorFixture(t, true)
	for _, route := range []struct{ method, path string }{
		{"GET", "/api/skills"}, {"PUT", "/api/skills"}, {"DELETE", "/api/skills/doc"},
	} {
		decodeM1Response[apitypes.Error](t, f.call(t, route.method, route.path, observerTestToken, "{}"), 403)
	}
	decodeM1Response[apitypes.Error](t, f.call(t, "PUT", "/api/skills", adminTestToken, `{"id":"bad","allowed_repos":["*","other"]}`), 400)
	const skill = `{"id":"doc","enabled":true,"allowed_repos":["*"],"allowed_services":["svc"]}`
	decodeM1Response[apitypes.Skill](t, f.call(t, "PUT", "/api/skills", adminTestToken, skill), 200)
	host := httptest.NewServer(f.handler)
	defer host.Close()
	transport := &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp", HTTPClient: &http.Client{Transport: headerTransport{headers: map[string]string{"Authorization": "Bearer " + observerTestToken}, base: http.DefaultTransport}}}
	client := mcp.NewClient(&mcp.Implementation{Name: "skill-test", Version: "1"}, nil)
	session, err := client.Connect(t.Context(), transport, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession(session)
	for _, tc := range []struct {
		id      string
		wantErr bool
	}{
		{"doc", false}, {"unregistered", true}, {"bad/id", true}, {"", false},
	} {
		args := map[string]any{"tool_id": "svc.observe", "arguments": map[string]any{}}
		if tc.id != "" {
			args["skill_id"] = tc.id
		}
		out, err := session.CallTool(t.Context(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: args})
		if err != nil || tc.wantErr != out.IsError {
			t.Fatalf("skill=%q MCP invocation: %+v %v", tc.id, out, err)
		}
	}
}

func TestCLIEndpointAndUnknownKindFailClosed(t *testing.T) {
	f := newActorFixture(t, false)
	cliBody := `{"service_id":"svc","argv":["pr","view","1","-R","owner/repo"]}`
	if err := f.svcs.Registry.SaveTool(registry.Tool{ID: "svc.pr.view", Name: "View", ServerID: "svc", Description: "view", Enabled: true,
		Actors: []string{"developer"}, Path: "/view", Method: "GET", InputSchema: objectSchema(map[string]any{})}); err != nil {
		t.Fatal(err)
	}
	requireCLIError(t, f.call(t, "POST", cliExecPath, "", cliBody), 404, "cli_service_not_found")
	requireCLIError(t, f.call(t, "POST", cliExecPath, "", `{"service_id":"gh","argv":["auth","token"]}`), 400, "cli_command_denied")
	bad := registry.Server{ID: "unknown", Name: "unknown", Kind: "unknown", Endpoint: "https://example.invalid"}
	decodeM1Response[apitypes.Error](t, f.call(t, "PUT", "/api/servers", "", string(mustJSON(t, bad))), 400)
	if err := f.svcs.Registry.Update(func(updated registry.Registry) (registry.Registry, error) {
		for i := range updated.Servers {
			if updated.Servers[i].ID == "svc" {
				updated.Servers[i].Kind = bad.Kind
			}
		}
		return updated, nil
	}); err != nil {
		t.Fatal(err)
	}
	requireCLIError(t, f.call(t, "POST", "/api/invoke", "", `{"tool_id":"svc.develop","arguments":{}}`), 400, "service_kind_invalid")
	decodeM1Response[apitypes.Error](t, f.call(t, "POST", "/api/servers/svc/sync", "", ""), 400)
	if f.developerCalls.Load() != 0 {
		t.Fatalf("unexpected upstream requests: %d", f.developerCalls.Load())
	}
}
