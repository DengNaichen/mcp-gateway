package gateway

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

// Acceptance suites are owner-authored, persisted separately, and NEVER included
// in the coding agent's prompt or job files. The runner owns assertions/mocks.
type AcceptanceSuite struct {
	ServerID  string         `json:"server_id"`
	Contracts []ContractCase `json:"contracts"`
	Routing   []RoutingCase  `json:"routing"`
}
type ContractCase struct {
	Name        string          `json:"name"`
	ToolID      string          `json:"tool_id"`
	Arguments   map[string]any  `json:"arguments"`
	Exchanges   []HTTPExchange  `json:"exchanges,omitempty"`
	Expected    json.RawMessage `json:"expected,omitempty"`
	ExpectError bool            `json:"expect_error,omitempty"`
	MCPResult   json.RawMessage `json:"mcp_result,omitempty"`
	MCPError    bool            `json:"mcp_error,omitempty"`
}
type HTTPExchange struct {
	Method          string              `json:"method"`
	Path            string              `json:"path"`
	Query           map[string][]string `json:"query,omitempty"`
	Headers         map[string]string   `json:"headers,omitempty"`
	Body            json.RawMessage     `json:"body,omitempty"`
	Status          int                 `json:"status"`
	ResponseHeaders map[string]string   `json:"response_headers,omitempty"`
	Response        json.RawMessage     `json:"response,omitempty"`
	Text            *string             `json:"text,omitempty"`
}
type RoutingCase struct {
	Name      string   `json:"name"`
	Intent    string   `json:"intent"`
	Context   string   `json:"context,omitempty"`
	Actor     string   `json:"actor,omitempty"`
	Mode      string   `json:"mode"`
	Expected  []string `json:"expected"`
	Forbidden []string `json:"forbidden,omitempty"`
	NoMatch   bool     `json:"no_match,omitempty"`
}
type AcceptanceResult struct {
	Name       string `json:"name"`
	Kind       string `json:"kind"`
	Passed     bool   `json:"passed"`
	Error      string `json:"error,omitempty"`
	DurationMS int64  `json:"duration_ms"`
}
type AcceptanceReport struct {
	Configured         bool               `json:"configured"`
	Passed             bool               `json:"passed"`
	Fingerprint        string             `json:"fingerprint,omitempty"`
	RoutingFingerprint string             `json:"routing_fingerprint,omitempty"`
	Results            []AcceptanceResult `json:"results"`
	MissingCoverage    []string           `json:"missing_coverage"`
}

