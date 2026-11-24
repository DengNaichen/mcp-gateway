package main

import (
	"testing"

	"github.com/egoist/mygo/ui"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/i18n"
)

func TestPrepareSettingsSubmitAndCanSave(t *testing.T) {
	baseState := &apitypes.Bootstrap{
		KeyConfigured: true,
		Settings: apitypes.Settings{
			BaseURL:        "https://api.openai.com/v1",
			Model:          "gpt-6-luna",
			MaxTools:       5,
			TimeoutSeconds: 30,
		},
		Profiles: []apitypes.ModelProfile{
			{ID: "p1", Active: true, KeyConfigured: true, BaseURL: "https://api.openai.com/v1", Model: "gpt-6-luna", MaxTools: 5, TimeoutSeconds: 30},
		},
		Setup: apitypes.SetupStatus{
			ModelReady: true,
		},
	}

	t.Run("missingModelReturnsValidationMessage", func(t *testing.T) {
		st := &settingsState{config: apitypes.Settings{BaseURL: "https://api.openai.com/v1"}, maxToolsFloat: 5, timeoutFloat: 30}
		_, errMsg := prepareSettingsSubmit(st, baseState, true)
		if errMsg == "" || errMsg == "skip" {
			t.Fatalf("expected validation error message, got %q", errMsg)
		}
	})

	t.Run("saveBlockedWithoutPingWhenConnectionChanged", func(t *testing.T) {
		st := &settingsState{
			editingProfileID: "p1",
			config:           apitypes.Settings{BaseURL: "https://api.openai.com/v1", Model: "other-model", MaxTools: 5, TimeoutSeconds: 30},
			maxToolsFloat:    5,
			timeoutFloat:     30,
		}
		if settingsCanSave(st, baseState) {
			t.Fatal("expected settingsCanSave=false without ping")
		}
		_, errMsg := prepareSettingsSubmit(st, baseState, false)
		if errMsg != "skip" {
			t.Fatalf("expected skip, got %q", errMsg)
		}
	})

	t.Run("maxToolsOnlyChangeAllowedWithoutPing", func(t *testing.T) {
		st := &settingsState{
			editingProfileID: "p1",
			config:           apitypes.Settings{BaseURL: "https://api.openai.com/v1", Model: "gpt-6-luna", MaxTools: 8, TimeoutSeconds: 30},
			maxToolsFloat:    8,
			timeoutFloat:     30,
		}
		plan, errMsg := prepareSettingsSubmit(st, baseState, false)
		if errMsg != "" {
			t.Fatalf("unexpected errMsg: %q", errMsg)
		}
		if plan.needsPingBeforeSave || plan.mode != "save" || plan.cfg.MaxTools != 8 {
			t.Fatalf("unexpected plan: %+v", plan)
		}
	})

	t.Run("runTestClampsLimitsAndPreservesPingKey", func(t *testing.T) {
		st := &settingsState{
			editingProfileID: "p1",
			config:           apitypes.Settings{BaseURL: " https://api.openai.com/v1 ", Model: " gpt-6-luna "},
			maxToolsFloat:    99,
			timeoutFloat:     1,
			testIntent:       "search issues",
		}
		plan, errMsg := prepareSettingsSubmit(st, baseState, true)
		if errMsg != "" {
			t.Fatalf("unexpected errMsg: %q", errMsg)
		}
		if plan.mode != "test" || plan.cfg.MaxTools != 12 || plan.cfg.TimeoutSeconds != 5 || !plan.needsPingBeforeSave {
			t.Fatalf("unexpected clamped test plan: %+v", plan)
		}

		finishSettingsSaved(st, plan, settingsSavedResult{
			pingRes:    &apitypes.PingModelResult{Model: "gpt-6-luna", DurationMS: 42},
			pingPassed: true,
			savedID:    "p1",
		})
		if st.busyMode != "" || st.editingProfileID != "p1" || st.pingPassedKey == "" {
			t.Fatalf("unexpected state after finishSettingsSaved: %+v", st)
		}
	})
}

func TestSettingsTrustModeCard(t *testing.T) {
	cases := []struct {
		name   string
		access apitypes.AccessInfo
		label  string
		trust  string
	}{
		{"local", apitypes.AccessInfo{SecurityMode: apitypes.SecurityModeLocalGuardrail}, "Local Guardrail", "trust = declared"},
		{"shared", apitypes.AccessInfo{Shared: true, SecurityMode: apitypes.SecurityModeSharedBoundary, Actor: "alice"}, "Shared Boundary", "trust = authenticated"},
		// Older backends omit security_mode: infer from the access shape.
		{"legacyShared", apitypes.AccessInfo{Shared: true, Actor: "alice"}, "Shared Boundary", "trust = authenticated"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			i18n.WithLang(i18n.LangEN, func() {
				boot := apitypes.Bootstrap{Access: tc.access}
				u := NewDesktopUI(&gatewayRuntime{UIURL: "http://127.0.0.1:1", Mode: modeAttached})
				u.State = &boot
				u.Loading = false
				tester := ui.NewTester(func(c *ui.Context) {
					defer u.enterHeadlessFrame()()
					u.renderSettings(c)
				}, 1400, 1600)
				if !tester.HasText("Runtime trust mode") || !tester.HasText(tc.label) || !tester.HasText(tc.trust) {
					t.Fatalf("trust card missing %q / %q: %v", tc.label, tc.trust, tester.Texts())
				}
			})
		})
	}
}
