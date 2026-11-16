package api

import (
	"net/http"

	"gatewaylab/internal/model"
	"gatewaylab/internal/registry"
)

func (g *server) registerSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := g.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, status)
	})
	mux.HandleFunc("POST /api/setup/source", func(w http.ResponseWriter, r *http.Request) {
		var source registry.Server
		if err := decode(w, r, &source); err != nil {
			fail(w, err)
			return
		}
		check, err := g.TestSource(r.Context(), source)
		if err != nil {
			if check.Message == "" {
				check.Message = "工具来源配置有误"
			}
			respond(w, 400, map[string]any{"error": check.Message, "detail": g.ErrorDetail(err)})
			return
		}
		respond(w, 200, check)
	})
	mux.HandleFunc("POST /api/servers/{id}/tools", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			IDs []string `json:"ids"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		if err := g.EnableTools(r.PathValue("id"), input.IDs); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, map[string]bool{"saved": true})
	})
	mux.HandleFunc("POST /api/model/ping", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			model.Settings
			ProfileID string `json:"profile_id"`
			APIKey    string `json:"api_key"`
			ClearKey  bool   `json:"clear_key"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		result, err := g.Model().PingModel(r.Context(), input.Settings, input.ProfileID, input.APIKey, input.ClearKey)
		if err != nil {
			respond(w, 400, map[string]any{"error": "模型连接测试失败", "detail": g.ErrorDetail(err)})
			return
		}
		respond(w, 200, result)
	})
	mux.HandleFunc("POST /api/setup/model", func(w http.ResponseWriter, r *http.Request) {
		var input struct {
			model.Settings
			APIKey   string `json:"api_key"`
			ClearKey bool   `json:"clear_key"`
			Intent   string `json:"intent"`
		}
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		if err := g.SaveSettings(input.Settings, input.APIKey, input.ClearKey); err != nil {
			fail(w, err)
			return
		}
		result, err := g.TestModel(r.Context(), input.Intent)
		if err != nil {
			respond(w, 400, map[string]any{"error": "配置已保存，模型路由尚未试通", "detail": g.ErrorDetail(err)})
			return
		}
		respond(w, 200, result)
	})
}
