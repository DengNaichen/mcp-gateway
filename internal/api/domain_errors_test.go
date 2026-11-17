package api

import (
	"encoding/json"
	"maps"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
	"gatewaylab/internal/routing"
	"gatewaylab/internal/scope"
	"gatewaylab/internal/upstream"
)

func TestDomainErrorHTTPCompatibility(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"model-invalid", &model.Error{Kind: model.ErrInvalid, Message: "配置无效"}, 400, "配置无效"},
		{"model-missing", &model.Error{Kind: model.ErrNotFound, Message: "配置不存在"}, 404, "配置不存在"},
		{"model-upstream", &model.Error{Kind: model.ErrUpstream, Message: "模型不可达"}, 502, "模型不可达"},
		{"routing-invalid", &routing.Error{Kind: routing.ErrInvalid, Message: "任务无效"}, 400, "任务无效"},
		{"routing-answer", &routing.Error{Kind: routing.ErrModelResponse, Message: "回答无效"}, 502, "回答无效"},
		{"upstream-invalid", &upstream.Error{Kind: upstream.ErrInvalid, Message: "服务无效"}, 400, "服务无效"},
		{"upstream-failed", &upstream.Error{Kind: upstream.ErrUpstream, Message: "服务不可达"}, 502, "服务不可达"},
		{"scope-invalid", &scope.Error{Kind: scope.ErrInvalid, Message: "范围无效"}, 400, "范围无效"},
		{"scope-conflict", &scope.Error{Kind: scope.ErrConflict, Message: "范围变化"}, 409, "范围变化"},
		{"outer-invoke", fault.New(fault.Upstream, "下游工具调用失败", &model.Error{Kind: model.ErrInvalid, Message: "隐藏的原因"}), 502, "下游工具调用失败"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			w := httptest.NewRecorder()
			fail(w, tc.err)
			want := string(mustJSON(t, map[string]string{"error": tc.body})) + "\n"
			if w.Code != tc.status || w.Body.String() != want {
				t.Fatalf("got %d %s; want %d %s", w.Code, w.Body.String(), tc.status, want)
			}
		})
	}
	t.Run("scope-code", func(t *testing.T) {
		w := httptest.NewRecorder()
		fail(w, &scope.Error{Kind: scope.ErrForbidden, Code: "scope_required", Message: "需要范围"})
		var got map[string]string
		if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
			t.Fatal(err)
		}
		want := map[string]string{"code": "scope_required", "error": "需要范围"}
		if w.Code != 403 || !maps.Equal(got, want) {
			t.Fatalf("%d %s", w.Code, w.Body.String())
		}
	})
	t.Run("fault-detailed", func(t *testing.T) {
		for _, tc := range []struct {
			name       string
			err        error
			status     int
			code       string
			retryAfter string
		}{
			{"timeout", fault.NewDetailed(fault.Timeout, "gw_timeout", "上游超时", 0, nil), 504, "gw_timeout", ""},
			{"unavailable", fault.NewDetailed(fault.Unavailable, "gw_unavailable", "服务不可用", 3*time.Second, nil), 503, "gw_unavailable", "3"},
		} {
			t.Run(tc.name, func(t *testing.T) {
				w := httptest.NewRecorder()
				fail(w, tc.err)
				var got map[string]any
				if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
					t.Fatal(err)
				}
				if w.Code != tc.status || got["code"] != tc.code || w.Header().Get("Retry-After") != tc.retryAfter {
					t.Fatalf("got %d %v Retry-After=%q", w.Code, got, w.Header().Get("Retry-After"))
				}
			})
		}
		if app := httpError(&sandbox.ValidationError{Message: "沙箱路径无效"}); app == nil || app.status != 400 {
			t.Fatalf("expected 400 for sandbox validation error, got %+v", app)
		}
	})
	t.Run("invoke-upstream-validation-stays-400", func(t *testing.T) {
		g, _ := testGateway(t)
		if err := g.Registry().SaveServer(registry.Server{
			ID: "svc", Name: "Svc", Kind: "http", Endpoint: "http://127.0.0.1:1", Enabled: true,
		}); err != nil {
			t.Fatal(err)
		}
		if err := g.Registry().SaveTool(registry.Tool{
			ID:          "svc.get",
			ServerID:    "svc",
			Name:        "get",
			Description: "get item",
			Enabled:     true,
			Actors:      []string{"developer"},
			Method:      "GET",
			Path:        "/items/{id}",
			InputSchema: objectSchema(map[string]any{"path": objectSchema(map[string]any{"id": map[string]any{"type": "string"}}, "id")}, "path"),
			HTTP: &registry.HTTPMapping{
				Parameters: []registry.HTTPParameter{{Name: "id", In: "path", Style: "simple"}},
				Responses:  map[string]registry.HTTPResponse{"200": {MediaType: "application/json"}},
			},
		}); err != nil {
			t.Fatal(err)
		}
		w := httptest.NewRecorder()
		Handler(g).ServeHTTP(w, httptest.NewRequest("POST", "http://127.0.0.1/api/invoke", strings.NewReader(`{"tool_id":"svc.get","arguments":{"path":{"id":".."}}}`)))
		if w.Code != 400 {
			t.Fatalf("expected 400 for upstream path validation error, got %d %s", w.Code, w.Body.String())
		}
	})
}
