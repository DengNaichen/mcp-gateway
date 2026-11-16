package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type counterResult struct {
	Count  int64  `json:"count"`
	PID    int    `json:"pid"`
	Marker string `json:"marker"`
}

// Run the real MCP stdio transport in a separate process, without shell fixtures
// or machine-installed runtimes. os.Exit keeps the Go test harness off stdout.
func TestStdioFixtureProcess(t *testing.T) {
	if os.Getenv("GATEWAY_B2_STDIO_HELPER") != "1" {
		t.Skip("subprocess fixture")
	}
	server := mcp.NewServer(&mcp.Implementation{Name: "counter", Version: "1"}, nil)
	var count atomic.Int64
	mcp.AddTool(server, &mcp.Tool{Name: "count", Description: "Count calls"}, func(context.Context, *mcp.CallToolRequest, struct{}) (*mcp.CallToolResult, counterResult, error) {
		return nil, counterResult{Count: count.Add(1), PID: os.Getpid(), Marker: os.Getenv("COUNTER_MARKER")}, nil
	})
	if err := server.Run(context.Background(), &mcp.StdioTransport{}); err != nil {
		os.Exit(2)
	}
	os.Exit(0)
}

func TestStdioSessionReuseReconfigurationAndClose(t *testing.T) {
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	service := New(Config{LookupEnv: func(string) string { return "runtime-secret" }})
	t.Cleanup(func() { _ = service.Close() })
	server := Server{ID: "counter", Kind: "mcp", Transport: "stdio", Command: executable, Args: []string{"-test.run=^TestStdioFixtureProcess$"}, Env: map[string]string{"GATEWAY_B2_STDIO_HELPER": "1", "COUNTER_MARKER": "${MARKER}"}}
	tool := Tool{Name: "count"}
	call := func() counterResult {
		t.Helper()
		raw, err := service.CallMCP(context.Background(), server, tool, map[string]any{})
		if err != nil {
			t.Fatal(err)
		}
		b, err := json.Marshal(raw)
		if err != nil {
			t.Fatal(err)
		}
		var result counterResult
		if err := json.Unmarshal(b, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	first, second := call(), call()
	if first.Count != 1 || second.Count != 2 || first.PID != second.PID || second.Marker != "runtime-secret" {
		t.Fatalf("session not reused or env not expanded: %+v %+v", first, second)
	}
	// Ending one caller's context must not end the pooled process.
	ctx, cancel := context.WithCancel(context.Background())
	_, release, err := service.Session(ctx, server)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	release()
	if next := call(); next.Count != 3 || next.PID != first.PID {
		t.Fatalf("caller cancellation ended session: %+v", next)
	}
	server.Env["COUNTER_MARKER"] = "changed"
	reconfigured := call()
	if reconfigured.Count != 1 || reconfigured.PID == first.PID || reconfigured.Marker != "changed" {
		t.Fatalf("reconfiguration retained stale process: %+v", reconfigured)
	}
	current, release, err := service.Session(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := current.Close(); err != nil {
		t.Fatal(err)
	}
	service.ForgetClosedSession(server.ID, current, mcp.ErrConnectionClosed)
	if restarted := call(); restarted.Count != 1 || restarted.PID == reconfigured.PID {
		t.Fatalf("closed session not discarded: %+v", restarted)
	}
	current, release, err = service.Session(context.Background(), server)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	if _, err := current.CallTool(context.Background(), &mcp.CallToolParams{Name: "count", Arguments: map[string]any{}}); !errors.Is(err, mcp.ErrConnectionClosed) {
		t.Fatalf("Close left pooled transport open: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
}

func TestMCPImportAndResultShapes(t *testing.T) {
	server := mcp.NewServer(&mcp.Implementation{Name: "fixture", Version: "1"}, nil)
	for i, tc := range []struct {
		name    string
		result  *mcp.CallToolResult
		want    string
		wantErr string
	}{
		{"structured", &mcp.CallToolResult{StructuredContent: map[string]any{"answer": 42}}, `{"answer":42}`, ""},
		{"json", &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: `{"answer":42}`}}}, `{"answer":42}`, ""},
		{"text", &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "hello"}}}, `{"text":"hello"}`, ""},
		{"many", &mcp.CallToolResult{Content: []mcp.Content{&mcp.TextContent{Text: "a"}, &mcp.TextContent{Text: "b"}}}, `{"content":[{"type":"text","text":"a"},{"type":"text","text":"b"}]}`, ""},
		{"error", &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "failed"}}}, "", "native mcp tool returned an execution error: failed"},
	} {
		server.AddTool(&mcp.Tool{Name: tc.name, Description: "Fixture " + strconv.Itoa(i), InputSchema: map[string]any{"type": "object"}, Annotations: &mcp.ToolAnnotations{ReadOnlyHint: true}}, func(context.Context, *mcp.CallToolRequest) (*mcp.CallToolResult, error) { return tc.result, nil })
	}
	host := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return server }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	defer host.Close()
	service := New(Config{})
	defer service.Close()
	source := Server{ID: "remote", Kind: "mcp", Endpoint: host.URL}
	imported, err := service.ImportTools(context.Background(), source)
	if err != nil {
		t.Fatal(err)
	}
	if len(imported) != 5 {
		t.Fatalf("imported=%v", imported)
	}
	for _, tool := range imported {
		if tool.Enabled || !tool.ReadOnly || tool.ID != "remote."+tool.Name || tool.ServerID != "remote" {
			t.Fatalf("changed imported definition: %+v", tool)
		}
	}
	var wg sync.WaitGroup
	for _, tool := range imported {
		wg.Add(1)
		go func() {
			defer wg.Done()
			raw, err := service.CallMCP(context.Background(), source, tool, map[string]any{})
			if tool.Name == "error" {
				if err == nil || err.Error() != "native mcp tool returned an execution error: failed" {
					t.Errorf("error=%v", err)
				}
				return
			}
			if err != nil {
				t.Error(err)
				return
			}
			b, err := json.Marshal(raw)
			if err != nil {
				t.Error(err)
				return
			}
			switch tool.Name {
			case "json", "structured":
				if string(b) != `{"answer":42}` {
					t.Errorf("%s=%s", tool.Name, b)
				}
			case "text":
				if string(b) != `{"text":"hello"}` {
					t.Errorf("text=%s", b)
				}
			case "many":
				var content map[string][]any
				if err := json.Unmarshal(b, &content); err != nil || len(content["content"]) != 2 {
					t.Errorf("content=%s err=%v", b, err)
				}
			}
		}()
	}
	wg.Wait()
}

func TestMCPImportErrorsHaveDomainKinds(t *testing.T) {
	service := New(Config{})
	defer service.Close()
	_, err := service.ImportTools(context.Background(), Server{ID: "absent", Transport: "stdio", Command: "/nonexistent/gateway-b2-test"})
	var failure *Error
	if !errors.Is(err, ErrUpstream) || !errors.As(err, &failure) || failure.Message != "连接 MCP 服务失败" {
		t.Fatalf("unexpected error: %v", err)
	}
	if got := fmt.Sprint(failure); got == failure.Message {
		t.Fatal("transport cause lost")
	}
}
