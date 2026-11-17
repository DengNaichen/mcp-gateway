package scope

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"gatewaylab/internal/buildinfo"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/store"
	"gatewaylab/internal/workspace"
)

func TestHookSuccessRequiresVerifiedCurrentInstall(t *testing.T) {
	dir, cwd := t.TempDir(), t.TempDir()
	db, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	key := []byte("01234567890123456789012345678901")
	opts := Options{DB: db.DB, Dir: dir, Key: key, Snapshot: func() registry.Registry { return registry.Registry{} }, AdminCaller: func(context.Context) bool { return true }}
	service, err := New(opts)
	if err != nil {
		t.Fatal(err)
	}
	call := func(agent, id string, valid bool) {
		t.Helper()
		args := map[string]any{"intent": "verify"}
		hash, _ := workspace.CanonicalArgsHash(args, workspace.CurrentContextField())
		now := time.Now()
		jti, _ := workspace.NewJTI()
		tok, err := workspace.SignToken(key, workspace.Claims{InstallationID: id, Agent: agent, Cwd: cwd, Op: "discover_tools", ArgsHash: hash, JTI: jti, Channel: buildinfo.Current().Channel, IAT: now.Unix(), EXP: now.Add(time.Minute).Unix()})
		if err != nil {
			t.Fatal(err)
		}
		if !valid {
			tok += "invalid"
		}
		args[workspace.CurrentContextField()] = tok
		raw, _ := json.Marshal(args)
		_, _, err = service.ResolveMCPToolCallScope(context.Background(), agent, "discover_tools", raw)
		if valid && err != nil {
			t.Fatal(err)
		}
		if !valid && err == nil {
			t.Fatal("invalid token accepted")
		}
	}
	call("codex", "current", false)
	call("claude", "current", true)
	call("codex", "previous", true)
	if workspace.HookSucceeded(dir, "codex", "current") {
		t.Fatal("unrelated event hid prompt")
	}
	call("codex", "current", true)
	if !workspace.HookSucceeded(dir, "codex", "current") {
		t.Fatal("verified hook not recorded")
	}
	service, err = New(opts)
	if err != nil {
		t.Fatal(err)
	}
	if !workspace.HookSucceeded(dir, "codex", "current") {
		t.Fatal("success lost on restart")
	}
	if workspace.HookSucceeded(t.TempDir(), "codex", "current") {
		t.Fatal("instance isolation failed")
	}
}
