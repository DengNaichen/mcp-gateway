package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/openapi"
)

// ErrUnauthorized indicates the shared gateway rejected a request with HTTP 401.
var ErrUnauthorized = errors.New("需要登录授权")

// APIError represents an HTTP error payload returned by the gateway backend.
type APIError struct {
	Status  int
	Message string
	Detail  string
}

func (e *APIError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return fmt.Sprintf("HTTP %d", e.Status)
}

// IsUnauthorized reports whether err is or wraps ErrUnauthorized or HTTP 401.
func IsUnauthorized(err error) bool {
	if errors.Is(err, ErrUnauthorized) {
		return true
	}
	var apiErr *APIError
	return errors.As(err, &apiErr) && apiErr.Status == http.StatusUnauthorized
}

// APIClient is a strongly-typed HTTP client for the local or shared gateway control plane.
type APIClient struct {
	baseURL    string
	httpClient *http.Client
}

// NewAPIClient creates an APIClient targeting baseURL with an in-memory cookie jar
// so shared-mode /api/auth/login sessions persist across requests.
func NewAPIClient(baseURL string) *APIClient {
	jar, _ := cookiejar.New(nil)
	return &APIClient{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: 190 * time.Second,
			Transport: &http.Transport{
				Proxy: nil,
			},
		},
	}
}

