package clientplugin

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

	"github.com/pelletier/go-toml/v2"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/workspace"
)

type fakeRunner struct {
	home  string
	calls []string
}

func (f *fakeRunner) LookPath(binary string) (string, error) {
	switch binary {
	case "claude", "codex", "pi":
		return "/usr/local/bin/" + binary, nil
	default:
		return "", exec.ErrNotFound
	}
}

func (f *fakeRunner) Run(_ context.Context, env []string, dir, binary string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, binary+" "+strings.Join(args, " "))
	slug := buildinfo.Current().Slug
	ver := buildinfo.Current().Version
	pluginID := slug + "@" + slug

	envMap := map[string]string{}
	for _, kv := range env {
		if k, v, ok := strings.Cut(kv, "="); ok {
			envMap[k] = v
		}
	}

	switch binary {
	case "claude":
		cfgDir := envMap["CLAUDE_CONFIG_DIR"]
		if cfgDir == "" {
			cfgDir = filepath.Join(f.home, ".claude")
		}
		if len(args) >= 4 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add" {
			mpPath := args[len(args)-1]
			knownPath := filepath.Join(cfgDir, "plugins", "known_marketplaces.json")
			doc := map[string]any{}
			if b, err := os.ReadFile(knownPath); err == nil {
				_ = json.Unmarshal(b, &doc)
			}
			doc[slug] = map[string]any{
				"source":          map[string]any{"source": "directory", "path": mpPath},
				"installLocation": mpPath,
			}
			raw, _ := json.MarshalIndent(doc, "", "  ")
			return nil, nil, atomicWriteFile(knownPath, raw, 0o600)
		}
		if len(args) >= 4 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "remove" {
			knownPath := filepath.Join(cfgDir, "plugins", "known_marketplaces.json")
			doc := map[string]any{}
			if b, err := os.ReadFile(knownPath); err == nil {
				_ = json.Unmarshal(b, &doc)
			}
			delete(doc, args[3])
			raw, _ := json.MarshalIndent(doc, "", "  ")
			return nil, nil, atomicWriteFile(knownPath, raw, 0o600)
		}
		if len(args) >= 4 && args[0] == "plugin" && args[1] == "install" {
			cachePath := filepath.Join(cfgDir, "plugins", "cache", slug, slug, ver)
			if err := os.MkdirAll(cachePath, 0o755); err != nil {
				return nil, nil, err
			}
			instPath := filepath.Join(cfgDir, "plugins", "installed_plugins.json")
			instDoc := map[string]any{"version": 2, "plugins": map[string]any{
				pluginID: []map[string]any{{"scope": "user", "installPath": cachePath, "version": ver}},
			}}
			raw, _ := json.MarshalIndent(instDoc, "", "  ")
			if err := atomicWriteFile(instPath, raw, 0o600); err != nil {
				return nil, nil, err
			}
			settingsPath := filepath.Join(cfgDir, "settings.json")
			sDoc := map[string]any{}
			if b, err := os.ReadFile(settingsPath); err == nil {
				_ = json.Unmarshal(b, &sDoc)
			}
			enabled, _ := sDoc["enabledPlugins"].(map[string]any)
			if enabled == nil {
				enabled = map[string]any{}
			}
			enabled[pluginID] = true
			sDoc["enabledPlugins"] = enabled
			sRaw, _ := json.MarshalIndent(sDoc, "", "  ")
			return nil, nil, atomicWriteFile(settingsPath, sRaw, 0o600)
		}
		if len(args) >= 4 && args[0] == "plugin" && args[1] == "uninstall" {
			instPath := filepath.Join(cfgDir, "plugins", "installed_plugins.json")
			raw, _ := json.MarshalIndent(map[string]any{"version": 2, "plugins": map[string]any{}}, "", "  ")
			return nil, nil, atomicWriteFile(instPath, raw, 0o600)
		}

	case "codex":
		cfgDir := envMap["CODEX_HOME"]
		if cfgDir == "" {
			cfgDir = filepath.Join(f.home, ".codex")
		}
		cfgPath := filepath.Join(cfgDir, "config.toml")
		doc := map[string]any{}
		if b, err := os.ReadFile(cfgPath); err == nil {
			_ = toml.Unmarshal(b, &doc)
		}
		if len(args) >= 4 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "add" {
			mpPath := args[3]
			mps, _ := doc["marketplaces"].(map[string]any)
			if mps == nil {
				mps = map[string]any{}
			}
			mps[slug] = map[string]any{"source_type": "local", "source": mpPath}
			doc["marketplaces"] = mps
			raw, _ := toml.Marshal(doc)
			return []byte("{}"), nil, atomicWriteFile(cfgPath, raw, 0o600)
		}
		if len(args) >= 4 && args[0] == "plugin" && args[1] == "marketplace" && args[2] == "remove" {
			if mps, ok := doc["marketplaces"].(map[string]any); ok {
				delete(mps, args[3])
			}
			raw, _ := toml.Marshal(doc)
			return []byte("{}"), nil, atomicWriteFile(cfgPath, raw, 0o600)
		}
		if len(args) >= 3 && args[0] == "plugin" && args[1] == "add" {
			pls, _ := doc["plugins"].(map[string]any)
			if pls == nil {
				pls = map[string]any{}
			}
			pls[pluginID] = map[string]any{"enabled": true}
			doc["plugins"] = pls
			if err := os.MkdirAll(filepath.Join(cfgDir, "plugins", "cache", slug, slug, ver), 0o755); err != nil {
				return nil, nil, err
			}
			raw, _ := toml.Marshal(doc)
			return []byte("{}"), nil, atomicWriteFile(cfgPath, raw, 0o600)
		}
		if len(args) >= 3 && args[0] == "plugin" && args[1] == "remove" {
			if pls, ok := doc["plugins"].(map[string]any); ok {
				delete(pls, args[2])
			}
			_ = os.RemoveAll(filepath.Join(cfgDir, "plugins", "cache", slug))
			raw, _ := toml.Marshal(doc)
			return []byte("{}"), nil, atomicWriteFile(cfgPath, raw, 0o600)
		}

	case "pi":
		cfgDir := envMap["PI_CODING_AGENT_DIR"]
		if cfgDir == "" {
			cfgDir = filepath.Join(f.home, ".pi", "agent")
		}
		settingsPath := filepath.Join(cfgDir, "settings.json")
		doc := map[string]any{}
		if b, err := os.ReadFile(settingsPath); err == nil {
			_ = json.Unmarshal(b, &doc)
		}
		if len(args) >= 2 && args[0] == "install" {
			pkgDir := args[len(args)-1]
			rel, err := filepath.Rel(dir, pkgDir)
			if err != nil {
				rel = pkgDir
			}
			doc["packages"] = []any{rel}
			raw, _ := json.MarshalIndent(doc, "", "  ")
			return nil, nil, atomicWriteFile(settingsPath, raw, 0o600)
		}
		if len(args) >= 2 && args[0] == "remove" {
			doc["packages"] = []any{}
			raw, _ := json.MarshalIndent(doc, "", "  ")
			return nil, nil, atomicWriteFile(settingsPath, raw, 0o600)
		}
	}

	return nil, nil, fmtErrorf("unexpected command: %s %v", binary, args)
}

