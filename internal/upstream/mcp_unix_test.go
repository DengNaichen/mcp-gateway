//go:build unix

package upstream

import (
	"context"
	"encoding/json"
	"os"
	"syscall"
	"testing"
)

func TestStdioChildRunsInOwnProcessGroup(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service := New(Config{})
	t.Cleanup(func() { _ = service.Close() })
	server := Server{
		ID:        "counter",
		Kind:      "mcp",
		Transport: "stdio",
		Command:   executable,
		Args:      []string{"-test.run=^TestStdioFixtureProcess$"},
		Env:       map[string]string{"GATEWAY_B2_STDIO_HELPER": "1"},
	}
	raw, err := service.CallMCP(context.Background(), server, Tool{Name: "count"}, map[string]any{})
	if err != nil {
		t.Fatal(err)
	}
	b, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	var res counterResult
	if err := json.Unmarshal(b, &res); err != nil {
		t.Fatal(err)
	}
	pgid, err := syscall.Getpgid(res.PID)
	if err != nil {
		t.Fatalf("getpgid(%d): %v", res.PID, err)
	}
	if pgid != res.PID || pgid == os.Getpid() {
		t.Fatalf("stdio child pgid=%d pid=%d parent=%d; expected isolated process group", pgid, res.PID, os.Getpid())
	}
}
