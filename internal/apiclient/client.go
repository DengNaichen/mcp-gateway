// Package apiclient provides a typed HTTP client for the gateway /api control plane.
package apiclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/cookiejar"
	"net/url"
	"strconv"
	"strings"
	"time"

	"gatewaylab/internal/apitypes"
)

const (
	defaultTimeout  = 190 * time.Second
	maxResponseSize = 8 << 20
	maxIconSize     = 1 << 20
)

// ErrUnauthorized indicates the shared gateway rejected a request with HTTP 401.
var ErrUnauthorized = errors.New("需要登录授权")

// APIError represents an HTTP error payload returned by the gateway backend.
type APIError struct {
	Status     int
	Message    string
	Detail     string
	Code       string
	RetryAfter time.Duration
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

// Option configures a Client created by New.
type Option func(*Client)

// WithToken configures bearer token authentication for every /api request.
func WithToken(token string) Option {
	return func(c *Client) {
		c.token = strings.TrimSpace(token)
	}
}

// WithAdminToken configures local management authentication independently of
// shared-mode bearer tokens and session cookies.
func WithAdminToken(token string) Option {
	return func(c *Client) {
		c.adminToken = token
	}
}

// WithTimeout overrides the underlying HTTP client timeout.
func WithTimeout(d time.Duration) Option {
	return func(c *Client) {
		if d > 0 && c.httpClient != nil {
			c.httpClient.Timeout = d
		}
	}
}

// WithHTTPClient replaces the underlying *http.Client used by Client.
func WithHTTPClient(hc *http.Client) Option {
	return func(c *Client) {
		if hc != nil {
			c.httpClient = hc
		}
	}
}

// Client is a strongly-typed HTTP client for the local or shared gateway control plane.
type Client struct {
	baseURL    string
	httpClient *http.Client
	token      string
	adminToken string
}

// New creates a Client targeting baseURL with an in-memory cookie jar
// so shared-mode /api/auth/login sessions persist across requests.
func New(baseURL string, opts ...Option) *Client {
	jar, _ := cookiejar.New(nil)
	c := &Client{
		baseURL: strings.TrimRight(baseURL, "/"),
		httpClient: &http.Client{
			Jar:     jar,
			Timeout: defaultTimeout,
			Transport: &http.Transport{
				Proxy: nil,
			},
		},
	}
	for _, opt := range opts {
		if opt != nil {
			opt(c)
		}
	}
	if c.adminToken != "" {
		// net/http forwards custom headers across redirects. Credentialed
		// desktop requests target the gateway API directly.
		hc := *c.httpClient
		hc.CheckRedirect = func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }
		c.httpClient = &hc
	}
	return c
}

func (c *Client) newRequest(ctx context.Context, method, path string, body io.Reader) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, method, c.baseURL+"/api"+path, body)
	if err != nil {
		return nil, fmt.Errorf("build request: %w", err)
	}
	if c.token != "" {
		req.Header.Set("Authorization", "Bearer "+c.token)
	}
	if c.adminToken != "" {
		req.Header.Set("X-Gateway-Admin-Token", c.adminToken)
	}
	return req, nil
}

func parseAPIError(resp *http.Response, respBytes []byte) *APIError {
	var errBody apitypes.Error
	_ = json.Unmarshal(respBytes, &errBody)
	msg := errBody.Error
	if msg == "" {
		if resp.StatusCode == http.StatusUnauthorized {
			msg = ErrUnauthorized.Error()
		} else {
			msg = fmt.Sprintf("HTTP %d", resp.StatusCode)
		}
	}
	var retryAfter time.Duration
	if sec, err := strconv.Atoi(strings.TrimSpace(resp.Header.Get("Retry-After"))); err == nil && sec > 0 {
		retryAfter = time.Duration(sec) * time.Second
	} else if errBody.RetryAfterSeconds > 0 {
		retryAfter = time.Duration(errBody.RetryAfterSeconds) * time.Second
	}
	return &APIError{
		Status:     resp.StatusCode,
		Message:    msg,
		Detail:     errBody.Detail,
		Code:       errBody.Code,
		RetryAfter: retryAfter,
	}
}

