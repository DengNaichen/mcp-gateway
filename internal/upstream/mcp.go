package upstream

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net/http"
	"os"
	"os/exec"
	"strings"

	"gatewaylab/internal/procutil"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/mcp"
)

type callHeadersKey struct{}

func withCallHeaders(ctx context.Context, headers map[string]string) context.Context {
	if len(headers) == 0 {
		return ctx
	}
	return context.WithValue(ctx, callHeadersKey{}, headers)
}

func callHeadersFromContext(ctx context.Context) map[string]string {
	headers, _ := ctx.Value(callHeadersKey{}).(map[string]string)
	return headers
}

// mcpParamHeaders extracts per-call Mcp-Param-* HTTP headers for schema properties
// annotated with "x-mcp-header" (for example GitHub Copilot MCP's owner/repo).
func mcpParamHeaders(schema, args map[string]any) map[string]string {
	props, ok := schema["properties"].(map[string]any)
	if !ok || len(args) == 0 {
		return nil
	}
	var headers map[string]string
	for propName, rawProp := range props {
		propMap, ok := rawProp.(map[string]any)
		if !ok {
			continue
		}
		headerName, ok := propMap["x-mcp-header"].(string)
		headerName = strings.TrimSpace(headerName)
		if !ok || headerName == "" || strings.ContainsAny(headerName, "\r\n:") {
			continue
		}
		val, exists := args[propName]
		if !exists || val == nil {
			continue
		}
		strVal := fmt.Sprint(val)
		if strVal == "" || strings.ContainsAny(strVal, "\r\n") {
			continue
		}
		if headers == nil {
			headers = make(map[string]string)
		}
		headers["Mcp-Param-"+headerName] = strVal
	}
	return headers
}

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
	for k, v := range callHeadersFromContext(req.Context()) {
		r.Header.Set(k, v)
	}
	return t.base.RoundTrip(r)
}

func (u *Service) connectMCP(ctx, processContext context.Context, s Server) (*mcp.ClientSession, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	client := mcp.NewClient(&mcp.Implementation{Name: "gateway-lab", Version: "0.0.1"}, nil)
	if err := CheckEnvironment(s, u.serverLookup(s)); err != nil {
		return nil, err
	}
	if s.Transport == "stdio" {
		command := exec.CommandContext(processContext, s.Command, s.Args...) //nolint:gosec // G204: command and arguments are fixed or come from the user's own local configuration; launching them is this component's purpose
		command.Dir = s.WorkDir
		command.Env = procutil.ProcessEnvironment(nil)
		for key, value := range s.Env {
			command.Env = append(command.Env, key+"="+os.Expand(value, u.serverLookup(s)))
		}
		command.Stderr = os.Stderr
		procutil.ConfigureProcess(command)
		session, err := client.Connect(ctx, &mcp.CommandTransport{Command: command}, nil)
		if err != nil {
			return nil, fmt.Errorf("start stdio mcp server: %w", err)
		}
		return session, nil
	}
	httpClient := &http.Client{Transport: headerTransport{headers: s.Headers, expand: func(value string) string { return os.Expand(value, u.serverLookup(s)) }, base: http.DefaultTransport}, CheckRedirect: noRedirect}
	var oauthHandler auth.OAuthHandler
	if s.OAuth && s.Headers["Authorization"] == "" {
		h, cleanup, err := u.oauthHandlerForServer(s)
		if err != nil {
			return nil, fmt.Errorf("setup oauth handler: %w", err)
		}
		defer cleanup()
		oauthHandler = h
	}
	session, err := client.Connect(ctx, &mcp.StreamableClientTransport{
		Endpoint:             s.Endpoint,
		HTTPClient:           httpClient,
		OAuthHandler:         oauthHandler,
		DisableStandaloneSSE: true,
		MaxRetries:           -1,
	}, nil)
	if err != nil {
		return nil, fmt.Errorf("connect native mcp server: %w", err)
	}
	return session, nil
}

func closeSession(session *mcp.ClientSession) {
	if err := session.Close(); err != nil {
		slog.Debug("close mcp session", "error", err)
	}
}

type stdioSession struct {
	fingerprint string
	session     *mcp.ClientSession
	cancel      context.CancelFunc
}

// Keep a local process alive across discovery and calls so stateful tools can
// preserve their session. Reconfiguration or gateway shutdown closes it.
func (u *Service) Session(ctx context.Context, s Server) (*mcp.ClientSession, func(), error) {
	if s.Transport != "stdio" {
		session, err := u.connectMCP(ctx, ctx, s)
		if err != nil {
			return nil, nil, err
		}
		return session, func() { closeSession(session) }, nil
	}
	fingerprint, err := Fingerprint(s, u.serverLookup(s))
	if err != nil {
		return nil, nil, err
	}
	u.sessionMu.Lock()
	defer u.sessionMu.Unlock()
	if u.sessions == nil {
		u.sessions = map[string]stdioSession{}
	}
	if current, ok := u.sessions[s.ID]; ok {
		if current.fingerprint == fingerprint {
			return current.session, func() {}, nil
		}
		closeSession(current.session)
		current.cancel()
		delete(u.sessions, s.ID)
	}
	processContext, cancel := context.WithCancel(context.Background())
	session, err := u.connectMCP(ctx, processContext, s)
	if err != nil {
		cancel()
		return nil, nil, err
	}
	u.sessions[s.ID] = stdioSession{fingerprint, session, cancel}
	return session, func() {}, nil
}

func (u *Service) ForgetClosedSession(id string, session *mcp.ClientSession, err error) {
	if !errors.Is(err, mcp.ErrConnectionClosed) {
		return
	}
	u.sessionMu.Lock()
	defer u.sessionMu.Unlock()
	if current, ok := u.sessions[id]; ok && current.session == session {
		closeSession(current.session)
		current.cancel()
		delete(u.sessions, id)
	}
}

func (u *Service) Close() error {
	u.sessionMu.Lock()
	defer u.sessionMu.Unlock()
	var result error
	for id, current := range u.sessions {
		if err := current.session.Close(); err != nil {
			result = errors.Join(result, fmt.Errorf("close stdio server %s: %w", id, err))
		}
		current.cancel()
		delete(u.sessions, id)
	}
	return result
}

func (u *Service) CallMCP(ctx context.Context, s Server, t Tool, args map[string]any) (any, error) {
	session, release, err := u.Session(ctx, s)
	if err != nil {
		return nil, err
	}
	defer release()
	callCtx := withCallHeaders(ctx, mcpParamHeaders(t.InputSchema, args))
	result, err := session.CallTool(callCtx, &mcp.CallToolParams{Name: t.Name, Arguments: args})
	if err != nil {
		u.ForgetClosedSession(s.ID, session, err)
		return nil, fmt.Errorf("call native mcp tool: %w", err)
	}
	if result.IsError {
		messages := []string{}
		for _, content := range result.Content {
			if text, ok := content.(*mcp.TextContent); ok {
				messages = append(messages, text.Text)
			}
		}
		message := strings.Join(messages, "\n")
		if len(message) > 4000 {
			message = message[:4000]
		}
		return nil, fmt.Errorf("native mcp tool returned an execution error: %s", message)
	}
	if result.StructuredContent != nil {
		return result.StructuredContent, nil
	}
	if len(result.Content) == 1 {
		if content, ok := result.Content[0].(*mcp.TextContent); ok {
			var data any
			if err := json.Unmarshal([]byte(content.Text), &data); err == nil {
				return data, nil
			}
			return map[string]any{"text": content.Text}, nil
		}
	}
	return map[string]any{"content": result.Content}, nil
}
