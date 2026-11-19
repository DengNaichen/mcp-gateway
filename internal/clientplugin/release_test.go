package clientplugin

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/clientconfig"
	"gatewaylab/internal/clientplugin/github"
)

func TestReleasedTemplatesUseTheSameLocalSettingsAsBundledTemplates(t *testing.T) {
	info := buildinfo.Current()
	m := &Manager{slug: info.Slug, version: "1.2.3", appName: info.Name}
	files, err := m.releaseTemplates()
	if err != nil {
		t.Fatal(err)
	}
	release := &releaseManifest{Version: m.version, Templates: files}

	const endpoint = "http://127.0.0.1:8844/mcp?x=1"
	const hook = "/local/path to 'hook'/gateway-hook"
	const installation = "installation-1"
	for _, data := range []string{"", "/path with a 'quote'/data"} {
		original := map[string]func() (renderedBundle, error){
			ClientClaude:      func() (renderedBundle, error) { return m.renderClaudeMarketplace(endpoint, hook, data) },
			ClientCodex:       func() (renderedBundle, error) { return m.renderCodexMarketplace(endpoint, hook, data, installation) },
			ClientCursor:      func() (renderedBundle, error) { return m.renderCursorPlugin(endpoint, hook, data) },
			ClientOpenCode:    func() (renderedBundle, error) { return m.renderOpenCodePlugin(endpoint, hook, data) },
			ClientPi:          func() (renderedBundle, error) { return m.renderPiPackage(endpoint, hook, data) },
			ClientAntigravity: func() (renderedBundle, error) { return m.renderAntigravityPlugin(endpoint, hook, data) },
		}
		for _, id := range clientconfig.ClientIDs() {
			want, err := original[id]()
			if err != nil {
				t.Fatal(err)
			}
			got := renderReleasedBundle(release, releaseRender{id, endpoint, hook, data, installation})
			if got.Digest != want.Digest {
				for i, f := range got.RootFiles {
					if !bytes.Equal(f.Data, want.RootFiles[i].Data) {
						t.Errorf("%s: different local settings in %s", id, f.RelPath)
					}
				}
				t.Fatalf("%s: rendered digest differs", id)
			}
		}
	}
}

