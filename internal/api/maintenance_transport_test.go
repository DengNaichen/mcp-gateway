package api

import (
	"errors"
	"net/http/httptest"
	"testing"

	"gatewaylab/internal/maintenance"
	"gatewaylab/internal/maintenance/sandbox"
	"gatewaylab/internal/openapi"
	"gatewaylab/internal/registry"
)

func TestMaintenanceDomainErrorHTTPMapping(t *testing.T) {
	for _, tc := range []struct {
		name   string
		err    error
		status int
		body   string
	}{
		{"invalid", &maintenance.Error{Kind: maintenance.ErrInvalid, Message: "维护格式必须是 pi、openapi 或 native"}, 400, "维护格式必须是 pi、openapi 或 native"},
		{"missing", &maintenance.Error{Kind: maintenance.ErrNotFound, Message: "任务不存在"}, 404, "任务不存在"},
		{"conflict", &maintenance.Error{Kind: maintenance.ErrConflict, Message: "该服务已有维护任务"}, 409, "该服务已有维护任务"},
		{"stopped", &maintenance.Error{Kind: maintenance.ErrUnavailable, Message: "维护运行时正在停止"}, 503, "维护运行时正在停止"},
		{"signature", &maintenance.Error{Kind: maintenance.ErrUnauthorized, Message: "事件签名或密钥无效"}, 401, "事件签名或密钥无效"},
		{"project", &maintenance.Error{Kind: maintenance.ErrForbidden, Message: "事件仓库与配置不匹配"}, 403, "事件仓库与配置不匹配"},
		{"schema", &registry.ValidationError{Message: "工具需要 input_schema"}, 400, "工具需要 input_schema"},
		{"openapi", &openapi.ValidationError{Message: "不支持的 OpenAPI 版本"}, 400, "不支持的 OpenAPI 版本"},
		{"sandbox", &sandbox.ValidationError{Message: "沙箱路径不能包含逗号或换行"}, 400, "沙箱路径不能包含逗号或换行"},
		{"outer-error", problem(502, "下游工具调用失败", &maintenance.Error{Kind: maintenance.ErrInvalid, Message: "适配器版本或路径无效"}), 502, "下游工具调用失败"},
		{"internal", errors.New("private database error"), 500, "操作失败，请查看后端日志"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			response := httptest.NewRecorder()
			fail(response, tc.err)
			want := string(mustJSON(t, map[string]string{"error": tc.body})) + "\n"
			if response.Code != tc.status || response.Body.String() != want {
				t.Fatalf("got %d %s; want %d %s", response.Code, response.Body.String(), tc.status, want)
			}
		})
	}
}
