package main

import (
	"encoding/json"
	"strings"
	"testing"
)

func TestLogCommandsRejectUnconfiguredTargets(t *testing.T) {
	services := serviceFlags{}
	if err := services.Set("backend=harness-backend-1"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{"backend=x;id", "backend=$(id)", "-x=container", "new=--help", "backend=duplicate"} {
		if err := services.Set(bad); err == nil {
			t.Fatalf("accepted %q", bad)
		}
	}
	for _, bad := range []logArgs{{Service: "backend;id"}, {Service: "unknown"}, {Service: "backend", Minutes: -1}, {Service: "backend", Limit: 501}, {Service: "backend", Query: strings.Repeat("x", 257)}} {
		if err := bad.validate(services); err == nil {
			t.Fatalf("accepted %+v", bad)
		}
	}
	a := logArgs{Service: "backend", Query: "'; reboot; #"}
	if err := a.validate(services); err != nil {
		t.Fatal(err)
	}
	command := remoteCommand(services[a.Service], a)
	if command != "docker logs --timestamps --since 15m --tail 100 harness-backend-1" {
		t.Fatal(command)
	}
	if strings.Contains(command, a.Query) {
		t.Fatal("search text reached remote shell")
	}
}

func TestLogsRedactStructuredAndTextSecrets(t *testing.T) {
	raw := "2026-10-30T08:00:00.000Z " + `{"message":"error for user@example.com","nested":{"access_token":"hidden-token"},"headers":{"Authorization":"Bearer hidden-bearer"}}` + "\n" +
		"2026-10-30T08:00:01Z ERROR password=hidden-password token=hidden-query\n" +
		"2026-10-30T08:00:02Z healthy\n"
	a := logArgs{Service: "backend", Minutes: 15, Limit: 100, Query: "ERROR"}
	b, err := json.Marshal(logResult(raw, a, false))
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"user@example.com", "hidden-token", "hidden-bearer", "hidden-password", "hidden-query"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("secret leaked: %s", secret)
		}
	}
	if got := logResult(raw, a, false)["count"]; got != 2 {
		t.Fatalf("keyword filter count: %v", got)
	}
}

func TestBoundedLogsOmitTruncatedLine(t *testing.T) {
	var b boundedBuffer
	raw := "2026-10-30T08:00:00Z safe\n" + strings.Repeat("x", maxLogBytes)
	n, err := b.Write([]byte(raw))
	if err != nil || n != len(raw) || b.Len() != maxLogBytes || !b.truncated {
		t.Fatalf("buffer limit failed: %d %v", n, err)
	}
	r := logResult(b.String(), logArgs{Service: "backend"}, b.truncated)
	if r["count"] != 1 || r["truncated"] != true {
		t.Fatalf("partial line returned: %+v", r)
	}
}
