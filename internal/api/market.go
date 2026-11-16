package api

import (
	"net/http"

	"gatewaylab/internal/market"
)

func (g *server) registerMarket(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/market", func(w http.ResponseWriter, r *http.Request) {
		res := g.Market().MarketServers(r.Context(), r.URL.Query().Get("q"))
		respond(w, 200, res)
	})
	mux.HandleFunc("GET /api/market/icon", func(w http.ResponseWriter, r *http.Request) {
		data, ct, err := g.Market().MarketIcon(r.Context(), r.URL.Query().Get("id"), r.URL.Query().Get("url"))
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(200)
		_, _ = w.Write(data) //nolint:gosec // G705: icon bytes are served with the upstream-verified Content-Type plus nosniff, not rendered as HTML
	})
	mux.HandleFunc("POST /api/market/install", func(w http.ResponseWriter, r *http.Request) {
		var in market.InstallMarketRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := g.Market().InstallMarketServer(r.Context(), in)
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, out)
	})
}
