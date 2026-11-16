package api

import (
	"log/slog"
	"net/http"
	"os"
	"strings"
	"time"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

func (g *server) handler() http.Handler {
	mux := http.NewServeMux()
	g.registerBootstrap(mux)
	g.registerModel(mux)
	g.registerMarket(mux)
	g.registerServers(mux)
	g.registerExecute(mux)
	g.registerEvents(mux)
	g.registerSetup(mux)
	g.registerMaintenance(mux)
	g.registerCapabilities(mux)
	g.registerSourceEvents(mux)
	g.registerAccess(mux)
	g.registerScopes(mux)
	mcpServer := g.mcpServer()
	mux.Handle("/mcp", g.trackMCP(mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return mcpServer }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true})))
	demo := g.demoServer()
	mux.Handle("/demo/mcp", mcp.NewStreamableHTTPHandler(func(r *http.Request) *mcp.Server { return demo }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	mux.HandleFunc("GET /demo/api/{name}", g.demoHTTP)

	mux.HandleFunc("/api/", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 404, map[string]string{"error": "API endpoint not found"})
	})
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		respond(w, 404, map[string]string{"error": "not found", "mcp": "/mcp", "health": "/api/health"})
	})
	protection := http.NewCrossOriginProtection()
	origins := []string{}
	if configured := os.Getenv("GATEWAY_TRUSTED_ORIGINS"); configured != "" {
		origins = append(origins, strings.Split(configured, ",")...)
	}
	for _, origin := range origins {
		if err := protection.AddTrustedOrigin(origin); err != nil {
			slog.Error("configure trusted origin", "error", err)
		}
	}
	return protection.Handler(g.protectAccess(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()
		mux.ServeHTTP(w, r)
		slog.Info("http request", "method", r.Method, "path", r.URL.Path, "duration_ms", time.Since(start).Milliseconds())
	})))
}
