package api

import (
	"bytes"
	"context"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"testing"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gatewaylab/internal/access"
	"gatewaylab/internal/gateway"
	"gatewaylab/internal/registry"
)

const adminTestToken = "admin-test-token-01234567890123456789"
const readerTestToken = "reader-test-token-01234567890123456789"
const writerTestToken = "writer-test-token-01234567890123456789"

func sharedGateway(t *testing.T) *gateway.Gateway {
	t.Helper()
	t.Setenv("AUTH_ADMIN", adminTestToken)
	t.Setenv("AUTH_READER", readerTestToken)
	t.Setenv("AUTH_WRITER", writerTestToken)
	path := filepath.Join(t.TempDir(), "auth.json")
	config := map[string]any{"principals": []access.PrincipalConfig{
		{Principal: access.Principal{ID: "owner", Groups: []string{"developer"}, Servers: []string{"*"}, AllowWrite: true, Admin: true}, TokenEnv: "AUTH_ADMIN"},
		{Principal: access.Principal{ID: "reader", Groups: []string{"developer"}, Servers: []string{"service"}, Tools: []string{"service.read"}}, TokenEnv: "AUTH_READER"},
		{Principal: access.Principal{ID: "writer", Groups: []string{"developer"}, Servers: []string{"service"}, AllowWrite: true}, TokenEnv: "AUTH_WRITER"},
	}}
	if err := writeJSON(path, config); err != nil {
		t.Fatal(err)
	}
	t.Setenv("GATEWAY_AUTH_FILE", path)
	g, _ := testGateway(t)
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, map[string]any{"status": "ok", "token": "private", "email": "person@example.com", "logs": []any{map[string]any{"user": map[string]any{"ssn": "123-45-6789"}}}})
	}))
	t.Cleanup(upstream.Close)
	if err := g.Registry().SaveServer(registry.Server{ID: "service", Name: "Service", Kind: "http", Endpoint: upstream.URL, Enabled: true, RedactEmails: true, RedactFields: []string{"logs.user.ssn"}}); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"read", "write"} {
		tool := registry.Tool{ID: "service." + name, ServerID: "service", Name: name, Description: name + " status", Enabled: true, Actors: []string{"developer"}, InputSchema: objectSchema(map[string]any{}), ReadOnly: name == "read", Method: "GET", Path: "/"}
		if name == "write" {
			tool.Method = "POST"
		}
		if err := g.Registry().SaveTool(tool); err != nil {
			t.Fatal(err)
		}
	}
	return g
}
func TestAccessIdentityScopeAndRedaction(t *testing.T) {
	g := sharedGateway(t)
	handler := Handler(g)
	call := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		if token != "" {
			req.Header.Set("Authorization", "Bearer "+token)
		}
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		return out
	}
	if got := call("GET", "/api/bootstrap", "", ""); got.Code != 401 {
		t.Fatal("management data accessible without token")
	}
	if got := call("PUT", "/api/tools", readerTestToken, `{}`); got.Code != 403 {
		t.Fatal("reader gained management access")
	}
	if got := call("GET", "/api/catalog?actor=owner", readerTestToken, ""); got.Code != 200 || strings.Contains(got.Body.String(), "service.write") || !strings.Contains(got.Body.String(), "service.read") {
		t.Fatalf("catalog scope: %d %s", got.Code, got.Body)
	}
	if got := call("GET", "/api/tools/service.write/schema?actor=owner", readerTestToken, ""); got.Code != 403 {
		t.Fatal("schema actor spoof succeeded")
	}
	if got := call("POST", "/api/invoke", readerTestToken, `{"tool_id":"service.write","actor":"owner","arguments":{}}`); got.Code != 403 {
		t.Fatal("write actor spoof succeeded")
	}
	got := call("POST", "/api/invoke", readerTestToken, `{"tool_id":"service.read","actor":"owner","arguments":{}}`)
	if got.Code != 200 || strings.Contains(got.Body.String(), "person@example.com") || strings.Contains(got.Body.String(), "123-45-6789") || strings.Contains(got.Body.String(), "private") {
		t.Fatalf("redaction failure: %d %s", got.Code, got.Body)
	}
	if got := call("POST", "/api/invoke", writerTestToken, `{"tool_id":"service.write","arguments":{}}`); got.Code != 200 {
		t.Fatalf("authorized write failed: %d %s", got.Code, got.Body)
	}
	actor := g.Activity().Recent()[0].Actor
	if actor != "writer" {
		t.Fatalf("audit actor was not server-derived: %s", actor)
	}
}
func TestAdminLoginCookieLogoutAndCSRF(t *testing.T) {
	g := sharedGateway(t)
	handler := Handler(g)
	req := httptest.NewRequest("POST", "http://localhost/api/auth/login", bytes.NewReader(mustJSON(t, map[string]string{"token": adminTestToken})))
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatalf("login: %d %s", out.Code, out.Body)
	}
	cookies := out.Result().Cookies()
	if len(cookies) != 1 || !cookies[0].HttpOnly || cookies[0].SameSite != http.SameSiteStrictMode {
		t.Fatal("session cookie flags missing")
	}
	req = httptest.NewRequest("GET", "http://localhost/api/bootstrap", nil)
	req.AddCookie(cookies[0])
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal("cookie authentication failed")
	}
	req = httptest.NewRequest("POST", "http://localhost/api/auth/logout", nil)
	req.AddCookie(cookies[0])
	req.Header.Set("Origin", "http://localhost:3000")
	req.Header.Set("Sec-Fetch-Site", "cross-site")
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 403 {
		t.Fatal("shared mode trusted development origin implicitly")
	}
	req = httptest.NewRequest("POST", "http://localhost/api/auth/logout", nil)
	req.AddCookie(cookies[0])
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 200 {
		t.Fatal("logout failed")
	}
	req = httptest.NewRequest("GET", "http://localhost/api/bootstrap", nil)
	req.AddCookie(cookies[0])
	out = httptest.NewRecorder()
	handler.ServeHTTP(out, req)
	if out.Code != 401 {
		t.Fatal("logged out session still worked")
	}
}
func TestMCPUsesAuthenticatedPrincipal(t *testing.T) {
	g := sharedGateway(t)
	host := httptest.NewServer(Handler(g))
	defer host.Close()
	client := mcp.NewClient(&mcp.Implementation{Name: "auth-test", Version: "1"}, nil)
	session, err := client.Connect(context.Background(), &mcp.StreamableClientTransport{Endpoint: host.URL + "/mcp", HTTPClient: &http.Client{Transport: headerTransport{headers: map[string]string{"Authorization": "Bearer " + readerTestToken}, base: http.DefaultTransport}}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer closeSession(session)
	result, err := session.CallTool(context.Background(), &mcp.CallToolParams{Name: "invoke_tool", Arguments: map[string]any{"tool_id": "service.write", "actor": "owner", "arguments": map[string]any{}}})
	if err == nil && !result.IsError {
		t.Fatal("MCP request actor bypassed write policy")
	}
	result, err = session.CallTool(context.Background(), &mcp.CallToolParams{Name: "get_tool_schema", Arguments: map[string]any{"tool_id": "service.read", "actor": "owner"}})
	if err != nil || result.IsError {
		t.Fatalf("authorized MCP schema access failed: %v %+v", err, result)
	}
}
func TestPublicListenerRequiresAuthentication(t *testing.T) {
	t.Setenv("GATEWAY_AUTH_FILE", "")
	if g, err := gateway.New(t.TempDir(), "http://0.0.0.0:8788"); err == nil {
		if closeErr := g.Close(); closeErr != nil {
			t.Error(closeErr)
		}
		t.Fatal("public listener accepted without auth")
	}
	g, _ := testGateway(t)
	req := httptest.NewRequest("GET", "http://attacker.invalid/api/bootstrap", nil)
	out := httptest.NewRecorder()
	Handler(g).ServeHTTP(out, req)
	if out.Code != 403 {
		t.Fatal("local mode accepted a rebinding host")
	}
}

func TestLocalModeRejectsNonLoopbackHost(t *testing.T) {
	g, _ := testGateway(t)
	if g.Access() != nil {
		t.Fatal("test gateway must run in local mode")
	}
	handler := Handler(g)
	const message = "本机模式不接受外部 Host，请配置认证后共享"
	for _, host := range []string{"evil.example", "evil.example:8788", "192.168.1.10:8788", "10.0.0.1", "[2001:db8::1]:8788", "localhost.evil.example", "127.0.0.1.evil.example", "0.0.0.0:8788", ""} {
		req := httptest.NewRequest("GET", "/api/health", nil)
		req.Host = host
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if out.Code != 403 || !strings.Contains(out.Body.String(), message) {
			t.Fatalf("host %q: %d %s", host, out.Code, out.Body)
		}
	}
	for _, host := range []string{"127.0.0.1", "127.0.0.1:8788", "localhost", "localhost:8788", "LOCALHOST:8788", "[::1]", "[::1]:8788"} {
		req := httptest.NewRequest("GET", "/api/health", nil)
		req.Host = host
		out := httptest.NewRecorder()
		handler.ServeHTTP(out, req)
		if out.Code != 200 {
			t.Fatalf("host %q: %d %s", host, out.Code, out.Body)
		}
	}
}