func fmtErrorf(format string, args ...any) error {
	return errors.New(strings.TrimSpace(strings.ReplaceAll(format, "%s", args[0].(string))))
}

func TestAllClientsInstallUpdateRemoveAndLegacyMigration(t *testing.T) {
	home := t.TempDir()
	slug := buildinfo.Current().Slug
	cc := clientconfig.New(home, filepath.Join(home, "."+slug, "client-config"), func(string) string { return "" })
	runner := &fakeRunner{home: home}
	mgr := New(Options{
		HomeDir:       home,
		StateDir:      filepath.Join(home, "."+slug, "plugins"),
		BinPath:       "/opt/test/" + slug + "-hook",
		Getenv:        func(string) string { return "" },
		Runner:        runner,
		ClientConfigs: cc,
	})

	endpoint1 := "http://127.0.0.1:8788/mcp"
	endpoint2 := "http://127.0.0.1:9797/mcp"

	// Pre-populate legacy clientconfig entries so we verify automatic upgrade & revoke.
	for _, id := range clientOrder {
		if err := cc.Write(id, endpoint1); err != nil {
			t.Fatalf("legacy cc.Write(%s): %v", id, err)
		}
	}

	statuses := mgr.Statuses(endpoint1)
	if len(statuses) != 5 {
		t.Fatalf("expected 5 statuses, got %d", len(statuses))
	}
	for _, st := range statuses {
		if !st.HasLegacyMCP || st.State != "待升级" || !st.CanInstall {
			t.Fatalf("expected 待升级 for %s, got %+v", st.ID, st)
		}
	}

	for _, id := range clientOrder {
		if err := mgr.Install(id, endpoint1); err != nil {
			t.Fatalf("Install(%s): %v", id, err)
		}
	}

	// Legacy global MCP configs managed by clientconfig must be cleanly revoked.
	for _, legacyRow := range cc.Statuses(endpoint1) {
		if legacyRow.State != "未配置" {
			t.Fatalf("expected legacy MCP entry for %s to be revoked, got %+v", legacyRow.ID, legacyRow)
		}
	}

	for _, st := range mgr.Statuses(endpoint1) {
		if st.State != "已安装" || !st.CanRemove || st.CanInstall || st.CanUpdate {
			t.Fatalf("expected 已安装 for %s, got %+v", st.ID, st)
		}
	}

	// Changing endpoint must surface 需要更新 for all 5 clients.
	for _, st := range mgr.Statuses(endpoint2) {
		if st.State != "需要更新" || !st.CanUpdate {
			t.Fatalf("expected 需要更新 for %s, got %+v", st.ID, st)
		}
	}

	for _, id := range clientOrder {
		if err := mgr.Update(id, endpoint2); err != nil {
			t.Fatalf("Update(%s): %v", id, err)
		}
	}
	for _, st := range mgr.Statuses(endpoint2) {
		if st.State != "已安装" || st.CanUpdate {
			t.Fatalf("expected 已安装 after Update for %s, got %+v", st.ID, st)
		}
	}

	// Verify OpenCode rendered JS syntax with node --check when available.
	if nodeBin, err := exec.LookPath("node"); err == nil {
		ocPath := filepath.Join(home, ".config", "opencode", "plugins", slug+".js")
		if out, err := exec.Command(nodeBin, "--check", ocPath).CombinedOutput(); err != nil {
			t.Fatalf("node --check %s failed: %v\n%s", ocPath, err, out)
		}
	}

	// Remove all 5 and confirm clean state.
	for _, id := range clientOrder {
		if err := mgr.Remove(id); err != nil {
			t.Fatalf("Remove(%s): %v", id, err)
		}
	}
	for _, st := range mgr.Statuses(endpoint2) {
		if st.State != "未安装" || !st.CanInstall || st.CanRemove {
			t.Fatalf("expected 未安装 after Remove for %s, got %+v", st.ID, st)
		}
	}
}

