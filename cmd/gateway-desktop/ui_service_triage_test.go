package main

import "testing"

func TestServiceReadinessTriage(t *testing.T) {
	cases := []struct {
		name      string
		r         serviceReadiness
		bucket    serviceTriageBucket
		action    readinessActionKind
		label     string
		probeable bool
	}{
		{"missing creds", serviceReadiness{Kind: readinessMissingCreds}, triageAttention, readinessActionFillCreds, "填写凭据", false},
		{"conn error", serviceReadiness{Kind: readinessConnError}, triageAttention, readinessActionProbe, "重新测试", true},
		{"invoke error", serviceReadiness{Kind: readinessInvokeError}, triageAttention, readinessActionProbe, "重新测试", true},
		{"unverified", serviceReadiness{Kind: readinessUnverified}, triagePending, readinessActionProbe, "测试连接", true},
		{"no open tools with ro", serviceReadiness{Kind: readinessNoOpenTools, AlertAction: readinessActionEnableReadOnly}, triagePending, readinessActionEnableReadOnly, "开放只读", false},
		{"no tools synced", serviceReadiness{Kind: readinessNoOpenTools}, triagePending, readinessActionNone, "", false},
		{"ready", serviceReadiness{Kind: readinessReady}, triageReady, readinessActionNone, "", false},
		{"disabled", serviceReadiness{Kind: readinessDisabled, MissingVars: []string{"X"}}, triageDisabled, readinessActionNone, "", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			if got := tc.r.TriageBucket(); got != tc.bucket {
				t.Fatalf("bucket = %v, want %v", got, tc.bucket)
			}
			action, label := tc.r.RowAction()
			if action != tc.action || label != tc.label {
				t.Fatalf("action = (%v, %q), want (%v, %q)", action, label, tc.action, tc.label)
			}
			if got := tc.r.Probeable(); got != tc.probeable {
				t.Fatalf("probeable = %v, want %v", got, tc.probeable)
			}
		})
	}
}

func TestServicesNavItemPrefersAttentionCount(t *testing.T) {
	if item := servicesNavItem(15, 11); item.badge != "11" || !item.badgeDanger {
		t.Fatalf("attention item = %+v", item)
	}
	if item := servicesNavItem(15, 0); item.badge != "15" || item.badgeDanger {
		t.Fatalf("healthy item = %+v", item)
	}
}
