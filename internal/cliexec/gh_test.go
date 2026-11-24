package cliexec

import (
	"errors"
	"slices"
	"strings"
	"testing"

	"gatewaylab/internal/fault"
)

func assertCLIError(t *testing.T, err error, code string) {
	t.Helper()
	var f *fault.Error
	if !errors.As(err, &f) || f.Code != code {
		t.Fatalf("got %v, want error %s", err, code)
	}
}

func TestParseGHArgsRedTeam(t *testing.T) {
	for _, attack := range []string{
		"auth token",
		"auth status",
		"auth status --show-token",
		"config get",
		"extension install",
		"alias set",
		"api /user --jq env",
		"api graphql",
		"pr view 1 --hostname evil.com",
		"pr view 1 -R evil.com/o/r",
		"pr view 1 --repo=evil.com/o/r",
		"pr view 1 -R https://evil.com/o/r",
		"pr view 1 -R -x/y",
		"pr view 1 -R x/.",
		"pr view 1 -R x/..",
		"pr view 1 -R x/y -- --hostname evil.com",
		"pr view 1 -- --hostname evil.com",
		"pr create --body-file /etc/passwd",
		"issue comment 1 -R x/y -F /etc/passwd",
		"issue comment 1 -R x/y --body-file=/etc/passwd",
		"pr merge 1 -R x/y --squash --admin",
		"pr merge 1 --admin",
		"pr merge 1 -R x/y --squash --merge",
		"pr view 1 -R x/y --jq env",
		"pr view 1 -R x/y --template={{env}}",
		"pr view 1 -R x/y --json=comments",
		"pr view 1 -R x/y --web",
		"pr view 1 -R x/y --input=-",
		"pr view +1 -R x/y",
		"pr view 01 -R x/y",
		"pr view 0 -R x/y",
		"pr view 2147483648 -R x/y",
		"pr view 1",
		"pr view 1 2 -R x/y",
		"pr view 1 -R x/y --repo=a/b",
		"pr list -R x/y --limit=51",
		"issue list -R x/y --state=merged",
		"repo view -x/y",
		"repo view https://evil.com/o/r",
	} {
		t.Run(attack, func(t *testing.T) {
			id, args, err := ParseGHArgs("gh", strings.Fields(attack))
			assertCLIError(t, err, "cli_command_denied")
			if id != "" || args != nil {
				t.Fatal("denied command produced typed arguments")
			}
		})
	}
	for _, body := range []string{"", "nul\x00byte", strings.Repeat("é", maxBody/2+1)} {
		_, _, err := ParseGHArgs("gh", []string{"issue", "comment", "1", "-R=x/y", "--body=" + body})
		assertCLIError(t, err, "cli_command_denied")
	}
}

func TestRenderGHArgsTemplates(t *testing.T) {
	for _, tc := range []struct {
		action, wanted string
		raw            []string
	}{
		{"pr.list", "pr list -R=x/y --json=number,title,state,url --state=merged --limit=50", strings.Fields("pr list --repo x/y --state merged -L 50")},
		{"pr.view", "pr view -R=x/y --json=number,title,body,state,url -- 2147483647", strings.Fields("pr view 2147483647 -R x/y")},
		{"issue.list", "issue list -R=x/y --json=number,title,state,url --state=open --limit=30", strings.Fields("issue list -R=x/y")},
		{"issue.view", "issue view -R=x/y --json=number,title,body,state,url -- 1", strings.Fields("issue view 1 -R=x/y")},
		{"repo.view", "repo view --json=nameWithOwner,description,url,isPrivate -- x/y", strings.Fields("repo view x/y")},
		{"issue.comment", "issue comment -R=x/y --body=--admin -- 1", strings.Fields("issue comment 1 -R=x/y --body=--admin")},
		{"pr.comment", "pr comment -R=x/y --body=$(id) -- 2", strings.Fields("pr comment 2 -R=x/y --body=$(id)")},
		{"pr.merge", "pr merge -R=x/y --rebase=true -- 1", strings.Fields("pr merge -R=x/y 1 --rebase")},
	} {
		t.Run(tc.action, func(t *testing.T) {
			id, args, err := ParseGHArgs("svc.example", tc.raw)
			if err != nil || id != "svc.example."+tc.action {
				t.Fatalf("parse: %q %v", id, err)
			}
			got, err := RenderGHArgs(tc.action, args)
			if err != nil || !slices.Equal(got, strings.Fields(tc.wanted)) {
				t.Fatalf("render: %q %v, want %q", got, err, tc.wanted)
			}
		})
	}
	for _, args := range []map[string]any{
		{"repo": "bad.host/x/y", "number": 1},
		{"repo": "x/y", "number": 0},
		{"repo": "x/y", "number": "1"},
		{"repo": "x/y", "number": 1.5},
		{"repo": "x/y", "number": 1, "hostname": "example.invalid"},
	} {
		_, err := RenderGHArgs("pr.view", args)
		assertCLIError(t, err, "cli_command_denied")
	}
}

func BenchmarkParseGHArgs(b *testing.B) {
	rawArgv := []string{"pr", "list", "-R", "owner/repository", "--state=merged", "--limit=50"}
	b.ReportAllocs()
	for b.Loop() {
		if _, _, err := ParseGHArgs("gh", rawArgv); err != nil {
			b.Fatal(err)
		}
	}
}
