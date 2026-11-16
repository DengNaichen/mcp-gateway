// Package contracttest provides loopback fixtures for outbound protocol acceptance.
// These synthetic peers do not serve Gateway API routes or receive live credentials.
package contracttest

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"sync/atomic"

	"github.com/modelcontextprotocol/go-sdk/mcp"

	"gatewaylab/internal/input"
	"gatewaylab/internal/mcputil"
)

type Endpoint struct {
	URL   string
	Close func()
}
type Request struct {
	Method, Path string
	Query        map[string][]string
	Headers      map[string][]string
	Body         func() ([]byte, error)
}

func (r Request) Header(key string) string { return http.Header(r.Headers).Get(key) }

type Response struct {
	Status  int
	Headers map[string]string
	Body    []byte
	Text    *string
}

// The acceptance state lock covers validation and response writing as one exchange.
func HTTP(lock sync.Locker, handle func(Request) Response, writeFailed func()) Endpoint {
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		lock.Lock()
		defer lock.Unlock()
		out := handle(Request{r.Method, r.URL.EscapedPath(), r.URL.Query(), r.Header, func() ([]byte, error) { return input.ReadBounded(r.Body, 1<<20) }})
		var err error
		for key, value := range out.Headers {
			w.Header().Set(key, value)
		}
		if out.Text != nil {
			w.Header().Set("Content-Type", "text/plain")
		} else if len(out.Body) > 0 {
			w.Header().Set("Content-Type", "application/json")
		}
		w.WriteHeader(out.Status)
		if out.Text != nil {
			_, err = io.WriteString(w, *out.Text)
		} else {
			_, err = w.Write(out.Body)
		}
		if err != nil {
			writeFailed()
		}
	}))
	return Endpoint{mock.URL, mock.Close}
}
func MCP(name string, schema map[string]any, data json.RawMessage, isError bool) (Endpoint, func() int, error) {
	native := mcp.NewServer(&mcp.Implementation{Name: "acceptance", Version: "1"}, nil)
	var fixture any
	if len(data) > 0 {
		if err := json.Unmarshal(data, &fixture); err != nil {
			return Endpoint{}, nil, fmt.Errorf("decode mcp fixture: %w", err)
		}
	}
	var calls atomic.Int32
	native.AddTool(&mcp.Tool{Name: name, InputSchema: schema}, func(ctx context.Context, req *mcp.CallToolRequest) (*mcp.CallToolResult, error) {
		calls.Add(1)
		if isError {
			return &mcp.CallToolResult{IsError: true, Content: []mcp.Content{&mcp.TextContent{Text: "fixture upstream error"}}}, nil
		}
		return mcputil.Result(fixture)
	})
	mock := httptest.NewServer(mcp.NewStreamableHTTPHandler(func(*http.Request) *mcp.Server { return native }, &mcp.StreamableHTTPOptions{Stateless: true, JSONResponse: true}))
	return Endpoint{mock.URL, mock.Close}, func() int { return int(calls.Load()) }, nil
}
