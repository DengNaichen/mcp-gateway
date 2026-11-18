package api

import (
	"errors"
	"net/http"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/gateway"
)

func (g *server) registerSetup(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/setup/status", func(w http.ResponseWriter, r *http.Request) {
		status, err := g.gw.Setup()
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
		check, err := g.gw.TestSource(r.Context(), domainServer(source))
		if err != nil {
			if check.Message == "" {
				check.Message = "工具来源配置有误"
			}
			respond(w, 400, apitypes.Error{Error: check.Message, Detail: g.gw.ErrorDetail(err)})
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
		if err := g.gw.EnableTools(r.PathValue("id"), input.IDs); err != nil {
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
		result, err := g.gw.PingModel(r.Context(), domainSettings(input.Settings), input.ProfileID, input.APIKey, input.ClearKey)
		if err != nil {
			failWithDetail(w, "模型连接测试失败", g.gw.ErrorDetail(err), err)
			return
		}
		respond(w, 200, apitypes.PingModelResult(result))
	})
	mux.HandleFunc("POST /api/setup/model", func(w http.ResponseWriter, r *http.Request) {
		var input apitypes.SetupModelRequest
		if err := decode(w, r, &input); err != nil {
			fail(w, err)
			return
		}
		result, err := g.gw.SetupModel(r.Context(), domainSettings(input.Settings), input.APIKey, input.ClearKey, input.Intent)
		if err != nil {
			var setupErr *gateway.SetupModelError
			if errors.As(err, &setupErr) {
				msg := "模型连接测试失败，未保存配置"
				if setupErr.Saved {
					msg = "配置已保存，模型路由尚未试通"
				}
				failWithDetail(w, msg, g.gw.ErrorDetail(setupErr.Err), setupErr.Err)
				return
			}
			fail(w, err)
			return
		}
		respond(w, 200, wireDiscoverResult(result))
	})
}
