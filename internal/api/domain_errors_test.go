package api

import (
	"encoding/json"
	"maps"
	"net/http/httptest"
	"testing"

	"gatewaylab/internal/fault"
	"gatewaylab/internal/model"
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
}
