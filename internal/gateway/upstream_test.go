package gateway

import (
	"net/http"
	"os"

	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type headerTransport struct {
	headers map[string]string
	expand  func(string) string
	base    http.RoundTripper
}

func (t headerTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	r := req.Clone(req.Context())
	expand := t.expand
	if expand == nil {
		expand = os.ExpandEnv
	}
	for k, v := range t.headers {
		r.Header.Set(k, expand(v))
	}
	return t.base.RoundTrip(r)
}

func closeSession(s *mcp.ClientSession) { _ = s.Close() }
