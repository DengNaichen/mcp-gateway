package api

import (
	"net/http"

	"gatewaylab/internal/apitypes"
)

func (g *server) registerMarket(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/market", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 200, wireMarketSearch(g.market.MarketServers(r.Context(), r.URL.Query().Get("q"))))
	})
	mux.HandleFunc("GET /api/market/icon", func(w http.ResponseWriter, r *http.Request) {
		data, ct, err := g.market.MarketIcon(r.Context(), r.URL.Query().Get("id"), r.URL.Query().Get("url"))
		if err != nil {
			fail(w, err)
			return
		}
		w.Header().Set("Content-Type", ct)
		w.Header().Set("Cache-Control", "public, max-age=86400")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		// Icons may be SVG. If one is opened directly in a browser, the sandbox CSP
		// keeps any embedded script from running on the gateway's own origin.
		w.Header().Set("Content-Security-Policy", "default-src 'none'; style-src 'unsafe-inline'; sandbox")
		w.WriteHeader(200)
		_, _ = w.Write(data) //nolint:gosec // G705: image/* only, nosniff, and a sandbox CSP block script execution
	})
	mux.HandleFunc("POST /api/market/install", func(w http.ResponseWriter, r *http.Request) {
		var in apitypes.InstallMarketRequest
		if err := decode(w, r, &in); err != nil {
			fail(w, err)
			return
		}
		out, err := g.market.InstallMarketServer(r.Context(), domainInstallRequest(in))
		if err != nil {
			fail(w, err)
			return
		}
		respond(w, 200, wireInstallResult(out))
	})
}
