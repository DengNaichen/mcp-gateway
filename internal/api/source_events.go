package api

import (
	"net/http"

	"gatewaylab/internal/apitypes"
	"gatewaylab/internal/input"
)

func (g *server) registerSourceEvents(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/maintenance/sources/{id}/webhook/{provider}", func(w http.ResponseWriter, r *http.Request) {
		// Preserve source validation before reading the bounded request body.
		source, ok := g.maintenance.Source(r.PathValue("id"))
		if !ok || source.Repository == nil || !source.Repository.EventsEnabled {
			fail(w, problem(404, "此来源未开启仓库事件", nil))
			return
		}
		body, err := input.ReadBounded(r.Body, 256<<10)
		if err != nil {
			fail(w, problem(413, "事件内容过大", err))
			return
		}
		result, err := g.maintenance.ReceiveSourceEvent(source, r.PathValue("provider"), r.Header, body)
		if err != nil {
			fail(w, err)
			return
		}
		if result.Ignored {
			respond(w, 200, apitypes.WebhookResult{Ignored: true})
			return
		}
		if result.Duplicate {
			respond(w, 200, apitypes.WebhookResult{Accepted: true, Duplicate: true, RunID: result.RunID})
			return
		}
		respond(w, 202, apitypes.WebhookResult{Accepted: true, Pending: true})
	})
}