func (c *Client) doJSON(ctx context.Context, method, path string, body any, out any) error {
	var bodyReader io.Reader
	if body != nil {
		raw, err := json.Marshal(body)
		if err != nil {
			return fmt.Errorf("encode request: %w", err)
		}
		bodyReader = bytes.NewReader(raw)
	}
	req, err := c.newRequest(ctx, method, path, bodyReader)
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer func() { _ = resp.Body.Close() }()

	respBytes, err := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
	if err != nil {
		return fmt.Errorf("read response: %w", err)
	}

	if resp.StatusCode == http.StatusUnauthorized && path != "/auth/login" {
		return fmt.Errorf("%w: %w", ErrUnauthorized, parseAPIError(resp, respBytes))
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return parseAPIError(resp, respBytes)
	}

	if out != nil && len(bytes.TrimSpace(respBytes)) > 0 {
		if err := json.Unmarshal(respBytes, out); err != nil {
			return fmt.Errorf("decode response: %w", err)
		}
	}
	return nil
}

func (c *Client) AuthMe(ctx context.Context) (apitypes.AuthMe, error) {
	var out apitypes.AuthMe
	err := c.doJSON(ctx, http.MethodGet, "/auth/me", nil, &out)
	return out, err
}

func (c *Client) Login(ctx context.Context, token string) (apitypes.Principal, error) {
	var resp apitypes.LoginResult
	err := c.doJSON(ctx, http.MethodPost, "/auth/login", apitypes.LoginRequest{Token: token}, &resp)
	return resp.Principal, err
}

func (c *Client) Logout(ctx context.Context) error {
	return c.doJSON(ctx, http.MethodPost, "/auth/logout", struct{}{}, nil)
}

func (c *Client) Bootstrap(ctx context.Context) (apitypes.Bootstrap, error) {
	var out apitypes.Bootstrap
	err := c.doJSON(ctx, http.MethodGet, "/bootstrap", nil, &out)
	return out, err
}

func (c *Client) Events(ctx context.Context) ([]apitypes.Event, error) {
	var out []apitypes.Event
	err := c.doJSON(ctx, http.MethodGet, "/events", nil, &out)
	return out, err
}

func (c *Client) SetupStatus(ctx context.Context) (apitypes.SetupStatus, error) {
	var out apitypes.SetupStatus
	err := c.doJSON(ctx, http.MethodGet, "/setup/status", nil, &out)
	return out, err
}

func (c *Client) SaveSettings(ctx context.Context, settings apitypes.Settings, apiKey string, clearKey bool) error {
	payload := apitypes.SaveSettingsRequest{Settings: settings, KeyUpdate: apitypes.KeyUpdate{APIKey: apiKey, ClearKey: clearKey}}
	return c.doJSON(ctx, http.MethodPut, "/settings", payload, nil)
}

func (c *Client) PingModel(ctx context.Context, settings apitypes.Settings, profileID, apiKey string, clearKey bool) (apitypes.PingModelResult, error) {
	payload := apitypes.PingModelRequest{Settings: settings, ProfileID: profileID, KeyUpdate: apitypes.KeyUpdate{APIKey: apiKey, ClearKey: clearKey}}
	var out apitypes.PingModelResult
	err := c.doJSON(ctx, http.MethodPost, "/model/ping", payload, &out)
	return out, err
}

func (c *Client) SaveModelProfile(ctx context.Context, profile apitypes.ModelProfile, apiKey string, clearKey bool) (apitypes.ModelProfile, error) {
	payload := apitypes.SaveModelProfileRequest{ModelProfile: profile, KeyUpdate: apitypes.KeyUpdate{APIKey: apiKey, ClearKey: clearKey}}
	var out apitypes.ModelProfile
	err := c.doJSON(ctx, http.MethodPut, "/model-profiles", payload, &out)
	return out, err
}

func (c *Client) ActivateModelProfile(ctx context.Context, id string) (apitypes.ModelProfile, error) {
	var out apitypes.ModelProfile
	err := c.doJSON(ctx, http.MethodPost, "/model-profiles/"+url.PathEscape(id)+"/activate", nil, &out)
	return out, err
}

