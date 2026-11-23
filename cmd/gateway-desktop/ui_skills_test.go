package main

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"
)

func skillsTestBootstrap() apitypes.Bootstrap {
	boot := apitypes.Bootstrap{MCPURL: "http://127.0.0.1:8788/mcp"}
	boot.Registry.Servers = []apitypes.Server{
		{ID: "gh", Name: "GitHub CLI", Kind: "cli", Command: "/opt/homebrew/bin/gh", CommandSHA256: strings.Repeat("ab", 32), Env: ghPresetEnv(), Enabled: true},
	}
	boot.Scopes.Repositories = []apitypes.Repository{{ID: "repo-1", Name: "alpha-repo"}}
	boot.Skills = []apitypes.Skill{
		{ID: "release-notes", Name: "Release Notes", Enabled: true, AllowedRepos: []string{"repo-1"}, AllowedServices: []string{"gh"}},
		{ID: "triage", Enabled: false, AllowedRepos: []string{"*"}, AllowedServices: []string{"*"}},
	}
	return boot
}

func TestBuildSkillFromDraft(t *testing.T) {
	skill, err := buildSkillFromDraft(skillDraft{id: " pr-review ", name: " PR Review ", repos: "repo-1, repo-2\nrepo-1", services: "*"})
	if err != nil {
		t.Fatal(err)
	}
	if skill.ID != "pr-review" || skill.Name != "PR Review" || !skill.Enabled ||
		!slices.Equal(skill.AllowedRepos, []string{"repo-1", "repo-2"}) || !slices.Equal(skill.AllowedServices, []string{"*"}) {
		t.Fatalf("unexpected skill: %+v", skill)
	}
	if s, err := buildSkillFromDraft(skillDraft{id: "x", repos: "", services: ""}); err != nil || len(s.AllowedRepos) != 0 || s.AllowedRepos == nil {
		t.Fatalf("empty scope must be an explicit empty allowlist: %+v err=%v", s, err)
	}
	for _, bad := range []skillDraft{
		{id: "", repos: "*", services: "*"},
		{id: "bad id", repos: "*", services: "*"},
		{id: "ok", repos: "*, repo-1", services: "*"},
		{id: "ok", repos: "*", services: "svc/1"},
	} {
		if _, err := buildSkillFromDraft(bad); err == nil {
			t.Fatalf("expected draft %+v to be rejected", bad)
		}
	}
}

func TestSkillScopeTextResolvesNames(t *testing.T) {
	boot := skillsTestBootstrap()
	if got := skillScopeText(boot.Skills[0], &boot); got != "alpha-repo · GitHub CLI" {
		t.Fatalf("scope = %q", got)
	}
	if got := skillScopeText(boot.Skills[1], &boot); got != "全部仓库 · 全部服务" {
		t.Fatalf("wildcard scope = %q", got)
	}
	if got := skillScopeText(apitypes.Skill{AllowedRepos: []string{}, AllowedServices: []string{"unknown"}}, &boot); got != "不放行 · unknown" {
		t.Fatalf("empty/unknown scope = %q", got)
	}
}

