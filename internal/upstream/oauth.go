package upstream

import (
	"context"
	"fmt"
	"html"
	"net"
	"net/http"
	"os/exec"
	"runtime"
	"sync"
	"time"

	"github.com/modelcontextprotocol/go-sdk/auth"
	"github.com/modelcontextprotocol/go-sdk/oauthex"
	"golang.org/x/oauth2"
)

func defaultOpenBrowserURL(rawURL string) error {
	var cmd *exec.Cmd
	switch runtime.GOOS {
	case "darwin":
		cmd = exec.Command("open", rawURL)
	case "windows":
		cmd = exec.Command("cmd", "/c", "start", "", rawURL)
	default:
		cmd = exec.Command("xdg-open", rawURL)
	}
	return cmd.Start()
}

func (u *Service) oauthHandlerForServer(serverID string) (*auth.AuthorizationCodeHandler, func(), error) {
	u.oauthMu.RLock()
	existingTS := u.oauthTokens[serverID]
	u.oauthMu.RUnlock()

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
				u.oauthMu.Lock()
				delete(u.oauthTokens, serverID)
				u.oauthMu.Unlock()
				return nil, fmt.Errorf("OAuth 授权已失效，请重新触发同步以完成浏览器登录")
			},
		})
		if err != nil {
			return nil, nil, err
		}
		return h, func() {}, nil
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

	mux := http.NewServeMux()
	mux.HandleFunc("/callback", func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if errCode := q.Get("error"); errCode != "" {
			desc := q.Get("error_description")
			msg := errCode
			if desc != "" {
				msg += ": " + desc
			}
			w.Header().Set("Content-Type", "text/html; charset=utf-8")
			w.WriteHeader(http.StatusBadRequest)
			_, _ = fmt.Fprintf(w, "<!doctype html><html><body style=\"font-family:system-ui;padding:40px;\"><h3>授权未完成</h3><p>%s</p></body></html>", html.EscapeString(msg))
			sendOnce.Do(func() {
				ch <- cbOutcome{err: fmt.Errorf("oauth authorization failed: %s", msg)}
			})
			return
		}
		code := q.Get("code")
		if code == "" {
			http.Error(w, "missing code", http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		_, _ = w.Write([]byte(`<!doctype html><html><body style="font-family:system-ui;padding:40px;color:#111827;"><h3>授权成功</h3><p>已连接到 MCP Gateway，可以关闭此标签页并返回应用。</p></body></html>`))
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

	srv := &http.Server{Handler: mux}
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
			ts := cfg.TokenSource(context.Background(), tok)
			u.oauthMu.Lock()
			if u.oauthTokens == nil {
				u.oauthTokens = map[string]oauth2.TokenSource{}
			}
			u.oauthTokens[serverID] = ts
			u.oauthMu.Unlock()
			return ts, nil
		},
	})
	if err != nil {
		cleanup()
		return nil, nil, err
	}
	return h, cleanup, nil
}