func (c *APIClient) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api"+path, bodyReader)
	if err != nil {
		return fmt.Errorf("build request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized && path != "/auth/login" {
		var errBody apitypes.Error
		_ = json.Unmarshal(respBytes, &errBody)
		msg := errBody.Error
		if msg == "" {
			msg = ErrUnauthorized.Error()
		}
		return fmt.Errorf("%w: %w", ErrUnauthorized, &APIError{
			Status:  resp.StatusCode,
			Message: msg,
			Detail:  errBody.Detail,
		})
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		var errBody apitypes.Error
		_ = json.Unmarshal(respBytes, &errBody)
		msg := errBody.Error
		if msg == "" {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
		return &APIError{
			Status:  resp.StatusCode,
			Message: msg,
			Detail:  errBody.Detail,
		}
	}

	if out != nil && len(bytes.TrimSpace(respBytes)) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *APIClient) AuthMe(ctx context.Context) (apitypes.AuthMe, error) {
	var out apitypes.AuthMe
	err := c.doJSON(ctx, http.MethodGet, "/auth/me", nil, &out)
	return out, err
}

func (c *APIClient) Login(ctx context.Context, token string) (apitypes.Principal, error) {
	var resp apitypes.LoginResult
	err := c.doJSON(ctx, http.MethodPost, "/auth/login", apitypes.LoginRequest{Token: token}, &resp)
	return resp.Principal, err
}

func (c *APIClient) Logout(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/auth/logout", map[string]any{}, nil)
}

func (c *APIClient) Bootstrap(ctx context.Context) (apitypes.Bootstrap, error) {
	var out apitypes.Bootstrap
	err := c.doJSON(ctx, http.MethodGet, "/bootstrap", nil, &out)
	return out, err
}

func (c *APIClient) Events(ctx context.Context) ([]apitypes.Event, error) {
	var out []apitypes.Event
	err := c.doJSON(ctx, http.MethodGet, "/events", nil, &out)
	return out, err
}

func (c *APIClient) SetupStatus(ctx context.Context) (apitypes.SetupStatus, error) {
	var out apitypes.SetupStatus
	err := c.doJSON(ctx, http.MethodGet, "/setup/status", nil, &out)
	return out, err
}

func (c *APIClient) SaveSettings(ctx context.Context, settings apitypes.Settings, apiKey string, clearKey bool) error {
	payload := apitypes.SaveSettingsRequest{Settings: settings, KeyUpdate: apitypes.KeyUpdate{APIKey: apiKey, ClearKey: clearKey}}
	return c.doJSON(ctx, http.MethodPut, "/settings", payload, nil)
}

func (c *APIClient) PingModel(ctx context.Context, settings apitypes.Settings, profileID, apiKey string, clearKey bool) (apitypes.PingModelResult, error) {
	payload := apitypes.PingModelRequest{Settings: settings, ProfileID: profileID, KeyUpdate: apitypes.KeyUpdate{APIKey: apiKey, ClearKey: clearKey}}
	var out apitypes.PingModelResult
	err := c.doJSON(ctx, http.MethodPost, "/model/ping", payload, &out)
	return out, err
}

func (c *APIClient) SaveModelProfile(ctx context.Context, profile apitypes.ModelProfile, apiKey string, clearKey bool) (apitypes.ModelProfile, error) {
	payload := apitypes.SaveModelProfileRequest{ModelProfile: profile, KeyUpdate: apitypes.KeyUpdate{APIKey: apiKey, ClearKey: clearKey}}
	var out apitypes.ModelProfile
	err := c.doJSON(ctx, http.MethodPut, "/model-profiles", payload, &out)
	return out, err
}

func (c *APIClient) ActivateModelProfile(ctx context.Context, id string) (apitypes.ModelProfile, error) {
	var out apitypes.ModelProfile
	err := c.doJSON(ctx, http.MethodPost, "/model-profiles/"+url.PathEscape(id)+"/activate", nil, &out)
	return out, err
}

func (c *APIClient) DeleteModelProfile(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/model-profiles/"+url.PathEscape(id), nil, nil)
}

func (c *APIClient) TestModel(ctx context.Context, settings apitypes.Settings, apiKey string, clearKey bool, intent string) (apitypes.DiscoverResult, error) {
	payload := apitypes.SetupModelRequest{Settings: settings, KeyUpdate: apitypes.KeyUpdate{APIKey: apiKey, ClearKey: clearKey}, Intent: intent}
	var out apitypes.DiscoverResult
	err := c.doJSON(ctx, http.MethodPost, "/setup/model", payload, &out)
	return out, err
}

func (c *APIClient) MarketServers(ctx context.Context, query string) (apitypes.MarketSearchResult, error) {
	path := "/market"
	if strings.TrimSpace(query) != "" {
		path += "?q=" + url.QueryEscape(strings.TrimSpace(query))
	}
	var out apitypes.MarketSearchResult
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *APIClient) InstallMarketServer(ctx context.Context, req apitypes.InstallMarketRequest) (apitypes.InstallMarketResult, error) {
	var out apitypes.InstallMarketResult
	err := c.doJSON(ctx, http.MethodPost, "/market/install", req, &out)
	return out, err
}

func (c *APIClient) MarketIcon(ctx context.Context, id, iconURL string) ([]byte, string, error) {
	q := url.Values{}
	if id != "" {
		q.Set("id", id)
	}
	if iconURL != "" {
		q.Set("url", iconURL)
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, c.baseURL+"/api/market/icon?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		return nil, "", fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return nil, "", err
	}
	return b, resp.Header.Get("Content-Type"), nil
}

func (c *APIClient) TestSource(ctx context.Context, server apitypes.Server) (apitypes.ConnectionCheck, error) {
	var out apitypes.ConnectionCheck
	err := c.doJSON(ctx, http.MethodPost, "/setup/source", server, &out)
	return out, err
}

func (c *APIClient) SaveServer(ctx context.Context, server apitypes.Server) (apitypes.Server, error) {
	var out apitypes.Server
	err := c.doJSON(ctx, http.MethodPut, "/servers", server, &out)
	return out, err
}

func (c *APIClient) SyncServer(ctx context.Context, serverID string) (int, error) {
	var out apitypes.SyncServerResult
	err := c.doJSON(ctx, http.MethodPost, "/servers/"+url.PathEscape(serverID)+"/sync", map[string]any{}, &out)
	return out.Imported, err
}

func (c *APIClient) EnableServerTools(ctx context.Context, serverID string, toolIDs []string) error {
	if toolIDs == nil {
		toolIDs = []string{}
	}
	return c.doJSON(ctx, http.MethodPost, "/servers/"+url.PathEscape(serverID)+"/tools", apitypes.EnableToolsRequest{IDs: toolIDs}, nil)
}

func (c *APIClient) SaveTool(ctx context.Context, tool apitypes.Tool) (apitypes.Tool, error) {
	var out apitypes.Tool
	err := c.doJSON(ctx, http.MethodPut, "/tools", tool, &out)
	return out, err
}

func (c *APIClient) Discover(ctx context.Context, req apitypes.DiscoverRequest) (apitypes.DiscoverResult, error) {
	return c.DiscoverScoped(ctx, req, nil)
}

func (c *APIClient) DiscoverScoped(ctx context.Context, req apitypes.DiscoverRequest, sim *apitypes.SimScope) (apitypes.DiscoverResult, error) {
	req.SimScope = sim
	var out apitypes.DiscoverResult
	err := c.doJSON(ctx, http.MethodPost, "/discover", req, &out)
	return out, err
}

func (c *APIClient) Invoke(ctx context.Context, req apitypes.InvokeRequest) (apitypes.InvokeResult, error) {
	return c.InvokeScoped(ctx, req, nil)
}

func (c *APIClient) InvokeScoped(ctx context.Context, req apitypes.InvokeRequest, sim *apitypes.SimScope) (apitypes.InvokeResult, error) {
	req.SimScope = sim
	var out apitypes.InvokeResult
	err := c.doJSON(ctx, http.MethodPost, "/invoke", req, &out)
	return out, err
}

func (c *APIClient) GetScopes(ctx context.Context) (apitypes.ScopeOverview, error) {
	var out apitypes.ScopeOverview
	err := c.doJSON(ctx, http.MethodGet, "/scopes", nil, &out)
	return out, err
}

func (c *APIClient) SetScopeMode(ctx context.Context, enforce bool) error {
	return c.doJSON(ctx, http.MethodPut, "/scopes/mode", apitypes.ScopeModeRequest{Enforce: enforce}, nil)
}

func (c *APIClient) SaveRepository(ctx context.Context, in apitypes.SaveRepositoryRequest) (apitypes.Repository, error) {
	var out apitypes.Repository
	err := c.doJSON(ctx, http.MethodPut, "/scopes/repositories", in, &out)
	return out, err
}

func (c *APIClient) DeleteRepository(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/scopes/repositories/"+url.PathEscape(id), nil, nil)
}

func (c *APIClient) SaveScopeRule(ctx context.Context, rule apitypes.ScopeRule) (apitypes.ScopeRule, error) {
	if rule.Grants == nil {
		rule.Grants = []apitypes.ServiceGrant{}
	}
	var out apitypes.ScopeRule
	err := c.doJSON(ctx, http.MethodPut, "/scopes/rules", rule, &out)
	return out, err
}

func (c *APIClient) DeleteScopeRule(ctx context.Context, repoKey, agentKey string) error {
	q := url.Values{}
	q.Set("repo_key", repoKey)
	q.Set("agent_key", agentKey)
	return c.doJSON(ctx, http.MethodDelete, "/scopes/rules?"+q.Encode(), nil, nil)
}

func (c *APIClient) PreviewScope(ctx context.Context, repoKey, actor string) (apitypes.ScopePreviewResponse, error) {
	payload := apitypes.ScopePreviewRequest{RepoKey: repoKey, Actor: actor}
	var out apitypes.ScopePreviewResponse
	err := c.doJSON(ctx, http.MethodPost, "/scopes/preview", payload, &out)
	return out, err
}

func (c *APIClient) GetHookSnippet(ctx context.Context, agent string) (apitypes.ScopeSnippetResponse, error) {
	q := url.Values{}
	q.Set("agent", agent)
	var out apitypes.ScopeSnippetResponse
	err := c.doJSON(ctx, http.MethodGet, "/scopes/snippets?"+q.Encode(), nil, &out)
	return out, err
}

func (c *APIClient) Catalog(ctx context.Context, actor string, includeDemo bool) (apitypes.Catalog, error) {
	q := url.Values{}
	if actor != "" {
		q.Set("actor", actor)
	}
	if includeDemo {
		q.Set("include_demo", "true")
	}
	path := "/catalog"
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out apitypes.Catalog
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *APIClient) ToolSchema(ctx context.Context, toolID, actor string) (apitypes.Tool, error) {
	path := "/tools/" + url.PathEscape(toolID) + "/schema"
	if actor != "" {
		path += "?actor=" + url.QueryEscape(actor)
	}
	var out apitypes.Tool
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// ImportOpenAPI saves the HTTP server, parses the OpenAPI 3.0/3.1 document
// (from specBytes or fetched from specURL), and registers each generated tool.
func (c *APIClient) ImportOpenAPI(ctx context.Context, server apitypes.Server, specBytes []byte, specURL string, operationIDs []string) (int, error) {
	data := bytes.TrimSpace(specBytes)
	if len(data) == 0 && strings.TrimSpace(specURL) != "" {
		req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimSpace(specURL), nil)
		if err != nil {
			return 0, fmt.Errorf("构造 OpenAPI URL 请求失败: %w", err)
		}
		resp, err := (&http.Client{Timeout: 30 * time.Second}).Do(req)
		if err != nil {
			return 0, fmt.Errorf("下载 OpenAPI 文档失败: %w", err)
		}
		defer func() { _ = resp.Body.Close() }()
		if resp.StatusCode < 200 || resp.StatusCode >= 300 {
			return 0, fmt.Errorf("下载 OpenAPI 文档失败: HTTP %d", resp.StatusCode)
		}
		fetched, err := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
		if err != nil {
			return 0, fmt.Errorf("读取 OpenAPI 文档失败: %w", err)
		}
		data = fetched
	}
	if len(data) == 0 {
		return 0, errors.New("请提供 OpenAPI JSON/YAML 内容或文档 URL")
	}

	server.Kind = "http"
	imported, err := openapi.ImportOpenAPI(server.ID, data, operationIDs)
	if err != nil {
		return 0, err
	}
	// Temporary bridge until the server owns OpenAPI import: the parser still
	// returns domain tools, which reach the API in their wire encoding.
	raw, err := json.Marshal(imported)
	if err != nil {
		return 0, err
	}
	var tools []apitypes.Tool
	if err := json.Unmarshal(raw, &tools); err != nil {
		return 0, err
	}
	if _, err := c.SaveServer(ctx, server); err != nil {
		return 0, err
	}
	for _, tool := range tools {
		if _, err := c.SaveTool(ctx, tool); err != nil {
			return 0, fmt.Errorf("保存工具 %s 失败: %w", tool.ID, err)
		}
	}
	return len(tools), nil
}

func (c *APIClient) Maintenance(ctx context.Context) (apitypes.MaintenanceStatus, error) {
	var out apitypes.MaintenanceStatus
	err := c.doJSON(ctx, http.MethodGet, "/maintenance", nil, &out)
	return out, err
}

func (c *APIClient) SaveMaintenanceSource(ctx context.Context, src apitypes.MaintenanceSource) error {
	return c.doJSON(ctx, http.MethodPut, "/maintenance/sources", src, nil)
}

func (c *APIClient) GetAcceptance(ctx context.Context, serverID string) (apitypes.AcceptanceSuite, error) {
	var out apitypes.AcceptanceSuite
	err := c.doJSON(ctx, http.MethodGet, "/maintenance/sources/"+url.PathEscape(serverID)+"/acceptance", nil, &out)
	return out, err
}

func (c *APIClient) SaveAcceptance(ctx context.Context, serverID string, suite apitypes.AcceptanceSuite) error {
	return c.doJSON(ctx, http.MethodPut, "/maintenance/sources/"+url.PathEscape(serverID)+"/acceptance", suite, nil)
}

func (c *APIClient) RunMaintenance(ctx context.Context, serverID string, force bool) (apitypes.MaintenanceRun, error) {
	var out apitypes.MaintenanceRun
	err := c.doJSON(ctx, http.MethodPost, "/maintenance/sources/"+url.PathEscape(serverID)+"/run", apitypes.RunMaintenanceRequest{Force: force}, &out)
	return out, err
}

func (c *APIClient) GetMaintenanceRun(ctx context.Context, runID string) (apitypes.MaintenanceRun, error) {
	var resp apitypes.MaintenanceRunDetails
	err := c.doJSON(ctx, http.MethodGet, "/maintenance/runs/"+url.PathEscape(runID), nil, &resp)
	return resp.Run, err
}

func (c *APIClient) CancelMaintenanceRun(ctx context.Context, runID string) error {
	return c.doJSON(ctx, http.MethodPost, "/maintenance/runs/"+url.PathEscape(runID)+"/cancel", map[string]any{}, nil)
}

func (c *APIClient) PublishMaintenanceRun(ctx context.Context, runID string) error {
	return c.doJSON(ctx, http.MethodPost, "/maintenance/runs/"+url.PathEscape(runID)+"/publish", map[string]any{}, nil)
}

func (c *APIClient) RollbackMaintenanceRun(ctx context.Context, runID string) error {
	return c.doJSON(ctx, http.MethodPost, "/maintenance/runs/"+url.PathEscape(runID)+"/rollback", map[string]any{}, nil)
}

// --- Analytics & Formatting ---

type BlindSpotItem struct {
	Intent       string
	Status       string
	Actor        string
	Count        int
	LastTime     time.Time
	LastEventID  string
	ErrorSummary string
}

type TopToolItem struct {
	ID         string
	Name       string
	ServerID   string
	ServerName string
	ReadOnly   bool
	Hits       int
}

type GatewayTelemetry struct {
	SampleSize        int
	DiscoverCount     int
	InvokeCount       int
	AgentCount        int
	SelectedCount     int
	NoMatchCount      int
	NeedsContextCount int
	ErrorCount        int
	HitRatePct        *float64
	AvgEligible       *float64
	AvgSelectedOnHit  *float64
	CompressionPct    *float64
	P50DiscoverMs     *int64
	P95DiscoverMs     *int64
	AvgInvokeMs       *int64
	TotalTokens       int
	ToolHitCounts     map[string]int
	TopTools          []TopToolItem
	AttentionItems    []BlindSpotItem
}

type ActorServerCount struct {
	ServerID   string
	ServerName string
	ReadOnly   int
	Write      int
}

type ActorPolicyRow struct {
	Actor             string
	ReachableServers  int
	TotalEnabledTools int
	ReadOnlyTools     int
	WriteTools        int
	ByServer          []ActorServerCount
}

func percentileInt64(sortedAsc []int64, p float64) *int64 {
	if len(sortedAsc) == 0 {
		return nil
	}
	idx := int(math.Ceil((p/100.0)*float64(len(sortedAsc)))) - 1
	if idx < 0 {
		idx = 0
	}
	if idx >= len(sortedAsc) {
		idx = len(sortedAsc) - 1
	}
	v := sortedAsc[idx]
	return &v
}

// ComputeGatewayTelemetry computes real-time overview metrics from the bootstrap snapshot.
func ComputeGatewayTelemetry(state apitypes.Bootstrap) GatewayTelemetry {
	events := state.Events
	servers := state.Registry.Servers
	tools := state.Registry.Tools

	var (
		discoverCount     int
		invokeCount       int
		agentCount        int
		selectedCount     int
		noMatchCount      int
		needsContextCount int
		errorCount        int

		eligibleSum      float64
		eligibleSamples  int
		selectedToolsSum float64
		selectedSamples  int
		totalTokens      int
	)

	discoverDurations := make([]int64, 0, len(events))
	invokeDurations := make([]int64, 0, len(events))
	toolHitCounts := make(map[string]int)
	blindSpotMap := make(map[string]*BlindSpotItem)

	toolByID := make(map[string]apitypes.Tool, len(tools))
	for _, t := range tools {
		toolByID[t.ID] = t
	}
	serverByID := make(map[string]apitypes.Server, len(servers))
	for _, s := range servers {
		serverByID[s.ID] = s
	}

	for _, ev := range events {
		switch ev.Kind {
		case "discover":
			discoverCount++
			if ev.DurationMS >= 0 {
				discoverDurations = append(discoverDurations, ev.DurationMS)
			}
		case "invoke":
			invokeCount++
			if ev.DurationMS >= 0 {
				invokeDurations = append(invokeDurations, ev.DurationMS)
			}
		case "agent":
			agentCount++
		}

		isErr := ev.Status == "error" || ev.Status == "failed"
		if isErr {
			errorCount++
		} else {
			switch ev.Status {
			case "selected":
				selectedCount++
			case "no_match":
				noMatchCount++
			case "needs_context":
				needsContextCount++
			}
		}

		var detailMap map[string]any
		if len(ev.Detail) > 0 {
			_ = json.Unmarshal(ev.Detail, &detailMap)
		}
		if detailMap != nil {
			if elig, ok := jsonFloat(detailMap["eligible"]); ok && elig > 0 {
				eligibleSum += elig
				eligibleSamples++
			}
			if pt, ok := jsonFloat(detailMap["prompt_tokens"]); ok {
				ot, _ := jsonFloat(detailMap["output_tokens"])
				totalTokens += int(pt + ot)
			}
			if rawTools, ok := detailMap["tools"].([]any); ok {
				if ev.Status == "selected" {
					selectedToolsSum += float64(len(rawTools))
					selectedSamples++
				}
				for _, item := range rawTools {
					var id string
					switch v := item.(type) {
					case string:
						id = v
					case map[string]any:
						if s, ok := v["id"].(string); ok && s != "" {
							id = s
						} else if s, ok := v["tool_id"].(string); ok && s != "" {
							id = s
						}
					}
					if id != "" {
						toolHitCounts[id]++
					}
				}
			}
		}

		if isErr || ev.Status == "no_match" || ev.Status == "needs_context" {
			key := ev.Status + "::" + strings.TrimSpace(ev.Title)
			if existing, ok := blindSpotMap[key]; ok {
				existing.Count++
			} else {
				var errSummary string
				if detailMap != nil {
					if rawErr, ok := detailMap["error"]; ok && rawErr != nil {
						firstLine := strings.SplitN(fmt.Sprint(rawErr), "\n", 2)[0]
						errSummary = truncateRunes(firstLine, 120)
					} else if rawQ, ok := detailMap["question"]; ok && rawQ != nil {
						errSummary = fmt.Sprint(rawQ)
					}
				}
				blindSpotMap[key] = &BlindSpotItem{
					Intent:       ev.Title,
					Status:       ev.Status,
					Actor:        ev.Actor,
					Count:        1,
					LastTime:     ev.Time,
					LastEventID:  ev.ID,
					ErrorSummary: errSummary,
				}
			}
		}
	}

	slices.Sort(discoverDurations)

	var hitRatePct *float64
	if discoverCount > 0 {
		v := (float64(selectedCount) / float64(discoverCount)) * 100
		hitRatePct = &v
	}
	var avgEligible *float64
	if eligibleSamples > 0 {
		v := eligibleSum / float64(eligibleSamples)
		avgEligible = &v
	}
	var avgSelectedOnHit *float64
	if selectedSamples > 0 {
		v := selectedToolsSum / float64(selectedSamples)
		avgSelectedOnHit = &v
	}
	var compressionPct *float64
	if avgEligible != nil && *avgEligible > 0 && avgSelectedOnHit != nil {
		v := math.Max(0, (1.0 - *avgSelectedOnHit / *avgEligible)*100)
		compressionPct = &v
	}
	var avgInvokeMs *int64
	if len(invokeDurations) > 0 {
		var sum int64
		for _, d := range invokeDurations {
			sum += d
		}
		v := int64(math.Round(float64(sum) / float64(len(invokeDurations))))
		avgInvokeMs = &v
	}

	topTools := make([]TopToolItem, 0, len(toolHitCounts))
	for id, hits := range toolHitCounts {
		regTool, hasTool := toolByID[id]
		serverID := regTool.ServerID
		if serverID == "" {
			if parts := strings.SplitN(id, ".", 2); len(parts) > 0 {
				serverID = parts[0]
			}
		}
		srv := serverByID[serverID]
		name := regTool.Name
		if name == "" {
			if parts := strings.SplitN(id, ".", 2); len(parts) == 2 {
				name = parts[1]
			} else {
				name = id
			}
		}
		srvName := srv.Name
		if srvName == "" {
			srvName = serverID
		}
		readOnly := true
		if hasTool {
			readOnly = regTool.ReadOnly
		}
		topTools = append(topTools, TopToolItem{
			ID:         id,
			Name:       name,
			ServerID:   serverID,
			ServerName: srvName,
			ReadOnly:   readOnly,
			Hits:       hits,
		})
	}
	sort.Slice(topTools, func(i, j int) bool {
		if topTools[i].Hits != topTools[j].Hits {
			return topTools[i].Hits > topTools[j].Hits
		}
		return topTools[i].ID < topTools[j].ID
	})
	if len(topTools) > 6 {
		topTools = topTools[:6]
	}

	attentionItems := make([]BlindSpotItem, 0, len(blindSpotMap))
	for _, item := range blindSpotMap {
		attentionItems = append(attentionItems, *item)
	}
	sort.Slice(attentionItems, func(i, j int) bool {
		aErr := attentionItems[i].Status == "error" || attentionItems[i].Status == "failed"
		bErr := attentionItems[j].Status == "error" || attentionItems[j].Status == "failed"
		if aErr != bErr {
			return aErr
		}
		if attentionItems[i].Count != attentionItems[j].Count {
			return attentionItems[i].Count > attentionItems[j].Count
		}
		return attentionItems[i].LastTime.After(attentionItems[j].LastTime)
	})
	if len(attentionItems) > 7 {
		attentionItems = attentionItems[:7]
	}

	return GatewayTelemetry{
		SampleSize:        len(events),
		DiscoverCount:     discoverCount,
		InvokeCount:       invokeCount,
		AgentCount:        agentCount,
		SelectedCount:     selectedCount,
		NoMatchCount:      noMatchCount,
		NeedsContextCount: needsContextCount,
		ErrorCount:        errorCount,
		HitRatePct:        hitRatePct,
		AvgEligible:       avgEligible,
		AvgSelectedOnHit:  avgSelectedOnHit,
		CompressionPct:    compressionPct,
		P50DiscoverMs:     percentileInt64(discoverDurations, 50),
		P95DiscoverMs:     percentileInt64(discoverDurations, 95),
		AvgInvokeMs:       avgInvokeMs,
		TotalTokens:       totalTokens,
		ToolHitCounts:     toolHitCounts,
		TopTools:          topTools,
		AttentionItems:    attentionItems,
	}
}

// ComputeActorMatrix computes per-actor reachable services and read/write tool counts.
func ComputeActorMatrix(servers []apitypes.Server, tools []apitypes.Tool, sharedActor string) []ActorPolicyRow {
	realActiveServers := make([]apitypes.Server, 0, len(servers))
	activeServerIDs := make(map[string]bool, len(servers))
	for _, s := range servers {
		if !s.Demo && s.Enabled {
			realActiveServers = append(realActiveServers, s)
			activeServerIDs[s.ID] = true
		}
	}

	enabledRealTools := make([]apitypes.Tool, 0, len(tools))
	for _, t := range tools {
		if t.Enabled && activeServerIDs[t.ServerID] {
			enabledRealTools = append(enabledRealTools, t)
		}
	}

	actorOrder := []string{"developer", "observer"}
	actorSeen := map[string]bool{"developer": true, "observer": true}
	if sharedActor != "" && !actorSeen[sharedActor] {
		actorOrder = append(actorOrder, sharedActor)
		actorSeen[sharedActor] = true
	}
	for _, t := range tools {
		if !activeServerIDs[t.ServerID] {
			continue
		}
		for _, a := range t.Actors {
			if a != "" && !actorSeen[a] {
				actorOrder = append(actorOrder, a)
				actorSeen[a] = true
			}
		}
	}

	rows := make([]ActorPolicyRow, 0, len(actorOrder))
	for _, actor := range actorOrder {
		var allowed []apitypes.Tool
		readOnly := 0
		for _, t := range enabledRealTools {
			if slices.Contains(t.Actors, "*") || slices.Contains(t.Actors, actor) {
				allowed = append(allowed, t)
				if t.ReadOnly {
					readOnly++
				}
			}
		}
		writeTools := len(allowed) - readOnly
		byServer := make([]ActorServerCount, 0, len(realActiveServers))
		for _, srv := range realActiveServers {
			srvRO, srvTotal := 0, 0
			for _, t := range allowed {
				if t.ServerID == srv.ID {
					srvTotal++
					if t.ReadOnly {
						srvRO++
					}
				}
			}
			if srvTotal > 0 {
				byServer = append(byServer, ActorServerCount{
					ServerID:   srv.ID,
					ServerName: srv.Name,
					ReadOnly:   srvRO,
					Write:      srvTotal - srvRO,
				})
			}
		}
		rows = append(rows, ActorPolicyRow{
			Actor:             actor,
			ReachableServers:  len(byServer),
			TotalEnabledTools: len(allowed),
			ReadOnlyTools:     readOnly,
			WriteTools:        writeTools,
			ByServer:          byServer,
		})
	}
	return rows
}

func FormatCompactTokens(tokens int) string {
	if tokens >= 1_000_000 {
		return fmt.Sprintf("%.2fM", float64(tokens)/1_000_000)
	}
	if tokens >= 1_000 {
		return fmt.Sprintf("%.1fk", float64(tokens)/1_000)
	}
	return fmt.Sprintf("%d", tokens)
}

func ServerProtocolLabel(s apitypes.Server) string {
	if s.Kind == "http" {
		return "HTTP API"
	}
	if s.Transport == "stdio" {
		return "MCP · stdio"
	}
	return "MCP · HTTP"
}

func ServerTargetText(s apitypes.Server) string {
	if s.Kind == "mcp" && s.Transport == "stdio" {
		parts := make([]string, 0, 1+len(s.Args))
		if s.Command != "" {
			parts = append(parts, s.Command)
		}
		parts = append(parts, s.Args...)
		if joined := strings.TrimSpace(strings.Join(parts, " ")); joined != "" {
			return joined
		}
		return "—"
	}
	if s.Endpoint != "" {
		return s.Endpoint
	}
	return "—"
}

func ExampleFromSchema(schema map[string]any) map[string]any {
	out := map[string]any{}
	if schema == nil {
		return out
	}
	props, _ := schema["properties"].(map[string]any)
	var reqKeys []string
	switch raw := schema["required"].(type) {
	case []string:
		reqKeys = raw
	case []any:
		for _, item := range raw {
			if s, ok := item.(string); ok {
				reqKeys = append(reqKeys, s)
			}
		}
	}
	for _, key := range reqKeys {
		p, _ := props[key].(map[string]any)
		if p == nil {
			out[key] = ""
			continue
		}
		if def, ok := p["default"]; ok && def != nil {
			out[key] = def
			continue
		}
		if ex, ok := p["examples"].([]any); ok && len(ex) > 0 {
			out[key] = ex[0]
			continue
		}
		if en, ok := p["enum"].([]any); ok && len(en) > 0 {
			out[key] = en[0]
			continue
		}
		switch p["type"] {
		case "integer", "number":
			if minVal, ok := p["minimum"]; ok && minVal != nil {
				out[key] = minVal
			} else {
				out[key] = 1
			}
		case "boolean":
			out[key] = false
		case "object":
			out[key] = map[string]any{}
		case "array":
			out[key] = []any{}
		default:
			out[key] = ""
		}
	}
	return out
}

func FormatTime(t time.Time) string {
	if t.IsZero() {
		return "—"
	}
	return t.Local().Format("01-02 15:04:05")
}

func PrettyJSON(v any) string {
	if v == nil {
		return "{}"
	}
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return fmt.Sprint(v)
	}
	return string(b)
}

func jsonFloat(v any) (float64, bool) {
	switch n := v.(type) {
	case float64:
		return n, true
	case int:
		return float64(n), true
	case int64:
		return float64(n), true
	default:
		return 0, false
	}
}

func truncateRunes(s string, max int) string {
	if max <= 0 {
		return ""
	}
	runes := []rune(s)
	if len(runes) <= max {
		return s
	}
	return string(runes[:max])
}