func (c *Client) DeleteModelProfile(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/model-profiles/"+url.PathEscape(id), nil, nil)
}

func (c *Client) TestModel(ctx context.Context, settings apitypes.Settings, apiKey string, clearKey bool, intent string) (apitypes.DiscoverResult, error) {
	payload := apitypes.SetupModelRequest{Settings: settings, KeyUpdate: apitypes.KeyUpdate{APIKey: apiKey, ClearKey: clearKey}, Intent: intent}
	var out apitypes.DiscoverResult
	err := c.doJSON(ctx, http.MethodPost, "/setup/model", payload, &out)
	return out, err
}

func (c *Client) MarketServers(ctx context.Context, query string) (apitypes.MarketSearchResult, error) {
	path := "/market"
	if q := strings.TrimSpace(query); q != "" {
		path += "?q=" + url.QueryEscape(q)
	}
	var out apitypes.MarketSearchResult
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

func (c *Client) InstallMarketServer(ctx context.Context, req apitypes.InstallMarketRequest) (apitypes.InstallMarketResult, error) {
	var out apitypes.InstallMarketResult
	err := c.doJSON(ctx, http.MethodPost, "/market/install", req, &out)
	return out, err
}

func (c *Client) MarketIcon(ctx context.Context, id, iconURL string) ([]byte, string, error) {
	q := url.Values{}
	if id != "" {
		q.Set("id", id)
	}
	if iconURL != "" {
		q.Set("url", iconURL)
	}
	req, err := c.newRequest(ctx, http.MethodGet, "/market/icon?"+q.Encode(), nil)
	if err != nil {
		return nil, "", err
	}
	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		respBytes, _ := io.ReadAll(io.LimitReader(resp.Body, maxResponseSize))
		if resp.StatusCode == http.StatusUnauthorized {
			return nil, "", fmt.Errorf("%w: %w", ErrUnauthorized, parseAPIError(resp, respBytes))
		}
		return nil, "", parseAPIError(resp, respBytes)
	}
	b, err := io.ReadAll(io.LimitReader(resp.Body, maxIconSize))
	if err != nil {
		return nil, "", err
	}
	return b, resp.Header.Get("Content-Type"), nil
}

func (c *Client) TestSource(ctx context.Context, server apitypes.Server) (apitypes.ConnectionCheck, error) {
	var out apitypes.ConnectionCheck
	err := c.doJSON(ctx, http.MethodPost, "/setup/source", server, &out)
	return out, err
}

func (c *Client) SaveServer(ctx context.Context, server apitypes.Server) (apitypes.Server, error) {
	var out apitypes.Server
	err := c.doJSON(ctx, http.MethodPut, "/servers", server, &out)
	return out, err
}

func (c *Client) SyncServer(ctx context.Context, serverID string) (int, error) {
	var out apitypes.SyncServerResult
	err := c.doJSON(ctx, http.MethodPost, "/servers/"+url.PathEscape(serverID)+"/sync", struct{}{}, &out)
	return out.Imported, err
}

func (c *Client) EnableServerTools(ctx context.Context, serverID string, toolIDs []string) error {
	if toolIDs == nil {
		toolIDs = []string{}
	}
	return c.doJSON(ctx, http.MethodPost, "/servers/"+url.PathEscape(serverID)+"/tools", apitypes.EnableToolsRequest{IDs: toolIDs}, nil)
}

func (c *Client) SaveTool(ctx context.Context, tool apitypes.Tool) (apitypes.Tool, error) {
	var out apitypes.Tool
	err := c.doJSON(ctx, http.MethodPut, "/tools", tool, &out)
	return out, err
}

func (c *Client) Discover(ctx context.Context, req apitypes.DiscoverRequest) (apitypes.DiscoverResult, error) {
	return c.DiscoverScoped(ctx, req, nil)
}

