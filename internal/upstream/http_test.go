package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"reflect"
	"strings"
	"testing"

	"golang.org/x/oauth2"
)

func TestHTTPMappingWireContract(t *testing.T) {
	host := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.EscapedPath() != "/api/team%2Fproject" || r.URL.Query().Get("filter[state]") != "open" || !reflect.DeepEqual(r.URL.Query()["tag"], []string{"one", "two"}) {
			t.Errorf("URL=%s", r.URL)
		}
		if r.Header.Get("Authorization") != "Bearer injected" || r.Header.Get("X-Enabled") != "true" || r.Header.Get("Content-Type") != "application/json" {
			t.Errorf("headers=%v", r.Header)
		}
		cookie, err := r.Cookie("locale")
		if err != nil || cookie.Value != "en+US" {
			t.Errorf("cookie=%v err=%v", cookie, err)
		}
		b, _ := io.ReadAll(r.Body)
		if string(b) != `{"name":"example"}` {
			t.Errorf("body=%s", b)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = io.WriteString(w, `{"id":42}`)
	}))
	defer host.Close()
	tool := Tool{Method: "POST", Path: "/api/{project}", HTTP: &HTTPMapping{Body: true, Parameters: []HTTPParameter{
		{Name: "project", In: "path", Style: "simple"}, {Name: "filter", In: "query", Style: "deepObject", Explode: true}, {Name: "tag", In: "query", Style: "form", Explode: true}, {Name: "X-Enabled", In: "header", Style: "simple"}, {Name: "locale", In: "cookie", Style: "form"},
	}, Responses: map[string]HTTPResponse{"2XX": {MediaType: "application/json", Schema: map[string]any{"type": "object", "required": []any{"id"}, "properties": map[string]any{"id": map[string]any{"type": "integer"}}}}}}}
	args := map[string]any{"path": map[string]any{"project": "team/project"}, "query": map[string]any{"filter": map[string]any{"state": "open"}, "tag": []any{"one", "two"}}, "headers": map[string]any{"X-Enabled": true}, "cookies": map[string]any{"locale": "en US"}, "body": map[string]any{"name": "example"}}
	raw, err := CallHTTPWithLookup(context.Background(), Server{Endpoint: host.URL, Headers: map[string]string{"Authorization": "Bearer ${KEY}"}}, tool, args, func(string) string { return "injected" })
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(raw, map[string]any{"id": float64(42)}) {
		t.Fatalf("result=%v", raw)
	}
}

func TestHTTPRedirectsStayAtConfiguredOrigin(t *testing.T) {
	reached := false
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { reached = true }))
	defer target.Close()
	source := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { http.Redirect(w, r, target.URL, http.StatusFound) }))
	defer source.Close()
	for _, mapped := range []bool{false, true} {
		tool := Tool{Method: "GET", Path: "/tool"}
		if mapped {
			tool.HTTP = &HTTPMapping{Responses: map[string]HTTPResponse{"200": {MediaType: "empty"}}}
		}
		_, err := CallHTTP(context.Background(), Server{Endpoint: source.URL}, tool, map[string]any{})
		if err == nil || err.Error() != "downstream returned http 302" {
			t.Fatalf("mapped=%v err=%v", mapped, err)
		}
	}
	if reached {
		t.Fatal("followed downstream redirect")
	}
}

