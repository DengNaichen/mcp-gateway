package upstream

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

// checkBrowserURL only lets OAuth authorization URLs reach the OS opener. The URL
// comes from the remote server's OAuth metadata, so without this a malicious
// server could make the OS open file:// paths or other registered app schemes.
func checkBrowserURL(rawURL string) error {
	u, err := url.Parse(rawURL)
	if err != nil || u.Host == "" {
		return fmt.Errorf("OAuth 授权地址无效")
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		if host := u.Hostname(); host == "localhost" || net.ParseIP(host).IsLoopback() {
			return nil
		}
	}
	return fmt.Errorf("OAuth 授权地址必须使用 https（本机地址可用 http）：%s", u.Redacted())
}

func defaultOpenBrowserURL(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL) //nolint:gosec // G204: fixed binary; rawURL is the remote OAuth authorization URL, restricted to http(s) by checkBrowserURL
	case "windows":
		// rundll32 hands the URL to the protocol handler without cmd.exe parsing,
		// so characters such as & in the query cannot start another command.
		cmd = exec.Command("rundll32", "url.dll,FileProtocolHandler", rawURL) //nolint:gosec // G204: fixed binary; rawURL restricted to http(s) by checkBrowserURL
	default:
		cmd = exec.Command("xdg-open", rawURL) //nolint:gosec // G204: fixed binary; rawURL restricted to http(s) by checkBrowserURL
	}
	return cmd.Start()
}

// An authorization failure is terminal for this connection. The MCP SDK can
// fall back from server/discover to initialize, but replaying browser OAuth on
// that fallback cannot reuse the already-consumed loopback callback.
type oauthHandler struct {
	*auth.AuthorizationCodeHandler
	mu  sync.Mutex
	err error
}

func (h *oauthHandler) Authorize(ctx context.Context, req *http.Request, resp *http.Response) error {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.err != nil {
		_ = resp.Body.Close()
		return h.err
	}
	h.err = h.AuthorizationCodeHandler.Authorize(ctx, req, resp)
	return h.err
}

func (u *Service) oauthHandlerForServer(server Server) (auth.OAuthHandler, func(), error) {
	serverID := oauthSessionKey(server)
	existingTS, err := u.cachedOAuth(serverID)
	if err != nil {
		return nil, nil, err
	}

	if existingTS != nil {
		h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
			RedirectURL: "http://127.0.0.1/callback",
			DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
				Metadata: &oauthex.ClientRegistrationMetadata{
					ClientName:              "MCP Gateway",
					RedirectURIs:            []string{"http://127.0.0.1/callback"},
					GrantTypes:              []string{"authorization_code", "refresh_token"},
					ResponseTypes:           []string{"code"},
					TokenEndpointAuthMethod: "none",
				},
			},
			InitialTokenSource:    existingTS,
			RequestRefreshToken:   true,
			AcceptUnadvertisedIss: true,
			Client:                &http.Client{Timeout: 30 * time.Second},
			AuthorizationCodeFetcher: func(ctx context.Context, _ *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
				if err := u.clearOAuth(serverID); err != nil {
					return nil, err
				}
				return nil, fmt.Errorf("OAuth 授权已失效，请重新触发同步以完成浏览器登录")
			},
		})
		if err != nil {
			return nil, nil, err
		}
		return &oauthHandler{AuthorizationCodeHandler: h}, func() {}, nil
	}

	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, nil, fmt.Errorf("listen oauth callback: %w", err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	callbackURL := fmt.Sprintf("http://127.0.0.1:%d/callback", port)

	type cbOutcome struct {
		res *auth.AuthorizationResult
		err error
	}
	ch := make(chan cbOutcome, 1)
	var sendOnce sync.Once

	writeCallbackPage := func(w http.ResponseWriter, statusCode int, state oauthCallbackState, errDetail string) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Header().Set("Cache-Control", "no-store")
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.WriteHeader(statusCode)
		_, _ = w.Write(renderOAuthCallbackPage(state, server, errDetail))
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errCode := q.Get("error"); errCode != "" {
			desc := q.Get("error_description")
			msg := errCode
			if desc != "" {
				msg += ": " + desc
			}
			writeCallbackPage(w, http.StatusBadRequest, oauthCallbackError, msg)
			sendOnce.Do(func() {
				ch <- cbOutcome{err: fmt.Errorf("oauth authorization failed: %s", msg)}
			})
			return
		}
		code := q.Get("code")
		if code == "" {
			writeCallbackPage(w, http.StatusBadRequest, oauthCallbackInvalid, "")
			return
		}
		writeCallbackPage(w, http.StatusOK, oauthCallbackReceived, "")
		sendOnce.Do(func() {
			ch <- cbOutcome{
				res: &auth.AuthorizationResult{
					Code:  code,
					State: q.Get("state"),
					Iss:   q.Get("iss"),
				},
			}
		})
	})

	srv := &http.Server{Handler: mux, ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = srv.Serve(ln) }()

	var closeOnce sync.Once
	cleanup := func() {
		closeOnce.Do(func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			_ = srv.Shutdown(ctx)
		})
	}

	h, err := auth.NewAuthorizationCodeHandler(&auth.AuthorizationCodeHandlerConfig{
		RedirectURL: callbackURL,
		DynamicClientRegistrationConfig: &auth.DynamicClientRegistrationConfig{
			Metadata: &oauthex.ClientRegistrationMetadata{
				ClientName:              "MCP Gateway",
				RedirectURIs:            []string{callbackURL},
				GrantTypes:              []string{"authorization_code", "refresh_token"},
				ResponseTypes:           []string{"code"},
				TokenEndpointAuthMethod: "none",
			},
		},
		RequestRefreshToken:   true,
		AcceptUnadvertisedIss: true,
		Client:                &http.Client{Timeout: 30 * time.Second},
		AuthorizationCodeFetcher: func(ctx context.Context, args *auth.AuthorizationArgs) (*auth.AuthorizationResult, error) {
			if err := checkBrowserURL(args.URL); err != nil {
				return nil, err
			}
			if err := u.openBrowser(args.URL); err != nil {
				return nil, fmt.Errorf("打开浏览器进行 OAuth 登录失败: %w", err)
			}
			select {
			case out := <-ch:
				return out.res, out.err
			case <-ctx.Done():
				return nil, fmt.Errorf("等待浏览器 OAuth 登录超时或已取消: %w", ctx.Err())
			}
		},
		NewTokenSource: func(ctx context.Context, cfg *oauth2.Config, tok *oauth2.Token) (oauth2.TokenSource, error) {
			ts, err := u.newOAuthSource(serverID, cfg, tok)
			if err != nil {
				return nil, err
			}
			return ts, nil
		},
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return &oauthHandler{AuthorizationCodeHandler: h}, cleanup, nil
}
