package api

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"testing"

	"gatewaylab/internal/gateway"
)

// Icons may be SVG; opened directly in a browser they must not run script on the
// gateway origin.
func TestMarketIconServedWithSandboxCSP(t *testing.T) {
	dir := t.TempDir()
	g, err := gateway.NewWithOptions(dir, "http://127.0.0.1:0", gateway.Options{})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = g.Close() })

	id, icon := "", ""
	for _, s := range g.Market().MarketServers(context.Background(), "").Items {
		if s.Icon != "" {
			id, icon = s.ID, s.Icon
			break
		}
	}
	if id == "" {
		t.Skip("curated market has no icons")
	}
	sum := sha256.Sum256([]byte(icon))
	key := hex.EncodeToString(sum[:16])
	cache := filepath.Join(dir, "market-icons")
	if err := os.MkdirAll(cache, 0o700); err != nil {
		t.Fatal(err)
	}
	svg := `<svg xmlns="http://www.w3.org/2000/svg"><script>alert(1)</script></svg>`
	if err := os.WriteFile(filepath.Join(cache, key+".bin"), []byte(svg), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(cache, key+".type"), []byte("image/svg+xml"), 0o600); err != nil {
		t.Fatal(err)
	}

	req := httptest.NewRequest(http.MethodGet, "/api/market/icon?id="+url.QueryEscape(id), nil)
	req.Host = "127.0.0.1"
	rec := httptest.NewRecorder()
	Handler(g).ServeHTTP(rec, req)
	if rec.Code != http.StatusOK {
		t.Fatalf("status = %d, body = %s", rec.Code, rec.Body.String())
	}
	if got, want := rec.Header().Get("Content-Security-Policy"), "default-src 'none'; style-src 'unsafe-inline'; sandbox"; got != want {
		t.Fatalf("Content-Security-Policy = %q, want %q", got, want)
	}
	if got := rec.Header().Get("X-Content-Type-Options"); got != "nosniff" {
		t.Fatalf("X-Content-Type-Options = %q, want nosniff", got)
	}
}
