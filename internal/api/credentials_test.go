package api

import (
	"bytes"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/registry"
)

func TestCredentialAPIIsWriteOnlyAndAdminOnly(t *testing.T) {
	g, svcs := sharedGateway(t)
	s := registry.Server{ID: "credential-test", Name: "Credentials", Kind: "http", Enabled: true, Endpoint: "https://example.com", Headers: map[string]string{"Authorization": "Bearer ${TEST_CRED_TOKEN}"}}
	if err := g.SaveServer(s); err != nil {
		t.Fatal(err)
	}
	handler := Handler(g, svcs)
	request := func(method, path, token, body string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://localhost"+path, strings.NewReader(body))
		req.Header.Set("Authorization", "Bearer "+token)
		req.Header.Set("Content-Type", "application/json")
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}
	path := "/api/servers/credential-test/credentials"
	const sentinel = "credential-api-sentinel"
	for _, method := range []string{http.MethodGet, http.MethodPut} {
		r := request(method, path, readerTestToken, `{"values":{"TEST_CRED_TOKEN":"`+sentinel+`"}}`)
		if r.Code != http.StatusForbidden {
			t.Fatalf("non-admin %s returned %d", method, r.Code)
		}
	}
	r := request(http.MethodPut, path, adminTestToken, `{"values":{"TEST_CRED_TOKEN":"`+sentinel+`"}}`)
	if r.Code != 200 {
		t.Fatalf("save: %d %s", r.Code, r.Body.String())
	}
	for _, path := range []string{path, "/api/bootstrap", "/api/events"} {
		r := request(http.MethodGet, path, adminTestToken, "")
		if r.Code != 200 || bytes.Contains(r.Body.Bytes(), []byte(sentinel)) {
			t.Fatalf("unsafe read %s: %d", path, r.Code)
		}
	}
	r = request(http.MethodGet, path, adminTestToken, "")
	var status apitypes.ServerCredentials
	if err := json.Unmarshal(r.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if len(status.Credentials) != 1 || !status.Credentials[0].Configured || !status.Credentials[0].Saved {
		t.Fatal("missing saved status")
	}
	r = request(http.MethodPut, path, adminTestToken, `{"values":{"TEST_CRED_TOKEN":""}}`)
	if r.Code != 200 {
		t.Fatal("clear failed")
	}
	r = request(http.MethodGet, path, adminTestToken, "")
	if err := json.Unmarshal(r.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.Credentials[0].Saved {
		t.Fatal("clear did not remove credential")
	}
}

func TestServerCredentialsReportsOAuthUpgradeAndClearsOAuth(t *testing.T) {
	g, svcs := sharedGateway(t)
	stdioNotion := registry.Server{
		ID:        "notion",
		Name:      "Notion",
		Kind:      "mcp",
		Transport: "stdio",
		Command:   "npx",
		Args:      []string{"-y", "@notionhq/notion-mcp-server"},
		Env:       map[string]string{"NOTION_TOKEN": "${NOTION_TOKEN}"},
		Enabled:   true,
	}
	if err := g.SaveServer(stdioNotion); err != nil {
		t.Fatal(err)
	}
	handler := Handler(g, svcs)
	request := func(method, path, token string) *httptest.ResponseRecorder {
		req := httptest.NewRequest(method, "http://localhost"+path, nil)
		req.Header.Set("Authorization", "Bearer "+token)
		recorder := httptest.NewRecorder()
		handler.ServeHTTP(recorder, req)
		return recorder
	}

	r := request(http.MethodGet, "/api/servers/notion/credentials", adminTestToken)
	if r.Code != 200 {
		t.Fatalf("get credentials: %d %s", r.Code, r.Body.String())
	}
	var status apitypes.ServerCredentials
	if err := json.Unmarshal(r.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.OAuth != nil {
		t.Fatalf("expected nil oauth status for stdio server, got %+v", status.OAuth)
	}
	if status.OAuthUpgrade == nil || status.OAuthUpgrade.Endpoint != "https://mcp.notion.com/mcp" {
		t.Fatalf("expected Notion OAuthUpgrade, got %+v", status.OAuthUpgrade)
	}

	// Switch to remote OAuth server and verify OAuth status & DELETE /oauth.
	oauthNotion := registry.Server{
		ID:        "notion",
		Name:      "Notion",
		Kind:      "mcp",
		Transport: "http",
		Endpoint:  "https://mcp.notion.com/mcp",
		OAuth:     true,
		Enabled:   true,
	}
	if err := g.SaveServer(oauthNotion); err != nil {
		t.Fatal(err)
	}
	r = request(http.MethodGet, "/api/servers/notion/credentials", adminTestToken)
	if r.Code != 200 {
		t.Fatalf("get oauth credentials: %d", r.Code)
	}
	status = apitypes.ServerCredentials{}
	if err := json.Unmarshal(r.Body.Bytes(), &status); err != nil {
		t.Fatal(err)
	}
	if status.OAuth == nil || !status.OAuth.Enabled || status.OAuth.Authorized {
		t.Fatalf("expected enabled=true authorized=false, got %+v", status.OAuth)
	}
	if status.OAuthUpgrade != nil {
		t.Fatalf("expected nil OAuthUpgrade for remote OAuth server, got %+v", status.OAuthUpgrade)
	}

	if r := request(http.MethodDelete, "/api/servers/notion/oauth", readerTestToken); r.Code != http.StatusForbidden {
		t.Fatalf("expected non-admin DELETE oauth forbidden, got %d", r.Code)
	}
	if r := request(http.MethodDelete, "/api/servers/notion/oauth", adminTestToken); r.Code != 200 {
		t.Fatalf("expected admin DELETE oauth 200, got %d %s", r.Code, r.Body.String())
	}
}