func (m *Maintainer) SaveAcceptance(s AcceptanceSuite) error {
	if len(s.Contracts) > 200 || len(s.Routing) > 200 || len(s.Routing) == 0 {
		return invalid("验收需要至少一条路由用例，每类最多 200 条")
	}
	names := map[string]bool{}
	for _, c := range s.Contracts {
		if c.Name == "" || names[c.Name] || !strings.HasPrefix(c.ToolID, s.ServerID+".") {
			return invalid("契约用例名称重复或工具归属无效")
		}
		names[c.Name] = true
		if !c.ExpectError && len(c.Expected) == 0 {
			return invalid("成功用例需要独立的 expected 结果")
		}
		if len(c.Exchanges) > 20 {
			return invalid("单条用例最多 20 个请求")
		}
		for _, x := range c.Exchanges {
			if x.Method == "" || !strings.HasPrefix(x.Path, "/") || x.Status < 100 || x.Status > 599 {
				return invalid("HTTP 验收请求 / 响应定义无效")
			}
		}
	}
	for _, c := range s.Routing {
		if c.Name == "" || names[c.Name] || strings.TrimSpace(c.Intent) == "" || !slices.Contains([]string{"retrieval", "model"}, c.Mode) {
			return invalid("路由用例需要唯一名称、intent 和 retrieval/model 模式")
		}
		names[c.Name] = true
		if !c.NoMatch && len(c.Expected) == 0 {
			return invalid("路由用例需要 expected 工具或 no_match")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.ContainsFunc(m.g.Snapshot().Servers, func(server Server) bool { return server.ID == s.ServerID }) {
		return invalid("服务不存在")
	}
	b, err := json.Marshal(s)
	if err != nil {
		return fmt.Errorf("encode acceptance suite: %w", err)
	}
	if len(b) > 1<<20 {
		return invalid("验收用例合计不能超过 1 MB")
	}
	// Normalize map/slice/number types and sever caller-owned mutable references.
	if err := json.Unmarshal(b, &s); err != nil {
		return fmt.Errorf("normalize acceptance suite: %w", err)
	}
	if _, err := m.g.db.Exec("INSERT INTO maintenance_acceptance(id,definition) VALUES(?,?) ON CONFLICT(id) DO UPDATE SET definition=excluded.definition", s.ServerID, string(b)); err != nil {
		return fmt.Errorf("save acceptance suite: %w", err)
	}
	m.suites[s.ServerID] = s
	return nil
}
func (g *Gateway) acceptanceRoutingFingerprint(serverID string) string {
	g.mu.RLock()
	defer g.mu.RUnlock()
	return g.routingFingerprintLocked(serverID)
}
func (g *Gateway) routingFingerprintLocked(serverID string) string {
	other := Registry{Servers: []Server{}, Tools: []Tool{}}
	for _, server := range g.registry.Servers {
		if server.ID != serverID {
			other.Servers = append(other.Servers, server)
		}
	}
	for _, tool := range g.registry.Tools {
		if tool.ServerID != serverID {
			other.Tools = append(other.Tools, tool)
		}
	}
	var principals any
	if g.access != nil {
		principals = g.access.principals
	}
	return maintenanceDigest([]any{g.settings, g.apiKey, other, principals, g.execution})
}
func (m *Maintainer) evaluateAcceptance(ctx context.Context, r MaintenanceRun, dir string) AcceptanceReport {
	report := AcceptanceReport{Results: []AcceptanceResult{}, MissingCoverage: []string{}, RoutingFingerprint: m.g.acceptanceRoutingFingerprint(r.ServerID)}
	if r.AcceptanceInput == nil {
		return report
	}
	suite := *r.AcceptanceInput
	report.Configured = true
	report.Passed = true
	report.Fingerprint = maintenanceDigest(suite)
	covered := map[string]bool{}
	routed := map[string]bool{}
	for _, c := range suite.Contracts {
		start := time.Now()
		err := m.checkContract(ctx, r, dir, c)
		result := AcceptanceResult{Name: c.Name, Kind: "contract", Passed: err == nil, DurationMS: time.Since(start).Milliseconds()}
		if err != nil {
			result.Error = m.g.errorDetail(err)
			report.Passed = false
		} else if !c.ExpectError {
			covered[c.ToolID] = true
		}
		report.Results = append(report.Results, result)
	}
	registry := m.g.Snapshot()
	candidate := Registry{Servers: registry.Servers, Tools: []Tool{}}
	for _, tool := range registry.Tools {
		if tool.ServerID != r.ServerID {
			candidate.Tools = append(candidate.Tools, tool)
		}
	}
	for _, tool := range r.Tools {
		// Draft new tools are tested as if the owner approved them; the active
		// registry remains untouched and discovery/invocation cannot see drafts.
		if !slices.ContainsFunc(r.Before, func(old Tool) bool { return old.ID == tool.ID }) {
			tool.Enabled = true
		}
		candidate.Tools = append(candidate.Tools, tool)
	}
	for _, c := range suite.Routing {
		start := time.Now()
		routingCtx, actorErr := m.g.acceptanceActor(ctx, c.Actor)
		var result DiscoverResult
		err := actorErr
		if err == nil {
			result, err = m.g.discoverRegistry(routingCtx, DiscoverRequest{Intent: c.Intent, Context: c.Context, Actor: c.Actor, Mode: c.Mode}, candidate, false)
		}
		if err == nil {
			if c.NoMatch && result.Status != "no_match" {
				err = fmt.Errorf("expected no_match, got %s", result.Status)
			}
			for _, id := range c.Expected {
				if !slices.ContainsFunc(result.Tools, func(t Selection) bool { return t.ID == id }) {
					err = fmt.Errorf("expected tool %s was not selected", id)
				}
			}
			for _, id := range c.Forbidden {
				if slices.ContainsFunc(result.Tools, func(t Selection) bool { return t.ID == id }) {
					err = fmt.Errorf("forbidden tool %s was selected", id)
				}
			}
		}
		outcome := AcceptanceResult{Name: c.Name, Kind: "routing", Passed: err == nil, DurationMS: time.Since(start).Milliseconds()}
		if err != nil {
			outcome.Error = m.g.errorDetail(err)
			report.Passed = false
		} else {
			for _, id := range c.Expected {
				routed[id] = true
			}
		}
		report.Results = append(report.Results, outcome)
	}
	for _, tool := range r.Tools {
		if !covered[tool.ID] {
			report.MissingCoverage = append(report.MissingCoverage, tool.ID+":contract")
		}
		if tool.Enabled || !slices.ContainsFunc(r.Before, func(old Tool) bool { return old.ID == tool.ID }) {
			if !routed[tool.ID] {
				report.MissingCoverage = append(report.MissingCoverage, tool.ID+":routing")
			}
		}
	}
	if len(report.MissingCoverage) > 0 {
		report.Passed = false
	}
	if report.RoutingFingerprint != m.g.acceptanceRoutingFingerprint(r.ServerID) {
		report.Passed = false
		report.Results = append(report.Results, AcceptanceResult{Name: "routing-snapshot", Kind: "routing", Error: "路由配置或其他工具在验收期间变化，需要重新运行"})
	}
	return report
}
func (m *Maintainer) checkContract(parent context.Context, r MaintenanceRun, dir string, c ContractCase) error {
	i := slices.IndexFunc(r.Tools, func(t Tool) bool { return t.ID == c.ToolID })
	if i < 0 {
		return fmt.Errorf("acceptance tool %s no longer exists", c.ToolID)
	}
	tool := r.Tools[i]
	schema, err := compileSchema(tool.InputSchema)
	if err != nil {
		return err
	}
	arguments := c.Arguments
	if arguments == nil {
		arguments = map[string]any{}
	}
	if err := schema.Validate(arguments); err != nil {
		if c.ExpectError && len(c.Exchanges) == 0 {
			return nil
		}
		return fmt.Errorf("acceptance arguments violate input schema: %w", err)
	}
	ctx, cancel := context.WithTimeout(parent, 20*time.Second)
	defer cancel()
	server := r.Server
	// Fixtures use synthetic credentials. Live provider secrets never enter mocks.
	server.Headers = map[string]string{}
	server.Env = map[string]string{}
	for key := range r.Server.Headers {
		server.Headers[key] = "acceptance-test"
	}
	for key := range r.Server.Env {
		server.Env[key] = "acceptance-test"
	}
	var result any
	var callErr error
	if server.Kind == "mcp" {
		native := mcp.NewServer(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
		var fixture any
		if len(c.MCPResult) > 0 {
			if err := json.Unmarshal(c.MCPResult, &fixture); err != nil {
				return fmt.Errorf("decode mcp fixture: %w", err)
			}
		}
		var calls atomic.Int32
		native.AddTool(&mcp.Tool{Name: tool.Name, InputSchema: tool.InputSchema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
			calls.Add(1)
			if c.MCPError {
				return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "fixture upstream error"}}}, nil
			}
			return toolResult(fixture)
		})
		mock := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return native }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
		defer mock.Close()
		server.Endpoint = mock.URL
		server.Transport = "http"
		result, callErr = m.g.callMCP(ctx, server, tool, arguments)
		if calls.Load() != 1 {
			return fmt.Errorf("expected one native tool invocation, got %d", calls.Load())
		}
	} else {
		var mu sync.Mutex
		index := 0
		failures := []string{}
		mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			mu.Lock()
			defer mu.Unlock()
			if index >= len(c.Exchanges) {
				failures = append(failures, "unexpected extra upstream request")
				w.WriteHeader(500)
				return
			}
			expected := c.Exchanges[index]
			index++
			if req.Method != expected.Method || req.URL.EscapedPath() != expected.Path {
				failures = append(failures, fmt.Sprintf("request %d: expected %s %s, got %s %s", index, expected.Method, expected.Path, req.Method, req.URL.EscapedPath()))
			}
			query := expected.Query
			if query == nil {
				query = map[string][]string{}
			}
			if maintenanceDigest(map[string][]string(req.URL.Query())) != maintenanceDigest(query) {
				failures = append(failures, fmt.Sprintf("request %d: query mismatch", index))
			}
			for key, value := range expected.Headers {
				if req.Header.Get(key) != value {
					failures = append(failures, "request header mismatch: "+key)
				}
			}
			b, err := readBounded(req.Body, 1<<20)
			if err != nil {
				failures = append(failures, "request body too large")
			} else if len(expected.Body) > 0 {
				if !equalJSON(b, expected.Body) {
					failures = append(failures, "request body mismatch")
				}
			} else if len(b) > 0 {
				failures = append(failures, "unexpected request body")
			}
			for key, value := range expected.ResponseHeaders {
				w.Header().Set(key, value)
			}
			if expected.Text != nil {
				w.Header().Set("Content-Type", "text/plain")
			} else if len(expected.Response) > 0 {
				w.Header().Set("Content-Type", "application/json")
			}
			w.WriteHeader(expected.Status)
			var writeErr error
			if expected.Text != nil {
				_, writeErr = io.WriteString(w, *expected.Text)
			} else {
				_, writeErr = w.Write(expected.Response)
			}
			if writeErr != nil {
				failures = append(failures, "write fixture response failed")
			}
		}))
		defer mock.Close()
		server.Endpoint = mock.URL
		if tool.Adapter != "" {
			result, callErr = callAdapterFile(ctx, m.g.execution, server, tool, arguments, filepath.Join(dir, tool.Adapter))
		} else {
			result, callErr = callHTTP(ctx, server, tool, arguments)
		}
		mu.Lock()
		count := index
		problems := slices.Clone(failures)
		mu.Unlock()
		if count != len(c.Exchanges) {
			return fmt.Errorf("expected %d upstream requests, got %d", len(c.Exchanges), count)
		}
		if len(problems) > 0 {
			return fmt.Errorf("contract mismatch: %s", strings.Join(problems, "; "))
		}
	}
	if c.ExpectError {
		if callErr == nil {
			return fmt.Errorf("expected upstream failure but call succeeded")
		}
		return nil
	}
	if callErr != nil {
		return callErr
	}
	result = redactResponse(result, server)
	encoded, err := json.Marshal(result)
	if err != nil {
		return fmt.Errorf("encode acceptance result: %w", err)
	}
	if !equalJSON(encoded, c.Expected) {
		return fmt.Errorf("tool result does not match owner-approved expected response")
	}
	return nil
}
func equalJSON(a, b []byte) bool {
	var x, y any
	if json.Unmarshal(a, &x) != nil || json.Unmarshal(b, &y) != nil {
		return false
	}
	return maintenanceDigest(x) == maintenanceDigest(y)
}
