package maintenance

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"time"

	"gatewaylab/internal/contracttest"
	"gatewaylab/internal/jsonutil"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/store"
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
		if c.Name == "" || names[c.Name] || strings.TrimSpace(c.Intent) == "" {
			return invalid("路由用例需要唯一名称和 intent")
		}
		names[c.Name] = true
		if !c.NoMatch && len(c.Expected) == 0 {
			return invalid("路由用例需要 expected 工具或 no_match")
		}
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if !slices.ContainsFunc(m.catalog.Snapshot().Servers, func(server Server) bool { return server.ID == s.ServerID }) {
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
	if err := store.MaintenanceAcceptance[json.RawMessage](m.db).Put(s.ServerID, b); err != nil {
		return fmt.Errorf("save acceptance suite: %w", err)
	}
	m.suites[s.ServerID] = s
	return nil
}
func (m *Maintainer) EvaluateAcceptance(ctx context.Context, r MaintenanceRun, dir string) AcceptanceReport {
	report := AcceptanceReport{Results: []AcceptanceResult{}, MissingCoverage: []string{}, RoutingFingerprint: m.router.RoutingFingerprint(r.ServerID)}
	if r.AcceptanceInput == nil {
		return report
	}
	suite := *r.AcceptanceInput
	report.Configured = true
	report.Passed = true
	report.Fingerprint = jsonutil.MustDigest(suite)
	covered := map[string]bool{}
	routed := map[string]bool{}
	for _, c := range suite.Contracts {
		start := time.Now()
		err := m.checkContract(ctx, r, dir, c)
		result := AcceptanceResult{Name: c.Name, Kind: "contract", Passed: err == nil, DurationMS: time.Since(start).Milliseconds()}
		if err != nil {
			result.Error = m.recorder.ErrorDetail(err)
			report.Passed = false
		} else if !c.ExpectError {
			covered[c.ToolID] = true
		}
		report.Results = append(report.Results, result)
	}
	registry := m.catalog.Snapshot()
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
		result, err := m.router.Discover(ctx, c, candidate)
		if err == nil {
			if c.NoMatch && result.Status != "no_match" {
				err = fmt.Errorf("expected no_match, got %s", result.Status)
			}
			for _, id := range c.Expected {
				if !slices.ContainsFunc(result.Tools, func(t string) bool { return t == id }) {
					err = fmt.Errorf("expected tool %s was not selected", id)
				}
			}
			for _, id := range c.Forbidden {
				if slices.ContainsFunc(result.Tools, func(t string) bool { return t == id }) {
					err = fmt.Errorf("forbidden tool %s was selected", id)
				}
			}
		}
		outcome := AcceptanceResult{Name: c.Name, Kind: "routing", Passed: err == nil, DurationMS: time.Since(start).Milliseconds()}
		if err != nil {
			outcome.Error = m.recorder.ErrorDetail(err)
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
	if report.RoutingFingerprint != m.router.RoutingFingerprint(r.ServerID) {
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
		mock, calls, err := contracttest.MCP(tool.Name, tool.InputSchema, c.MCPResult, c.MCPError)
		if err != nil {
			return err
		}

		defer mock.Close()
		server.Endpoint = mock.URL
		server.Transport = "http"
		result, callErr = m.upstream.CallMCP(ctx, server, tool, arguments)
		if calls() != 1 {
			return fmt.Errorf("expected one native tool invocation, got %d", calls())
		}
	} else {
		var mu sync.Mutex
		index := 0
		failures := []string{}
		mock := contracttest.HTTP(&mu, func(req contracttest.Request) contracttest.Response {
			if index >= len(c.Exchanges) {
				failures = append(failures, "unexpected extra upstream request")
				return contracttest.Response{Status: 500}
			}
			expected := c.Exchanges[index]
			index++
			if req.Method != expected.Method || req.Path != expected.Path {
				failures = append(failures, fmt.Sprintf("request %d: expected %s %s, got %s %s", index, expected.Method, expected.Path, req.Method, req.Path))
			}
			query := expected.Query
			if query == nil {
				query = map[string][]string{}
			}
			if jsonutil.MustDigest(req.Query) != jsonutil.MustDigest(query) {
				failures = append(failures, fmt.Sprintf("request %d: query mismatch", index))
			}
			for key, value := range expected.Headers {
				if req.Header(key) != value {
					failures = append(failures, "request header mismatch: "+key)
				}
			}
			b, err := req.Body()
			if err != nil {
				failures = append(failures, "request body too large")
			} else if len(expected.Body) > 0 {
				if !equalJSON(b, expected.Body) {
					failures = append(failures, "request body mismatch")
				}
			} else if len(b) > 0 {
				failures = append(failures, "unexpected request body")
			}
			return contracttest.Response{Status: expected.Status, Headers: expected.ResponseHeaders, Body: expected.Response, Text: expected.Text}
		}, func() { failures = append(failures, "write fixture response failed") })

		defer mock.Close()
		server.Endpoint = mock.URL
		if tool.Adapter != "" {
			result, callErr = sandbox.CallAdapterFileWithLookup(ctx, m.execution, server, tool, arguments, filepath.Join(dir, tool.Adapter), m.lookupEnv)
		} else {
			result, callErr = m.upstream.CallHTTP(ctx, server, tool, arguments)
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
	result = m.upstream.RedactResponse(result, server)
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
	return jsonutil.MustDigest(x) == jsonutil.MustDigest(y)
}