func TestConflictProtectionForCursorAndOpenCode(t *testing.T) {
	home := t.TempDir()
	slug := buildinfo.Current().Slug
	mgr := New(Options{
		HomeDir:  home,
		StateDir: filepath.Join(home, "."+slug, "plugins"),
		BinPath:  "/opt/test/" + slug + "-hook",
		Getenv:   func(string) string { return "" },
		Runner:   &fakeRunner{home: home},
	})

	// Pre-existing unmanaged Cursor dir must block install.
	cursorDir := filepath.Join(home, ".cursor", "plugins", "local", slug)
	if err := os.MkdirAll(cursorDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cursorDir, "custom.txt"), []byte("user"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Install(ClientCursor, "http://127.0.0.1:8788/mcp"); err == nil {
		t.Fatal("expected Cursor install to refuse overwriting unmanaged directory")
	}

	// Install OpenCode cleanly, then modify externally -> Update and Remove must refuse.
	if err := mgr.Install(ClientOpenCode, "http://127.0.0.1:8788/mcp"); err != nil {
		t.Fatalf("Install OpenCode: %v", err)
	}
	ocPlugin := filepath.Join(home, ".config", "opencode", "plugins", slug+".js")
	if err := os.WriteFile(ocPlugin, []byte("// user modified"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := mgr.Update(ClientOpenCode, "http://127.0.0.1:8788/mcp"); err == nil {
		t.Fatal("expected OpenCode update to refuse overwriting external edits")
	}
	if err := mgr.Remove(ClientOpenCode); err == nil {
		t.Fatal("expected OpenCode remove to refuse deleting externally modified file")
	}
}

func TestRenderedHookWrapperExecutesRealHookBinary(t *testing.T) {
	home := t.TempDir()
	dataDir := filepath.Join(home, "gwdata")
	key, err := workspace.EnsureScopeKey(dataDir)
	if err != nil {
		t.Fatal(err)
	}

	hookBin := filepath.Join(t.TempDir(), "gateway-hook")
	build := exec.Command("go", "build", "-ldflags=-X gatewaylab/internal/buildinfo.channel="+buildinfo.Current().Channel, "-o", hookBin, "../../cmd/gateway-hook")
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build gateway-hook: %v\n%s", err, out)
	}

	slug := buildinfo.Current().Slug
	mgr := New(Options{
		HomeDir:  home,
		StateDir: filepath.Join(home, "."+slug, "plugins"),
		DataDir:  dataDir,
		BinPath:  hookBin,
		Getenv:   func(string) string { return "" },
		Runner:   &fakeRunner{home: home},
	})
	if err := mgr.Install(ClientCursor, "http://127.0.0.1:8788/mcp"); err != nil {
		t.Fatal(err)
	}

	wrapper := filepath.Join(home, ".cursor", "plugins", "local", slug, "scripts", "run-gateway-hook")
	repoDir := t.TempDir()
	ev := `{"hook_event_name":"preToolUse","tool_name":"MCP:discover_tools","workspace_roots":[` + mustJSON(repoDir) + `],"tool_input":{"intent":"check logs"}}`
	cmd := exec.Command("sh", wrapper, "cursor")
	cmd.Stdin = strings.NewReader(ev)
	var out, errBuf bytes.Buffer
	cmd.Stdout = &out
	cmd.Stderr = &errBuf
	if err := cmd.Run(); err != nil {
		t.Fatalf("wrapper run failed: %v\n%s", err, errBuf.String())
	}
	var resp struct {
		Permission   string         `json:"permission"`
		UpdatedInput map[string]any `json:"updated_input"`
	}
	if err := json.Unmarshal(out.Bytes(), &resp); err != nil {
		t.Fatalf("invalid wrapper output: %v\n%s", err, out.String())
	}
	tok, _ := resp.UpdatedInput[workspace.CurrentContextField()].(string)
	updatedRaw, _ := json.Marshal(resp.UpdatedInput)
	argsHash, _, _, _ := workspace.CanonicalArgsHashJSON(updatedRaw, workspace.CurrentContextField())
	claims, err := workspace.VerifyToken(key, tok, workspace.VerifyParams{
		Channel:    buildinfo.Current().Channel,
		URLAgent:   "cursor",
		AllowedOps: []string{"discover_tools"},
		ArgsHash:   argsHash,
	}, time.Now())
	if err != nil || claims.Cwd != repoDir {
		t.Fatalf("VerifyToken failed: err=%v claims=%+v", err, claims)
	}
}

func TestRealInstalledCLIsEndToEnd(t *testing.T) {
	home := t.TempDir()
	slug := buildinfo.Current().Slug
	cc := clientconfig.New(home, filepath.Join(home, "."+slug, "client-config"), func(string) string { return "" })
	mgr := New(Options{
		HomeDir:       home,
		StateDir:      filepath.Join(home, "."+slug, "plugins"),
		BinPath:       "/usr/local/bin/" + slug + "-hook",
		Getenv:        func(string) string { return "" },
		ClientConfigs: cc,
	})

	runner := execRunner{homeDir: home}
	for _, id := range []string{ClientClaude, ClientCodex, ClientPi} {
		if _, err := runner.LookPath(id); err != nil {
			t.Logf("skipping real CLI %s: %v", id, err)
			continue
		}
		if err := cc.Write(id, "http://127.0.0.1:8788/mcp"); err != nil {
			t.Fatalf("real cc.Write(%s): %v", id, err)
		}
		if err := mgr.Install(id, "http://127.0.0.1:8788/mcp"); err != nil {
			t.Fatalf("real Install(%s): %v", id, err)
		}
		for _, legacyRow := range cc.Statuses("http://127.0.0.1:8788/mcp") {
			if legacyRow.ID == id && legacyRow.State != "未配置" {
				t.Fatalf("expected legacy MCP entry for %s to be revoked after real Install, got %+v", id, legacyRow)
			}
		}
		for _, st := range mgr.Statuses("http://127.0.0.1:8788/mcp") {
			if st.ID == id && st.State != "已安装" {
				t.Fatalf("expected 已安装 after real Install(%s), got %+v", id, st)
			}
		}
		if err := mgr.Update(id, "http://127.0.0.1:9797/mcp"); err != nil {
			t.Fatalf("real Update(%s): %v", id, err)
		}
		if err := mgr.Remove(id); err != nil {
			t.Fatalf("real Remove(%s): %v", id, err)
		}
		for _, st := range mgr.Statuses("http://127.0.0.1:9797/mcp") {
			if st.ID == id && st.State != "未安装" {
				t.Fatalf("expected 未安装 after real Remove(%s), got %+v", id, st)
			}
		}
	}
}

func mustJSON(s string) string {
	b, _ := json.Marshal(s)
	return string(b)
}