func (c *Client) DiscoverScoped(ctx context.Context, req apitypes.DiscoverRequest, sim *apitypes.SimScope) (apitypes.DiscoverResult, error) {
	req.SimScope = sim
	var out apitypes.DiscoverResult
	err := c.doJSON(ctx, http.MethodPost, "/discover", req, &out)
	return out, err
}

// DiscoverSummary calls POST /api/discover with summary_only=true and decodes the capability summary envelope.
func (c *Client) DiscoverSummary(ctx context.Context, req apitypes.DiscoverRequest) (apitypes.DiscoverSummaryResult, error) {
	req.SummaryOnly = true
	var out apitypes.DiscoverSummaryResult
	err := c.doJSON(ctx, http.MethodPost, "/discover", req, &out)
	return out, err
}

func (c *Client) Invoke(ctx context.Context, req apitypes.InvokeRequest) (apitypes.InvokeResult, error) {
	return c.InvokeScoped(ctx, req, nil)
}

func (c *Client) InvokeScoped(ctx context.Context, req apitypes.InvokeRequest, sim *apitypes.SimScope) (apitypes.InvokeResult, error) {
	req.SimScope = sim
	var out apitypes.InvokeResult
	err := c.doJSON(ctx, http.MethodPost, "/invoke", req, &out)
	return out, err
}

func (c *Client) GetScopes(ctx context.Context) (apitypes.ScopeOverview, error) {
	var out apitypes.ScopeOverview
	err := c.doJSON(ctx, http.MethodGet, "/scopes", nil, &out)
	return out, err
}

func (c *Client) SetScopeMode(ctx context.Context, enforce bool) error {
	return c.doJSON(ctx, http.MethodPut, "/scopes/mode", apitypes.ScopeModeRequest{Enforce: enforce}, nil)
}

func (c *Client) SaveRepository(ctx context.Context, in apitypes.SaveRepositoryRequest) (apitypes.Repository, error) {
	var out apitypes.Repository
	err := c.doJSON(ctx, http.MethodPut, "/scopes/repositories", in, &out)
	return out, err
}

func (c *Client) DeleteRepository(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/scopes/repositories/"+url.PathEscape(id), nil, nil)
}

func (c *Client) SaveScopeRule(ctx context.Context, rule apitypes.ScopeRule) (apitypes.ScopeRule, error) {
	if rule.Grants == nil {
		rule.Grants = []apitypes.ServiceGrant{}
	}
	var out apitypes.ScopeRule
	err := c.doJSON(ctx, http.MethodPut, "/scopes/rules", rule, &out)
	return out, err
}

func (c *Client) DeleteScopeRule(ctx context.Context, repoKey string) error {
	q := url.Values{}
	q.Set("repo_key", repoKey)
	return c.doJSON(ctx, http.MethodDelete, "/scopes/rules?"+q.Encode(), nil, nil)
}

func (c *Client) GetHookSnippet(ctx context.Context, agent string) (apitypes.ScopeSnippetResponse, error) {
	q := url.Values{}
	q.Set("agent", agent)
	var out apitypes.ScopeSnippetResponse
	err := c.doJSON(ctx, http.MethodGet, "/scopes/snippets?"+q.Encode(), nil, &out)
	return out, err
}

