package upstream

import (
	"context"
	"errors"
	"net/http"
	"os"
	"testing"
	"time"

	"golang.org/x/oauth2"
)

type diagnosticTransport struct {
	base http.RoundTripper
	t    *testing.T
}

func (d diagnosticTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	start := time.Now()
	resp, err := d.base.RoundTrip(r)
	status := 0
	if resp != nil {
		status = resp.StatusCode
	}
	d.t.Logf("%s %s%s status=%d elapsed=%s network_error=%t", r.Method, r.URL.Host, r.URL.Path, status, time.Since(start), err != nil)
	return resp, err
}

func TestDiagnosticSupabaseOAuth(t *testing.T) {
	if os.Getenv("GATEWAY_DIAG_SUPABASE") != "1" {
		t.Skip("manual OAuth diagnostic")
	}
	previous := http.DefaultTransport
	http.DefaultTransport = diagnosticTransport{base: previous, t: t}
	defer func() { http.DefaultTransport = previous }()
	svc := New(Config{})
	// Use an isolated in-memory service: never read or replace the user's saved
	// OAuth session. Log statuses and timings, never headers, bodies or query strings.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	start := time.Now()
	session, err := svc.connectMCP(ctx, ctx, Server{ID: "supabase-diag", Endpoint: "https://mcp.supabase.com/mcp", Transport: "http", OAuth: true})
	if err != nil {
		var retrieve *oauth2.RetrieveError
		if errors.As(err, &retrieve) {
			t.Logf("token exchange HTTP status=%d", retrieve.Response.StatusCode)
		}
		t.Fatalf("OAuth/MCP connection failed after %s; raw errors withheld to avoid logging credentials", time.Since(start))
	}
	defer closeSession(session)
	tools, err := session.ListTools(ctx, nil)
	if err != nil {
		t.Fatal("authenticated tools/list failed; raw errors withheld to avoid logging credentials")
	}
	t.Logf("OAuth and MCP connected, tools=%d", len(tools.Tools))
}
