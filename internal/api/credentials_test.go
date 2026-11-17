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
	g := sharedGateway(t)
	s := registry.Server{ID: "credential-test", Name: "Credentials", Kind: "http", Enabled: true, Endpoint: "https://example.com", Headers: map[string]string{"Authorization": "Bearer ${TEST_CRED_TOKEN}"}}
	if err := g.SaveServer(s); err != nil {
		t.Fatal(err)
	}
	handler := Handler(g)
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
