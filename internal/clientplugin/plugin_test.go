package clientplugin

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io/fs"
	"maps"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/pelletier/go-toml/v2"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/workspace"
	pluginassets "gatewaylab/plugins"
)

type fakeRunner struct {
	home  string
	calls []string
}

func (f *fakeRunner) LookPath(binary string) (string, error) {
	switch binary {
	case "claude", "codex", "pi", "agy":
		return "/usr/local/bin/" + binary, nil
	default:
		return "", exec.ErrNotFound
	}
}

func (f *fakeRunner) Run(_ context.Context, env []string, dir, binary string, args ...string) ([]byte, []byte, error) {
	f.calls = append(f.calls, binary+" "+strings.Join(args, " "))
	slug := buildinfo.Current().Slug
	ver := buildinfo.PluginVersion()
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

	case "agy":
		cfgDir := filepath.Join(f.home, ".gemini", "config")
		if h := envMap["HOME"]; h != "" {
			cfgDir = filepath.Join(h, ".gemini", "config")
		}
		if len(args) >= 3 && args[0] == "plugin" && args[1] == "install" {
			srcDir := args[2]
			destDir := filepath.Join(cfgDir, "plugins", slug)
			if err := os.RemoveAll(destDir); err != nil {
				return nil, nil, err
			}
			err := filepath.WalkDir(srcDir, func(path string, d os.DirEntry, walkErr error) error {
				if walkErr != nil {
					return walkErr
				}
				rel, err := filepath.Rel(srcDir, path)
				if err != nil || rel == "." {
					return err
				}
				dst := filepath.Join(destDir, rel)
				if d.IsDir() {
					return os.MkdirAll(dst, 0o755)
				}
				info, err := d.Info()
				if err != nil {
					return err
				}
				data, err := os.ReadFile(path)
				if err != nil {
					return err
				}
				return atomicWriteFile(dst, data, info.Mode().Perm())
			})
			return nil, nil, err
		}
		if len(args) >= 3 && args[0] == "plugin" && args[1] == "uninstall" {
			return nil, nil, os.RemoveAll(filepath.Join(cfgDir, "plugins", args[2]))
		}
		if len(args) >= 3 && args[0] == "plugin" && args[1] == "enable" {
			cfgPath := filepath.Join(cfgDir, "config.json")
			raw, _ := json.MarshalIndent(map[string]any{"plugins": map[string]any{args[2]: map[string]any{"enabled": true}}}, "", "  ")
			return nil, nil, atomicWriteFile(cfgPath, raw, 0o600)
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
	clientIDs := clientconfig.ClientIDs()
	for _, id := range clientIDs {
		if err := cc.Write(id, endpoint1); err != nil {
			t.Fatalf("legacy cc.Write(%s): %v", id, err)
		}
	}

	statuses := mgr.Statuses(endpoint1)
	if len(statuses) != 6 {
		t.Fatalf("expected 6 statuses, got %d", len(statuses))
	}
	for _, st := range statuses {
		if !st.HasLegacyMCP || st.State != "待升级" || !st.CanInstall {
			t.Fatalf("expected 待升级 for %s, got %+v", st.ID, st)
		}
	}

	for _, id := range clientIDs {
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

	for _, id := range clientIDs {
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

	// Remove all 6 and confirm clean state.
	for _, id := range clientIDs {
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

	// Verify the installed Codex wrapper passes its installation identity through
	// the real hook binary and into signed provenance.
	if err := mgr.Install(ClientCodex, "http://127.0.0.1:8788/mcp"); err != nil {
		t.Fatal(err)
	}
	receipt, err := mgr.loadReceipt(ClientCodex)
	if err != nil {
		t.Fatal(err)
	}
	codexWrapper := filepath.Join(mgr.stateDir, "marketplaces", ClientCodex, "plugins", slug, "scripts", "run-gateway-hook")
	codexEvent := `{"hook_event_name":"PreToolUse","tool_name":"mcp__` + strings.ReplaceAll(slug, "-", "_") + `__discover_tools","cwd":` + mustJSON(repoDir) + `,"tool_input":{"intent":"check logs"}}`
	codexCmd := exec.Command("sh", codexWrapper, "codex")
	codexCmd.Stdin = strings.NewReader(codexEvent)
	codexOut, err := codexCmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	var codexResp struct {
		Output struct {
			Input map[string]any `json:"updatedInput"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal(codexOut, &codexResp); err != nil {
		t.Fatal(err)
	}
	codexInput := codexResp.Output.Input
	codexHash, _ := workspace.CanonicalArgsHash(codexInput, workspace.CurrentContextField())
	codexToken, _ := codexInput[workspace.CurrentContextField()].(string)
	codexClaims, err := workspace.VerifyToken(key, codexToken, workspace.VerifyParams{Channel: buildinfo.Current().Channel, URLAgent: "codex", AllowedOps: []string{"discover_tools"}, ArgsHash: codexHash}, time.Now())
	if err != nil || codexClaims.InstallationID != receipt.InstallationID {
		t.Fatalf("Codex installation identity missing: %v", err)
	}

	// Verify installed Antigravity wrapper executes real gateway-hook binary too.
	if err := mgr.Install(ClientAntigravity, "http://127.0.0.1:8788/mcp"); err != nil {
		t.Fatal(err)
	}
	agyWrapper := filepath.Join(home, ".gemini", "config", "plugins", slug, "scripts", "run-gateway-hook")
	agyEv := `{"conversationId":"agy-sess-1","hookEventName":"PreToolUse","cwd":` + mustJSON(repoDir) + `,"workspacePaths":[` + mustJSON(repoDir) + `],"toolCall":{"name":` + mustJSON("mcp_"+slug+"_discover_tools") + `,"args":{"intent":"check logs"}}}`
	agyCmd := exec.Command("sh", agyWrapper, "antigravity")
	agyCmd.Stdin = strings.NewReader(agyEv)
	var agyOut, agyErr bytes.Buffer
	agyCmd.Stdout = &agyOut
	agyCmd.Stderr = &agyErr
	if err := agyCmd.Run(); err != nil {
		t.Fatalf("antigravity wrapper failed: %v\n%s", err, agyErr.String())
	}
	var agyResp struct {
		Decision  string         `json:"decision"`
		Overwrite map[string]any `json:"overwrite"`
	}
	if err := json.Unmarshal(agyOut.Bytes(), &agyResp); err != nil {
		t.Fatalf("invalid antigravity wrapper output: %v\n%s", err, agyOut.String())
	}
	agyTok, _ := agyResp.Overwrite[workspace.CurrentContextField()].(string)
	mergedRaw, _ := json.Marshal(map[string]any{"intent": "check logs", workspace.CurrentContextField(): agyTok})
	agyHash, _, _, _ := workspace.CanonicalArgsHashJSON(mergedRaw, workspace.CurrentContextField())
	agyClaims, err := workspace.VerifyToken(key, agyTok, workspace.VerifyParams{
		Channel:    buildinfo.Current().Channel,
		URLAgent:   "antigravity",
		AllowedOps: []string{"discover_tools"},
		ArgsHash:   agyHash,
	}, time.Now())
	if err != nil || agyClaims.Cwd != repoDir || agyClaims.SID != "agy-sess-1" {
		t.Fatalf("VerifyToken antigravity failed: err=%v claims=%+v", err, agyClaims)
	}
}

func TestAntigravityLazyHooksCoexist(t *testing.T) {
	home, cwd := t.TempDir(), t.TempDir()
	t.Setenv("GATEWAY_HOOK_BIN", "")
	channels := []struct {
		name string
		slug string
		key  []byte
		root string
	}{
		{name: "stable", slug: "mcp-gateway"},
		{name: "dev", slug: "mcp-gateway-dev"},
	}
	for i := range channels {
		ch := &channels[i]
		dataDir := t.TempDir()
		key, err := workspace.EnsureScopeKey(dataDir)
		if err != nil {
			t.Fatal(err)
		}
		ch.key = key
		hookBin := filepath.Join(t.TempDir(), ch.slug+"-hook")
		build := exec.Command("go", "build", "-ldflags=-X gatewaylab/internal/buildinfo.channel="+ch.name, "-o", hookBin, "../../cmd/gateway-hook")
		if out, err := build.CombinedOutput(); err != nil {
			t.Fatalf("build %s hook: %v\n%s", ch.name, err, out)
		}
		mgr := New(Options{
			HomeDir:  home,
			StateDir: filepath.Join(home, "."+ch.slug, "plugins"),
			DataDir:  dataDir,
			BinPath:  hookBin,
			Getenv:   func(string) string { return "" },
			Runner:   missingAgyRunner{inner: &fakeRunner{home: home}},
		})
		mgr.slug = ch.slug
		if err := mgr.Install(ClientAntigravity, "http://127.0.0.1:8788/mcp"); err != nil {
			t.Fatal(err)
		}
		ch.root = mgr.agyPluginDir()
		raw, err := os.ReadFile(filepath.Join(ch.root, "hooks.json"))
		if err != nil {
			t.Fatal(err)
		}
		var config map[string]struct {
			PreToolUse []struct{ Matcher string }
		}
		if err := json.Unmarshal(raw, &config); err != nil {
			t.Fatal(err)
		}
		hooks := config[ch.slug].PreToolUse
		if len(hooks) != 1 || !regexp.MustCompile(hooks[0].Matcher).MatchString("call_mcp_tool") {
			t.Fatalf("%s hooks.json does not match call_mcp_tool", ch.slug)
		}
	}

	const original = `{"arguments":{},"tool_id":"notion.notion-get-self"}`
	for _, target := range channels {
		for _, order := range [][]int{{0, 1}, {1, 0}} {
			t.Run(target.name+"/"+channels[order[0]].name+"-first", func(t *testing.T) {
				args := map[string]json.RawMessage{
					"ServerName": json.RawMessage(mustJSON(target.slug + "_" + target.slug)),
					"ToolName":   json.RawMessage(`"invoke_tool"`),
					"Arguments":  json.RawMessage(original),
				}
				for _, i := range order {
					ch := channels[i]
					raw, err := json.Marshal(args)
					if err != nil {
						t.Fatal(err)
					}
					event := `{"hookEventName":"PreToolUse","cwd":` + mustJSON(cwd) + `,"toolCall":{"name":"call_mcp_tool","args":` + string(raw) + `}}`
					cmd := exec.Command("sh", "./scripts/run-gateway-hook", "antigravity")
					cmd.Dir = ch.root
					cmd.Stdin = strings.NewReader(event)
					out, err := cmd.Output()
					if err != nil {
						t.Fatalf("%s hook: %v", ch.name, err)
					}
					var response struct {
						Decision  string                     `json:"decision"`
						Overwrite map[string]json.RawMessage `json:"overwrite"`
					}
					if err := json.Unmarshal(out, &response); err != nil || response.Decision != "allow" {
						t.Fatalf("%s hook response: %s (%v)", ch.name, out, err)
					}
					if ch.slug != target.slug && string(out) != "{\"decision\":\"allow\"}\n" {
						t.Fatalf("%s hook overwrote arguments for %s: %s", ch.name, target.name, out)
					}
					// Model Antigravity's shallow overwrite merge between serial hooks.
					maps.Copy(args, response.Overwrite)
				}
				field := workspace.ContextFieldForSlug(target.slug)
				hash, token, hasToken, err := workspace.CanonicalArgsHashJSON(args["Arguments"], field)
				originalHash, _, _, _ := workspace.CanonicalArgsHashJSON([]byte(original), field)
				if err != nil || !hasToken || hash != originalHash {
					t.Fatalf("final Arguments changed or lacks signature: %s (%v)", args["Arguments"], err)
				}
				_, err = workspace.VerifyToken(target.key, token, workspace.VerifyParams{
					Channel:    target.name,
					URLAgent:   "antigravity",
					AllowedOps: []string{"invoke_tool"},
					ArgsHash:   hash,
				}, time.Now())
				if err != nil {
					t.Fatalf("final %s signature failed verification: %v", target.name, err)
				}
			})
		}
	}
}

func TestAntigravityDesktopFallbackAndLegacyIDEWarning(t *testing.T) {
	home := t.TempDir()
	slug := buildinfo.Current().Slug
	legacyIDEPath := filepath.Join(home, ".gemini", "antigravity", "mcp_config.json")
	if err := atomicWriteFile(legacyIDEPath, []byte(`{"mcpServers":{"`+slug+`":{"serverUrl":"http://127.0.0.1:8788/mcp"}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	noCLIRunner := &struct{ fakeRunner }{fakeRunner{home: home}}
	// Use a runner where agy is absent to verify Desktop-only direct installation/removal.
	mgr := New(Options{
		HomeDir:  home,
		StateDir: filepath.Join(home, "."+slug, "plugins"),
		BinPath:  "/opt/test/" + slug + "-hook",
		Getenv:   func(string) string { return "" },
		Runner:   missingAgyRunner{inner: noCLIRunner},
	})
	var agyStatus Status
	for _, st := range mgr.Statuses("http://127.0.0.1:8788/mcp") {
		if st.ID == ClientAntigravity {
			agyStatus = st
		}
	}
	if !agyStatus.CanInstall || agyStatus.CLIAvailable || len(agyStatus.Warnings) == 0 {
		t.Fatalf("unexpected desktop-only Antigravity status: %+v", agyStatus)
	}
	if err := mgr.Install(ClientAntigravity, "http://127.0.0.1:8788/mcp"); err != nil {
		t.Fatalf("desktop fallback install failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(home, ".gemini", "config", "plugins", slug, "hooks.json")); err != nil {
		t.Fatalf("expected installed hooks.json: %v", err)
	}
	if err := mgr.Remove(ClientAntigravity); err != nil {
		t.Fatalf("desktop fallback remove failed: %v", err)
	}
}

type missingAgyRunner struct{ inner CommandRunner }

func (r missingAgyRunner) LookPath(binary string) (string, error) {
	if binary == "agy" {
		return "", exec.ErrNotFound
	}
	return r.inner.LookPath(binary)
}

func (r missingAgyRunner) Run(ctx context.Context, env []string, dir, binary string, args ...string) ([]byte, []byte, error) {
	return r.inner.Run(ctx, env, dir, binary, args...)
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
	for _, id := range []string{ClientClaude, ClientCodex, ClientPi, ClientAntigravity} {
		look := id
		if id == ClientAntigravity {
			look = "agy"
		}
		if _, err := runner.LookPath(look); err != nil {
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

func TestEmbeddedAssetsHaveNoDeadFilesAndTemplatesRender(t *testing.T) {
	expectedAssets := []string{
		"antigravity/README.md",
		"antigravity/rules/AGENTS.md",
		"mcp-gateway/README.md",
		"mcp-gateway/assets/icon.png",
		"mcp-gateway/rules/mcp-gateway.mdc",
		"mcp-gateway/scripts/run-gateway-hook",
		"mcp-gateway/skills/mcp-gateway/SKILL.md",
		"opencode/index.js",
		"pi/README.md",
		"pi/extensions/mcp-gateway.ts",
	}
	var actual []string
	if err := fs.WalkDir(pluginassets.Files, ".", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return err
		}
		actual = append(actual, p)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	slices.Sort(actual)
	if !slices.Equal(actual, expectedAssets) {
		t.Fatalf("embedded plugin assets mismatch:\ngot:  %v\nwant: %v", actual, expectedAssets)
	}

	for _, slug := range []string{"mcp-gateway", "mcp-gateway-dev"} {
		mgr := &Manager{slug: slug, version: "0.0.4", appName: "MCP Gateway"}
		for _, fn := range []func() (renderedBundle, error){
			func() (renderedBundle, error) {
				return mgr.renderClaudeMarketplace("http://127.0.0.1:8788/mcp", "/bin/hook", "/data/dir")
			},
			func() (renderedBundle, error) {
				return mgr.renderCodexMarketplace("http://127.0.0.1:8788/mcp", "/bin/hook", "/data/dir", "")
			},
			func() (renderedBundle, error) {
				return mgr.renderCursorPlugin("http://127.0.0.1:8788/mcp", "/bin/hook", "/data/dir")
			},
			func() (renderedBundle, error) {
				return mgr.renderOpenCodePlugin("http://127.0.0.1:8788/mcp", "/bin/hook", "/data/dir")
			},
			func() (renderedBundle, error) {
				return mgr.renderPiPackage("http://127.0.0.1:8788/mcp", "/bin/hook", "/data/dir")
			},
			func() (renderedBundle, error) {
				return mgr.renderAntigravityPlugin("http://127.0.0.1:8788/mcp", "/bin/hook", "/data/dir")
			},
		} {
			if _, err := fn(); err != nil {
				t.Fatalf("render failed for slug %s: %v", slug, err)
			}
		}
	}

	if _, err := renderTemplateAsset("opencode/index.js", templateReplacement{old: "NONEXISTENT_PLACEHOLDER", new: "x"}); err == nil {
		t.Fatal("expected missing placeholder to return error")
	}
}

func TestWorkspaceAndClientConfigHostParity(t *testing.T) {
	if !slices.Equal(clientconfig.ClientIDs(), workspace.HookAgents) {
		t.Fatalf("host catalog IDs mismatch: clientconfig=%v workspace=%v", clientconfig.ClientIDs(), workspace.HookAgents)
	}
	home := t.TempDir()
	env := map[string]string{
		"CODEX_HOME":          filepath.Join(home, "custom-codex"),
		"CLAUDE_CONFIG_DIR":   filepath.Join(home, "custom-claude"),
		"XDG_CONFIG_HOME":     filepath.Join(home, "custom-xdg"),
		"PI_CODING_AGENT_DIR": filepath.Join(home, "custom-pi"),
	}
	getenv := func(k string) string { return env[k] }
	for _, id := range clientconfig.ClientIDs() {
		if gotCC, gotWS := clientconfig.ClientEndpoint("http://127.0.0.1:8788", id), workspace.AgentMCPURL("http://127.0.0.1:8788", id); gotCC != gotWS {
			t.Fatalf("endpoint mismatch for %s: clientconfig=%q workspace=%q", id, gotCC, gotWS)
		}
		spec, err := workspace.BuildSnippet(workspace.SnippetOptions{
			Agent:    id,
			Endpoint: "http://127.0.0.1:8788/mcp",
			BinPath:  "/bin/hook",
			HomeDir:  home,
			Getenv:   getenv,
		})
		if err != nil {
			t.Fatalf("BuildSnippet(%s): %v", id, err)
		}
		wantDir := clientconfig.HostConfigDir(id, home, getenv)
		if !strings.HasPrefix(spec.ConfigPath, wantDir+string(filepath.Separator)) {
			t.Fatalf("config dir mismatch for %s: snippet=%q hostConfigDir=%q", id, spec.ConfigPath, wantDir)
		}
	}
}

func TestCodexTrustHintTracksInstallation(t *testing.T) {
	home, state, data := t.TempDir(), t.TempDir(), t.TempDir()
	options := Options{
		HomeDir:  home,
		StateDir: state,
		DataDir:  data,
		BinPath:  "/bin/hook",
		Getenv:   func(string) string { return "" },
		Runner:   &fakeRunner{home: home},
	}
	mgr := New(options)
	endpoint := "http://127.0.0.1:8788/mcp"
	warning := func() bool { t.Helper(); return len(mgr.observeCodex(endpoint, false).Warnings) > 0 }
	if !warning() {
		t.Fatal("new client must show trust hint")
	}
	if err := mgr.Install(ClientCodex, endpoint); err != nil {
		t.Fatal(err)
	}
	receipt, err := mgr.loadReceipt(ClientCodex)
	if err != nil {
		t.Fatal(err)
	}
	if receipt.InstallationID == "" {
		t.Fatal("missing installation identity")
	}
	if !warning() {
		t.Fatal("installation alone hid hint")
	}
	if err := workspace.RecordHookSuccess(data, ClientClaude, receipt.InstallationID); err != nil {
		t.Fatal(err)
	}
	if !warning() {
		t.Fatal("other client hid hint")
	}
	if err := workspace.RecordHookSuccess(data, ClientCodex, "old"); err != nil {
		t.Fatal(err)
	}
	if !warning() {
		t.Fatal("old install hid hint")
	}
	if err := workspace.RecordHookSuccess(data, ClientCodex, receipt.InstallationID); err != nil {
		t.Fatal(err)
	}
	if warning() {
		t.Fatal("successful hook did not hide hint")
	}
	mgr = New(options)
	if warning() {
		t.Fatal("success lost across manager restart")
	}
	if err := mgr.Update(ClientCodex, endpoint); err != nil {
		t.Fatal(err)
	}
	if !warning() {
		t.Fatal("updated hook reused old installation success")
	}
	next, _ := mgr.loadReceipt(ClientCodex)
	if next.InstallationID == receipt.InstallationID {
		t.Fatal("identity reused")
	}
}