func TestMappedRequestAndResponseValidation(t *testing.T) {
	source := Server{Endpoint: "https://example.invalid"}
	tool := Tool{Method: "GET", Path: "/api/{id}", HTTP: &HTTPMapping{Parameters: []HTTPParameter{{Name: "id", In: "path", Style: "simple"}}, Responses: map[string]HTTPResponse{"200": {MediaType: "application/json", Schema: map[string]any{"type": "integer"}}, "204": {MediaType: "empty"}, "default": {MediaType: "text/plain"}}}}
	for _, value := range []string{".", ".."} {
		_, err := BuildMappedRequest(context.Background(), source, tool, map[string]any{"path": map[string]any{"id": value}})
		if !errors.Is(err, ErrInvalid) || err.Error() != "路径参数不能是相对路径片段" {
			t.Fatalf("path %q: %v", value, err)
		}
	}
	if _, err := BuildMappedRequest(context.Background(), source, tool, map[string]any{}); !errors.Is(err, ErrInvalid) || err.Error() != "缺少路径参数" {
		t.Fatalf("missing path: %v", err)
	}
	if _, err := MappedResponse(tool, 200, []byte(`"wrong"`)); err == nil || !strings.Contains(err.Error(), "upstream response violates schema") {
		t.Fatalf("schema validation: %v", err)
	}
	if _, err := MappedResponse(tool, 204, []byte("unexpected")); err == nil || err.Error() != "unexpected body for empty response" {
		t.Fatalf("empty response: %v", err)
	}
	text, err := MappedResponse(tool, 201, []byte("hello"))
	if err != nil || !reflect.DeepEqual(text, map[string]any{"text": "hello"}) {
		t.Fatalf("default response: %v %v", text, err)
	}
}

func TestCredentialsAreRedactedBeforeProjection(t *testing.T) {
	source := Server{Headers: map[string]string{"Authorization": "Bearer ${KEY}"}, Env: map[string]string{"KEY": "${KEY}"}, RedactEmails: true, RedactFields: []string{"items.private"}}
	data := map[string]any{"message": "Bearer secret-value-12345 user@example.com", "nested": map[string]any{"apiKey": "another secret"}, "items": []any{map[string]any{"public": "visible", "private": "hidden", "password": "hidden"}}}
	redacted := RedactResponseWithLookup(data, source, func(string) string { return "secret-value-12345" })
	projected := Project(redacted, []string{"message", "nested.apiKey", "items.public", "items.private"})
	b, err := json.Marshal(projected)
	if err != nil {
		t.Fatal(err)
	}
	for _, secret := range []string{"secret-value", "another secret", "hidden", "user@example.com"} {
		if strings.Contains(string(b), secret) {
			t.Fatalf("credential leaked: %s", b)
		}
	}
	if !strings.Contains(string(b), `"public":"visible"`) || !strings.Contains(string(b), `"private":"[redacted]"`) {
		t.Fatalf("projection=%s", b)
	}
	original, _ := json.Marshal(data)
	if !strings.Contains(string(original), "another secret") {
		t.Fatal("redaction mutated input")
	}
}

func TestServiceRedactResponseRedactsOAuthTokensAndLookupSecrets(t *testing.T) {
	svc := New(Config{LookupEnv: func(key string) string {
		if key == "KEY" {
			return "env-secret-999"
		}
		return ""
	}})
	svc.oauthTokens["api"] = oauth2.StaticTokenSource(&oauth2.Token{AccessToken: "oauth-token-abc"})

	source := Server{ID: "api", Headers: map[string]string{"X-API-Key": "${KEY}"}}
	data := map[string]any{
		"note":   "token oauth-token-abc and key env-secret-999",
		"header": "Bearer oauth-token-abc",
	}
	redacted := svc.RedactResponse(data, source).(map[string]any)
	if strings.Contains(redacted["note"].(string), "oauth-token-abc") || strings.Contains(redacted["note"].(string), "env-secret-999") {
		t.Fatalf("secrets leaked: %+v", redacted)
	}
	if redacted["header"] != "Bearer [redacted]" {
		t.Fatalf("oauth token in Bearer header not redacted: %+v", redacted)
	}
}