func (c *Client) Catalog(ctx context.Context, actor string, includeDemo bool) (apitypes.Catalog, error) {
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

func (c *Client) ToolSchema(ctx context.Context, toolID, actor string) (apitypes.ToolSchema, error) {
	return c.ToolSchemaScoped(ctx, toolID, actor, nil)
}

func (c *Client) ToolSchemaScoped(ctx context.Context, toolID, actor string, simScope *apitypes.SimScope) (apitypes.ToolSchema, error) {
	path := "/tools/" + url.PathEscape(toolID) + "/schema"
	q := url.Values{}
	if actor != "" {
		q.Set("actor", actor)
	}
	if simScope != nil {
		q.Set("sim_scope", simScope.RepoKey)
	}
	if enc := q.Encode(); enc != "" {
		path += "?" + enc
	}
	var out apitypes.ToolSchema
	err := c.doJSON(ctx, http.MethodGet, path, nil, &out)
	return out, err
}

// ImportOpenAPI asks the gateway to register server and the tools generated
// from an OpenAPI 3.0/3.1 document: specBytes when given, otherwise the
// document the gateway downloads from specURL.
func (c *Client) ImportOpenAPI(ctx context.Context, server apitypes.Server, specBytes []byte, specURL string, operationIDs []string) (int, error) {
	req := apitypes.ImportOpenAPIRequest{Server: server, Spec: string(specBytes), SpecURL: strings.TrimSpace(specURL), OperationIDs: operationIDs}
	var out apitypes.ImportOpenAPIResult
	err := c.doJSON(ctx, http.MethodPost, "/servers/openapi", req, &out)
	return out.Imported, err
}

func (c *Client) Maintenance(ctx context.Context) (apitypes.MaintenanceStatus, error) {
	var out apitypes.MaintenanceStatus
	err := c.doJSON(ctx, http.MethodGet, "/maintenance", nil, &out)
	return out, err
}

func (c *Client) SaveMaintenanceSource(ctx context.Context, src apitypes.MaintenanceSource) error {
	return c.doJSON(ctx, http.MethodPut, "/maintenance/sources", src, nil)
}

func (c *Client) GetAcceptance(ctx context.Context, serverID string) (apitypes.AcceptanceSuite, error) {
	var out apitypes.AcceptanceSuite
	err := c.doJSON(ctx, http.MethodGet, "/maintenance/sources/"+url.PathEscape(serverID)+"/acceptance", nil, &out)
	return out, err
}

func (c *Client) SaveAcceptance(ctx context.Context, serverID string, suite apitypes.AcceptanceSuite) error {
	return c.doJSON(ctx, http.MethodPut, "/maintenance/sources/"+url.PathEscape(serverID)+"/acceptance", suite, nil)
}

func (c *Client) RunMaintenance(ctx context.Context, serverID string, force bool) (apitypes.MaintenanceRun, error) {
	var out apitypes.MaintenanceRun
	err := c.doJSON(ctx, http.MethodPost, "/maintenance/sources/"+url.PathEscape(serverID)+"/run", apitypes.RunMaintenanceRequest{Force: force}, &out)
	return out, err
}

func (c *Client) GetMaintenanceRun(ctx context.Context, runID string) (apitypes.MaintenanceRun, error) {
	var resp apitypes.MaintenanceRunDetails
	err := c.doJSON(ctx, http.MethodGet, "/maintenance/runs/"+url.PathEscape(runID), nil, &resp)
	return resp.Run, err
}

func (c *Client) CancelMaintenanceRun(ctx context.Context, runID string) error {
	return c.doJSON(ctx, http.MethodPost, "/maintenance/runs/"+url.PathEscape(runID)+"/cancel", struct{}{}, nil)
}

func (c *Client) PublishMaintenanceRun(ctx context.Context, runID string) error {
	return c.doJSON(ctx, http.MethodPost, "/maintenance/runs/"+url.PathEscape(runID)+"/publish", struct{}{}, nil)
}

func (c *Client) RollbackMaintenanceRun(ctx context.Context, runID string) error {
	return c.doJSON(ctx, http.MethodPost, "/maintenance/runs/"+url.PathEscape(runID)+"/rollback", struct{}{}, nil)
}

func (c *Client) ServerCredentials(ctx context.Context, id string) (apitypes.ServerCredentials, error) {
	var out apitypes.ServerCredentials
	err := c.doJSON(ctx, http.MethodGet, "/servers/"+url.PathEscape(id)+"/credentials", nil, &out)
	return out, err
}
func (c *Client) SaveServerCredentials(ctx context.Context, id string, values map[string]string) error {
	return c.doJSON(ctx, http.MethodPut, "/servers/"+url.PathEscape(id)+"/credentials", apitypes.SaveCredentialsRequest{Values: values}, nil)
}
func (c *Client) ClearServerOAuth(ctx context.Context, id string) error {
	return c.doJSON(ctx, http.MethodDelete, "/servers/"+url.PathEscape(id)+"/oauth", nil, nil)
}
