package api

import (
	"context"
	"errors"
	"net/http"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/model"
)

func (g *server) verifyModelConnection(ctx context.Context, w http.ResponseWriter, p model.ModelProfile, apiKey string, clearKey bool) bool {
	if err := g.gw.Model().VerifyModelConnection(ctx, p, apiKey, clearKey); err != nil {
		if errors.Is(err, model.ErrInvalid) {
			fail(w, err)
			return false
		}
		failWithDetail(w, "模型连接测试失败，未保存配置", g.gw.ErrorDetail(err), err)
		return false
	}
	return true
}

func (g *server) registerModel(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/settings", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.SaveSettingsRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if err := g.gw.SaveSettings(domainSettings(in.Settings), in.APIKey, in.ClearKey); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Saved{Saved: true})
	})
	mux.HandleFunc("PUT /api/model-profiles", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.SaveModelProfileRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		if !g.verifyModelConnection(r.Context(), w, domainProfile(in.ModelProfile), in.APIKey, in.ClearKey) {
			return
		}
		saved, err := g.gw.SaveModelProfile(domainProfile(in.ModelProfile), in.APIKey, in.ClearKey)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireProfile(saved))
	})
	mux.HandleFunc("POST /api/model-profiles/{id}/activate", func(w http.ResponseWriter, r *http.Request) {
		activated, err := g.gw.ActivateModelProfile(r.PathValue("id"))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireProfile(activated))
	})
	mux.HandleFunc("DELETE /api/model-profiles/{id}", func(w http.ResponseWriter, r *http.Request) {
		if err := g.gw.DeleteModelProfile(r.PathValue("id")); err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, apitypes.Deleted{Deleted: true})
	})
}