// TestServicesSkillAllowlistCRUD drives the embedded allowlist section on the
// Services page: list, create, toggle and delete through the gateway API.
func TestServicesSkillAllowlistCRUD(t *testing.T) {
	var (
		mu      sync.Mutex
		saved   []apitypes.Skill
		deleted []string
	)
	boot := skillsTestBootstrap()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		w.Header().Set("Content-Type", "application/json")
		mu.Lock()
		defer mu.Unlock()
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/bootstrap":
			_ = json.NewEncoder(w).Encode(boot)
		case r.Method == http.MethodPut && r.URL.Path == "/api/skills":
			var s apitypes.Skill
			_ = json.Unmarshal(raw, &s)
			saved = append(saved, s)
			i := slices.IndexFunc(boot.Skills, func(old apitypes.Skill) bool { return old.ID == s.ID })
			if i >= 0 {
				boot.Skills[i] = s
			} else {
				boot.Skills = append(boot.Skills, s)
			}
			_ = json.NewEncoder(w).Encode(s)
		case r.Method == http.MethodDelete && strings.HasPrefix(r.URL.Path, "/api/skills/"):
			id := strings.TrimPrefix(r.URL.Path, "/api/skills/")
			deleted = append(deleted, id)
			boot.Skills = slices.DeleteFunc(boot.Skills, func(s apitypes.Skill) bool { return s.ID == id })
			_, _ = w.Write([]byte(`{"deleted":true}`))
		default:
			_, _ = w.Write([]byte(`{}`))
		}
	}))
	defer srv.Close()

	uiApp := NewDesktopUI(&gatewayRuntime{UIURL: srv.URL, MCPURL: boot.MCPURL, Mode: modeAttached})
	mu.Lock()
	bootCopy := boot
	bootCopy.Skills = slices.Clone(boot.Skills)
	mu.Unlock()
	uiApp.State = &bootCopy
	uiApp.Loading = false
	uiApp.ViewMode = ViewServices
	uiApp.marketState().loaded = true
	tester := ui.NewTester(uiApp.View, 1400, 1600)
	waitAsyncIdle(t, tester, uiApp)

	for _, want := range []string{
		"Skill 准入白名单",
		"声明式护栏（skill_trust = declared）：当调用显式声明 skill_id 时校验白名单。",
		"release-notes", "Release Notes", "alpha-repo · GitHub CLI",
		"triage", "全部仓库 · 全部服务",
	} {
		if !tester.HasText(want) {
			t.Fatalf("missing %q in services page: %v", want, tester.Texts())
		}
	}
	if tester.HasText("Skill 市场") || tester.HasText("安装") {
		t.Fatal("the allowlist must not render marketplace affordances")
	}

	// Create.
	if err := tester.Click("新增 Skill"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	ks := uiApp.servicesState().skillsState()
	ks.draft = skillDraft{id: "pr-review", name: "PR Review", repos: "repo-1", services: "gh"}
	if err := tester.Click("保存准入"); err != nil {
		t.Fatal(err)
	}
	waitAsyncIdle(t, tester, uiApp)
	if !tester.HasText("pr-review") || ks.adding {
		t.Fatal("created skill must appear and close the form")
	}

	// Toggle the disabled wildcard entry on.
	if err := tester.Click("启用 Skill triage"); err != nil {
		t.Fatal(err)
	}
	waitAsyncIdle(t, tester, uiApp)

	// Delete with confirmation.
	if err := tester.Click("删除 Skill release-notes"); err != nil {
		t.Fatal(err)
	}
	tester.Frame()
	if !tester.HasText("删除这条 Skill 准入？") {
		t.Fatal("delete must ask for confirmation")
	}
	if err := tester.Click("删除"); err != nil {
		t.Fatal(err)
	}
	waitAsyncIdle(t, tester, uiApp)

	mu.Lock()
	defer mu.Unlock()
	if len(saved) != 2 {
		t.Fatalf("saved = %+v, want create + toggle", saved)
	}
	if c := saved[0]; c.ID != "pr-review" || c.Name != "PR Review" || !c.Enabled ||
		!slices.Equal(c.AllowedRepos, []string{"repo-1"}) || !slices.Equal(c.AllowedServices, []string{"gh"}) {
		t.Fatalf("unexpected created skill: %+v", c)
	}
	if tg := saved[1]; tg.ID != "triage" || !tg.Enabled || !slices.Equal(tg.AllowedRepos, []string{"*"}) {
		t.Fatalf("toggle must resend the full skill enabled: %+v", tg)
	}
	if len(deleted) != 1 || deleted[0] != "release-notes" {
		t.Fatalf("deleted = %v, want [release-notes]", deleted)
	}
}

func TestServicesSkillAllowlistEnglish(t *testing.T) {
	i18n.WithLang(i18n.LangEN, func() {
		boot := skillsTestBootstrap()
		uiApp := NewDesktopUI(&gatewayRuntime{UIURL: "http://127.0.0.1:1", Mode: modeAttached})
		uiApp.State = &boot
		uiApp.Loading = false
		tester := ui.NewTester(func(c *ui.Context) {
			defer uiApp.enterHeadlessFrame()()
			uiApp.renderSkillAllowlistSection(c, uiApp.servicesState())
		}, 1400, 600)
		for _, want := range []string{"Skill admission allowlist", "All repositories · All services", "Add skill"} {
			if !tester.HasText(want) {
				t.Fatalf("missing %q: %v", want, tester.Texts())
			}
		}
		for _, text := range tester.Texts() {
			if cjkTextRe.MatchString(text) {
				t.Fatalf("untranslated text in English mode: %q", text)
			}
		}
	})
}
