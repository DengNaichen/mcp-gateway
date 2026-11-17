package api

import (
	"net/http"
	"strings"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/model"
)

func (g *server) registerSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := g.Setup()
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireSetup(status))
	})
	mux.HandleFunc("POST /api/setup/source", func(w http.ResponseWriter, r *http.Request) {
		var source apitypes.Server
		if err := decode(w, r, &source); err != nil {
			fail(w, err)
			return
		}
		check, err := g.TestSource(r.Context(), domainServer(source))
		if err != nil {
			if check.Message == "" {
				check.Message = "工具来源配置有误"
			}
			respond(w, 400, apitypes.Error{Error: check.Message, Detail: g.ErrorDetail(err)})
			return
		}
		respond(w, 200, wireCheck(check))
	})
	mux.HandleFunc("POST /api/servers/{id}/tools", func(w http.ResponseWriter, r *http.Request) {
		var input apitypes.EnableToolsRequest
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		if err := g.EnableTools(r.PathValue("id"), input.IDs); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Saved{Saved: true})
	})
	mux.HandleFunc("POST /api/model/ping", func(w http.ResponseWriter, r *http.Request) {
		var input apitypes.PingModelRequest
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		result, err := g.Model().PingModel(r.Context(), domainSettings(input.Settings), input.ProfileID, input.APIKey, input.ClearKey)
		if err != nil {
			respond(w, 400, errorBody("模型连接测试失败", g.ErrorDetail(err), err))
			return
		}
		respond(w, 200, wirePing(result))
	})
	mux.HandleFunc("POST /api/setup/model", func(w http.ResponseWriter, r *http.Request) {
		var input apitypes.SetupModelRequest
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		current, _ := g.Model().Config()
		connectionChanged := strings.TrimSpace(input.BaseURL) != strings.TrimSpace(current.BaseURL) ||
			strings.TrimSpace(input.Model) != strings.TrimSpace(current.Model) ||
			input.TimeoutSeconds != current.TimeoutSeconds ||
			strings.TrimSpace(input.APIKey) != "" ||
			input.ClearKey
		if connectionChanged {
			prof := model.ModelProfile{
				BaseURL:        input.BaseURL,
				Model:          input.Model,
				MaxTools:       input.MaxTools,
				TimeoutSeconds: input.TimeoutSeconds,
			}
			if !g.verifyModelConnection(r.Context(), w, prof, input.APIKey, input.ClearKey) {
				return
			}
		}
		if err := g.SaveSettings(domainSettings(input.Settings), input.APIKey, input.ClearKey); err != nil {
			fail(w, err)
			return
		}
		result, err := g.TestModel(r.Context(), input.Intent)
		if err != nil {
			respond(w, 400, errorBody("配置已保存，模型路由尚未试通", g.ErrorDetail(err), err))
			return
		}
		respond(w, 200, wireDiscoverResult(result))
	})
}
