package gateway

import (
	"encoding/json"
	"net/http/httptest"
	"strings"
	"testing"
)

// Exercise the actual transport adapters after domain errors lost HTTP status fields.
func TestPolicyDomainHTTPCompatibility(t *testing.T) {
	g, err := New(t.TempDir(), "http://127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer g.Close()
	handler := g.Handler()
	for _, tc := range []struct {
		method, path, body string
		status             int
		message            string
	}{
		{"PUT", "/api/model-profiles", `{}`, 400, "请填写路由模型名称"},
		{"POST", "/api/model-profiles/missing/activate", ``, 404, "未找到指定的模型配置"},
		{"PUT", "/api/settings", `{"base_url":"https://example.com","model":"test","max_tools":0,"timeout_seconds":30}`, 400, "返回工具数需为 1–12"},
		{"PUT", "/api/scopes/rules", `{}`, 400, "repo_key 不能为空"},
	} {
		t.Run(tc.path+tc.message, func(t *testing.T) {
			out := httptest.NewRecorder()
			handler.ServeHTTP(out, httptest.NewRequest(tc.method, "http://127.0.0.1"+tc.path, strings.NewReader(tc.body)))
			var got map[string]any
			if err := json.Unmarshal(out.Body.Bytes(), &got); err != nil {
				t.Fatal(err)
			}
			if out.Code != tc.status || got["error"] != tc.message || len(got) != 1 {
				t.Fatalf("HTTP %d %s", out.Code, out.Body.String())
			}
		})
	}
	out := httptest.NewRecorder()
	handler.ServeHTTP(out, httptest.NewRequest("GET", "http://127.0.0.1/api/events", nil))
	if out.Code != 200 || strings.TrimSpace(out.Body.String()) != "[]" {
		t.Fatalf("empty events changed shape: %d %s", out.Code, out.Body.String())
	}
}
