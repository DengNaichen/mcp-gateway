package api

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"sync/atomic"
	"testing"

	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/registry"
)

// Exercises the installed Pi binary, its real tools and RPC events against a
// local deterministic model endpoint. No cloud credentials or model spend.
func TestPiRealRPCContract(t *testing.T) {
	if os.Getenv("GATEWAY_TEST_REAL_PI") != "1" {
		t.Skip("set GATEWAY_TEST_REAL_PI=1 to exercise the installed Pi binary")
	}
	command, err := exec.LookPath("pi")
	if err != nil {
		t.Fatal(err)
	}
	var policy *sandbox.Policy
	if os.Getenv("GATEWAY_TEST_DOCKER") == "1" {
		policy = &sandbox.Policy{Mode: "docker", Image: "node@sha256:4b150b48b4116e2a22f2c39cc27025fbf4f153e2078fd3b14d159fdb5c7279da"}
	}
	g, _ := testGatewayOptions(t, command, policy)
	m := g.Maintenance()
	setLogsAcceptance(t, g, "v1", "real-pi")
	agentDir := t.TempDir()
	t.Setenv("PI_CODING_AGENT_DIR", agentDir)
	t.Setenv("PI_TELEMETRY", "0")
	manifest := struct {
		Summary string          `json:"summary"`
		Tools   []registry.Tool `json:"tools"`
	}{Summary: "真实 Pi 工具写入并通过本地模型契约检查", Tools: []registry.Tool{{ID: "service.logs", ServerID: "service", Name: "logs", Description: "Search logs", Tags: []string{"logs"}, InputSchema: objectSchema(map[string]any{}), Method: "GET", Path: "/logs", ReadOnly: true}}}
	manifestJSON := string(mustJSON(t, manifest))
	args := string(mustJSON(t, map[string]string{"path": "output/manifest.json", "content": manifestJSON}))
	var requests atomic.Int32
	modelServer := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Errorf("unexpected model endpoint: %s", r.URL.Path)
			w.WriteHeader(404)
			return
		}
		var req struct {
			Messages []struct {
				Role string `json:"role"`
			} `json:"messages"`
			Tools []struct {
				Function struct {
					Name string `json:"name"`
				} `json:"function"`
			} `json:"tools"`
		}
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			t.Error(err)
			w.WriteHeader(400)
			return
		}
		requests.Add(1)
		hasWrite := false
		done := false
		for _, tool := range req.Tools {
			if tool.Function.Name == "write" {
				hasWrite = true
			}
		}
		if !hasWrite {
			t.Error("Pi did not expose write tool")
		}
		for _, message := range req.Messages {
			if message.Role == "tool" {
				done = true
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		emit := func(delta any, finish any) {
			b := mustJSON(t, map[string]any{"id": "chatcmpl-test", "object": "chat.completion.chunk", "created": 1, "model": "fixture", "choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
			if _, err := fmt.Fprintf(w, "data: %s\n\n", b); err != nil {
				t.Error(err)
			}
		}
		if !done {
			emit(map[string]any{"role": "assistant", "tool_calls": []any{map[string]any{"index": 0, "id": "write-manifest", "type": "function", "function": map[string]string{"name": "write", "arguments": args}}}}, nil)
			emit(map[string]any{}, "tool_calls")
		} else {
			emit(map[string]any{"role": "assistant", "content": "Completed the maintenance artifact."}, nil)
			emit(map[string]any{}, "stop")
		}
		if _, err := fmt.Fprint(w, "data: [DONE]\n\n"); err != nil {
			t.Error(err)
		}
	}))
	defer modelServer.Close()
	models := map[string]any{"providers": map[string]any{"gateway-test": map[string]any{"baseUrl": modelServer.URL + "/v1", "api": "openai-completions", "apiKey": "local-test-only", "models": []any{map[string]any{"id": "fixture", "name": "Fixture", "reasoning": false, "input": []string{"text"}, "contextWindow": 128000, "maxTokens": 4096}}}}}
	if err := writeJSON(filepath.Join(agentDir, "models.json"), models); err != nil {
		t.Fatal(err)
	}
	source, _ := m.Source("service")
	source.Provider = "gateway-test"
	source.Model = "fixture"
	if err := m.SaveSource(source); err != nil {
		t.Fatal(err)
	}
	g.StartMaintenance(context.Background())
	run := approveRun(t, m, enqueueTest(t, m, true).ID)
	if requests.Load() < 2 || len(run.Tools) != 1 {
		t.Fatalf("real Pi did not complete tool loop: requests=%d run=%+v", requests.Load(), run)
	}
}