func TestBuildMappedRequestRejectsForbiddenHeaders(t *testing.T) {
	source := Server{Endpoint: "https://example.invalid"}
	for _, header := range []string{"Authorization", "Content-Type", "Cookie", "Host"} {
		tool := Tool{
			Method: "GET",
			Path:   "/items",
			HTTP: &HTTPMapping{
				Parameters: []HTTPParameter{{Name: header, In: "header", Style: "simple"}},
				Responses:  map[string]HTTPResponse{"200": {MediaType: "empty"}},
			},
		}
		_, err := BuildMappedRequest(context.Background(), source, tool, map[string]any{"headers": map[string]any{header: "x"}})
		if !errors.Is(err, ErrInvalid) {
			t.Fatalf("header %q: expected ErrInvalid, got %v", header, err)
		}
	}
}

// FuzzBuildMappedRequest checks parameter serialization. For any string value a
// path parameter must stay inside its own path segment (escaped, never
// injecting "/" or changing the host) and a query value must round-trip.
// Arbitrary JSON argument shapes must only ever return errors, never panic.
func FuzzBuildMappedRequest(f *testing.F) {
	f.Add("42", `{"path":{"id":"x"},"query":{"q":["a","b"],"o":{"k":"v"},"p":1.5},"headers":{"X-Test":"v"},"cookies":{"c":{"a":"b"}},"body":{"k":[1]}}`)
	f.Add("a/b", `{"path":{"id":["a","b/c"]}}`)
	f.Add("..", `{"path":{"id":{"a":"b"}}}`)
	f.Add(".", `{"path":{"id":null}}`)
	f.Add("%2e%2e", `{"query":{"o":{"x":[1]}}}`)
	f.Add("a?b#c", `[]`)
	f.Add("../../etc/passwd", `{"path":"x","query":7}`)
	f.Add("@evil.example/", `{"cookies":{"c":[1,"2"]},"headers":{"X-Test":{"a":1}}}`)
	f.Add("\x00\xff ", `{"path":{"id":1e400}}`)
	f.Add("", `{}`)
	server := Server{Endpoint: "https://api.example.invalid/base/"}
	tool := Tool{Method: "POST", Path: "/items/{id}/sub", HTTP: &HTTPMapping{Body: true, Parameters: []HTTPParameter{
		{Name: "id", In: "path", Style: "simple"},
		{Name: "q", In: "query", Style: "form", Explode: true},
		{Name: "o", In: "query", Style: "deepObject", Explode: true},
		{Name: "p", In: "query", Style: "pipeDelimited"},
		{Name: "X-Test", In: "header", Style: "simple"},
		{Name: "c", In: "cookie", Style: "form", Explode: true},
	}}}
	f.Fuzz(func(t *testing.T, value, raw string) {
		var args map[string]any
		if json.Unmarshal([]byte(raw), &args) == nil {
			_, _ = BuildMappedRequest(context.Background(), server, tool, args)
		}
		req, err := BuildMappedRequest(context.Background(), server, tool, map[string]any{
			"path":  map[string]any{"id": value},
			"query": map[string]any{"q": value},
		})
		if value == "." || value == ".." {
			if !errors.Is(err, ErrInvalid) {
				t.Fatalf("relative path segment %q accepted: %v", value, err)
			}
			return
		}
		if err != nil {
			t.Fatalf("valid string arguments rejected for %q: %v", value, err)
		}
		if req.URL.Host != "api.example.invalid" || req.URL.Scheme != "https" || req.URL.User != nil {
			t.Fatalf("value %q changed request authority: %s", value, req.URL)
		}
		if want := "/base/items/" + url.PathEscape(value) + "/sub"; req.URL.EscapedPath() != want {
			t.Fatalf("escaped path %q, want %q", req.URL.EscapedPath(), want)
		}
		if req.URL.Path != "/base/items/"+value+"/sub" {
			t.Fatalf("path value did not round-trip: %q", req.URL.Path)
		}
		if n := strings.Count(req.URL.EscapedPath(), "/"); n != 4 {
			t.Fatalf("path segments injected for %q: %q", value, req.URL.EscapedPath())
		}
		if req.URL.Fragment != "" || req.URL.Query().Get("q") != value {
			t.Fatalf("query value did not round-trip for %q: %q", value, req.URL.RawQuery)
		}
	})
}