func TestPluginReleaseCheckAndExplicitUpdate(t *testing.T) {
	const version = "1.2.3"
	const platform = "darwin-arm64"
	const endpoint = "http://127.0.0.1:8788/mcp"
	hook := []byte("#!/bin/sh\nprintf '{\"decision\":\"allow\"}\\n'\n")
	channel := buildinfo.Current().Channel
	public, private, err := ed25519.GenerateKey(nil)
	if err != nil {
		t.Fatal(err)
	}
	manifest, err := buildRelease(version, platform, hook, private)
	if err != nil {
		t.Fatal(err)
	}
	base := "plugin-" + channel + "-" + platform
	var catalog, manifests, hooks atomic.Int64
	var limited, tamper, badManifest, listingEmpty atomic.Bool
	mux := http.NewServeMux()
	mux.HandleFunc("/api/releases", func(w http.ResponseWriter, r *http.Request) {
		catalog.Add(1)
		if limited.Load() {
			w.Header().Set("Retry-After", "7200")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		if listingEmpty.Load() {
			_, _ = fmt.Fprint(w, `[]`)
			return
		}
		if r.URL.Query().Get("page") == "1" {
			w.Header().Set("Link", `<next>; rel="next"`)
			_, _ = fmt.Fprint(w, `[{"tag_name":"v99.0.0"},{"tag_name":"plugin-v9.9.9","prerelease":true}]`)
			return
		}
		_, _ = fmt.Fprintf(w, `[{"tag_name":"plugin-v%s","assets":[{"name":"%s.json"}]}]`, version, base)
	})
	mux.HandleFunc("/assets/plugin-v"+version+"/"+base+".json", func(w http.ResponseWriter, _ *http.Request) {
		manifests.Add(1)
		if badManifest.Load() {
			_, _ = fmt.Fprint(w, `{"manifest":{},"signature":"invalid"}`)
			return
		}
		_, _ = w.Write(manifest)
	})
	mux.HandleFunc("/assets/plugin-v"+version+"/"+base+"-hook", func(w http.ResponseWriter, _ *http.Request) {
		hooks.Add(1)
		if tamper.Load() {
			_, _ = w.Write(bytes.Repeat([]byte("x"), len(hook)))
			return
		}
		_, _ = w.Write(hook)
	})
	server := httptest.NewServer(mux)
	defer server.Close()

	home := t.TempDir()
	mgr := New(Options{
		HomeDir: home, Runner: missingAgyRunner{inner: &fakeRunner{home: home}},
		Getenv: func(string) string { return "" },
	})
	mgr.updates = &pluginUpdates{
		stateDir: filepath.Join(mgr.stateDir, "releases"), channel: channel, platform: platform, key: public,
		feed: github.Client{APIURL: server.URL + "/api", Assets: server.URL + "/assets/", HTTP: server.Client()},
	}
	ctx := context.Background()
	if err := mgr.Install(ClientAntigravity, endpoint); err != nil {
		t.Fatal(err)
	}
	originalReceipt, err := mgr.loadReceipt(ClientAntigravity)
	if err != nil || originalReceipt == nil || originalReceipt.Release {
		t.Fatalf("expected bundled receipt: %+v, %v", originalReceipt, err)
	}
	wrapper := filepath.Join(mgr.agyPluginDir(), "scripts", "run-gateway-hook")
	original, err := os.ReadFile(wrapper)
	if err != nil {
		t.Fatal(err)
	}

	state, checked, err := mgr.updates.check(ctx, false)
	if err != nil || !checked || state.LastSuccess.IsZero() || state.nextCheck().Sub(state.LastSuccess) != 6*time.Hour {
		t.Fatalf("initial release check: last success=%v checked=%t err=%v", state.LastSuccess, checked, err)
	}
	if catalog.Load() != 2 || manifests.Load() != 1 || hooks.Load() != 0 {
		t.Fatal("checking must paginate and fetch only release metadata")
	}
	if _, checked, err := mgr.updates.check(ctx, false); checked || err != nil || catalog.Load() != 2 {
		t.Fatalf("scheduled check ran again before six hours: checked=%t err=%v", checked, err)
	}
	assertWrapper := func(want []byte) {
		t.Helper()
		got, err := os.ReadFile(wrapper)
		if err != nil || !bytes.Equal(got, want) {
			t.Fatalf("unexpected installed wrapper: err=%v", err)
		}
	}
	assertWrapper(original)
	getRow := func() Status {
		t.Helper()
		for _, row := range mgr.Statuses(endpoint) {
			if row.ID == ClientAntigravity {
				return row
			}
		}
		t.Fatal("missing Antigravity status")
		return Status{}
	}
	if row := getRow(); !row.CanUpdate || row.AvailableVersion != version {
		t.Fatalf("checked release must appear as an available update: %+v", row)
	}

	limited.Store(true)
	retry, checked, err := mgr.updates.check(ctx, true)
	if !checked || err == nil || !retry.LastSuccess.Equal(state.LastSuccess) ||
		time.Until(retry.RetryAt) < 119*time.Minute || retry.Failures != 1 {
		t.Fatalf("check did not preserve last success and honor Retry-After: success=%v retry=%v failures=%d err=%v",
			retry.LastSuccess, retry.RetryAt, retry.Failures, err)
	}
	limited.Store(false)
	if _, checked, err := mgr.updates.check(ctx, false); checked || err != nil {
		t.Fatalf("background check ignored persisted retry time: checked=%t err=%v", checked, err)
	}
	badManifest.Store(true)
	failed, _, err := mgr.updates.check(ctx, true)
	if err == nil || !bytes.Equal(failed.Release, state.Release) || time.Until(failed.RetryAt) < 59*time.Minute {
		t.Fatalf("invalid release check should back off and retain prior metadata: retry=%v err=%v", failed.RetryAt, err)
	}
	badManifest.Store(false)

	tamper.Store(true)
	if err := mgr.Update(ClientAntigravity, endpoint); err == nil {
		t.Fatal("tampered Hook must be rejected")
	}
	assertWrapper(original)
	if r, _ := mgr.loadReceipt(ClientAntigravity); r == nil || r.Version != originalReceipt.Version || r.Release {
		t.Fatalf("tampered update mutated receipt: %+v", r)
	}
	tamper.Store(false)
	if err := mgr.Update(ClientAntigravity, endpoint); err != nil {
		t.Fatal(err)
	}
	if r, _ := mgr.loadReceipt(ClientAntigravity); r == nil || r.Version != version || !r.Release {
		t.Fatalf("release was not installed: %+v", r)
	}
	if hooks.Load() != 2 {
		t.Fatalf("got %d Hook downloads after one rejected and one successful update", hooks.Load())
	}

	raw, err := os.ReadFile(wrapper)
	if err != nil || !strings.Contains(string(raw), mgr.updates.hookPath(version)) {
		t.Fatalf("installed plugin does not use version-specific Hook: %v", err)
	}
	cmd := exec.Command("sh", wrapper, "antigravity")
	cmd.Env = []string{"HOME=" + home, "PATH=/usr/bin:/bin"}
	out, err := cmd.Output()
	if err != nil || string(out) != "{\"decision\":\"allow\"}\n" {
		t.Fatalf("installed Hook failed: %q %v", out, err)
	}

	listingEmpty.Store(true)
	if _, err := mgr.CheckUpdates(ctx); err != nil {
		t.Fatal(err)
	}
	if row := getRow(); row.CanUpdate || row.InstalledVersion != version {
		t.Fatalf("installed release lost after catalog disappeared: %+v", row)
	}
	if err := mgr.Update(ClientAntigravity, endpoint); err != nil {
		t.Fatal(err)
	}
	assertWrapper(raw)
	if hooks.Load() != 2 {
		t.Fatal("already installed release was downloaded again")
	}

	mgr2 := New(Options{HomeDir: home, Runner: mgr.runner, Getenv: mgr.getenv})
	mgr2.updates = mgr.updates
	if row := mgr2.observe(ClientAntigravity, endpoint, false); row.CanUpdate || row.InstalledVersion != version {
		t.Fatalf("restarted manager failed to load pinned release: %+v", row)
	}
	if _, err := parseRelease(manifest, public, channel, "darwin-amd64"); err == nil {
		t.Fatal("release for other platform verified")
	}
	var envelope signedRelease
	if err := json.Unmarshal(manifest, &envelope); err != nil {
		t.Fatal(err)
	}
	envelope.Manifest = bytes.ReplaceAll(envelope.Manifest, []byte(version), []byte("9.9.9"))
	tampered, err := json.Marshal(envelope)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := parseRelease(tampered, public, channel, platform); err == nil {
		t.Fatal("tampered manifest verified")
	}
}

func TestPluginActionsRespectReplacementMarketplace(t *testing.T) {
	for _, id := range []string{ClientClaude, ClientCodex} {
		t.Run(id, func(t *testing.T) {
			home := t.TempDir()
			runner := &fakeRunner{home: home}
			mgr := New(Options{HomeDir: home, Runner: runner, Getenv: func(string) string { return "" }})
			if err := mgr.Install(id, ""); err != nil {
				t.Fatal(err)
			}
			r, err := mgr.loadReceipt(id)
			if err != nil || r == nil {
				t.Fatalf("read installation receipt: %v", err)
			}
			config := filepath.Join(mgr.hostConfigDir(id), "config.toml")
			if id == ClientClaude {
				config = filepath.Join(mgr.hostConfigDir(id), "plugins", "known_marketplaces.json")
			}
			raw, err := os.ReadFile(config)
			if err != nil {
				t.Fatal(err)
			}
			foreign := bytes.ReplaceAll(raw, []byte(r.InstallRoot), []byte(filepath.Join(home, "host-managed")))
			if bytes.Equal(raw, foreign) {
				t.Fatal("test must replace the registered marketplace source")
			}
			if err := atomicWriteFile(config, foreign, 0o600); err != nil {
				t.Fatal(err)
			}
			before := len(runner.calls)
			if err := mgr.Update(id, ""); err == nil {
				t.Fatal("updated plugin owned by a replacement marketplace")
			}
			if err := mgr.Remove(id); err == nil {
				t.Fatal("removed plugin owned by a replacement marketplace")
			}
			if len(runner.calls) != before {
				t.Fatal("ran a host mutation against a replacement marketplace")
			}
		})
	}
}
