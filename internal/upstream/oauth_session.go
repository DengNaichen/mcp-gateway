package upstream

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync"
	"time"

	"golang.org/x/oauth2"
)

// Both client registration and tokens belong in the instance's secret store.
// The endpoint hash prevents reusing a service ID from leaking an old session.
type oauthSession struct {
	Config oauth2.Config `json:"config"`
	Token  oauth2.Token  `json:"token"`
}

func oauthSessionKey(s Server) string {
	return fmt.Sprintf("oauth/%s/%x", s.ID, sha256.Sum256([]byte(s.Endpoint)))
}

func (u *Service) clearOAuth(key string) error {
	if u.saveOAuth != nil {
		if err := u.saveOAuth(key, ""); err != nil {
			return err
		}
	}
	u.oauthMu.Lock()
	delete(u.oauthTokens, key)
	u.oauthMu.Unlock()
	return nil
}

func (u *Service) restoreOAuth(key string) (oauth2.TokenSource, error) {
	if u.loadOAuth == nil {
		return nil, nil
	}
	raw := u.loadOAuth(key)
	if raw == "" {
		return nil, nil
	}
	var session oauthSession
	if json.Unmarshal([]byte(raw), &session) != nil || session.Token.AccessToken == "" || session.Config.Endpoint.TokenURL == "" {
		return nil, errors.New("无法读取 OAuth 会话，请重新连接服务")
	}
	return u.newOAuthSource(key, &session.Config, &session.Token)
}

func (u *Service) newOAuthSource(key string, cfg *oauth2.Config, token *oauth2.Token) (oauth2.TokenSource, error) {
	// Never retain the short-lived connect context in a refresh source.
	ctx := context.WithValue(context.Background(), oauth2.HTTPClient, &http.Client{Timeout: 30 * time.Second})
	source := &oauthSessionSource{service: u, key: key, config: *cfg, source: cfg.TokenSource(ctx, token)}
	if err := source.save(token); err != nil {
		return nil, err
	}
	u.oauthMu.Lock()
	u.oauthTokens[key] = source
	u.oauthMu.Unlock()
	return source, nil
}

type oauthSessionSource struct {
	mu      sync.Mutex
	service *Service
	key     string
	config  oauth2.Config
	source  oauth2.TokenSource
	saved   string
}

func (s *oauthSessionSource) save(token *oauth2.Token) error {
	raw, err := json.Marshal(oauthSession{Config: s.config, Token: *token})
	if err != nil {
		return errors.New("无法编码 OAuth 会话")
	}
	if string(raw) == s.saved {
		return nil
	}
	if s.service.saveOAuth != nil {
		if err := s.service.saveOAuth(s.key, string(raw)); err != nil {
			return err
		}
	}
	s.saved = string(raw)
	return nil
}

func (s *oauthSessionSource) Token() (*oauth2.Token, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	token, err := s.source.Token()
	if err != nil {
		var retrieve *oauth2.RetrieveError
		if errors.As(err, &retrieve) && (retrieve.ErrorCode == "invalid_grant" || retrieve.ErrorCode == "invalid_client") {
			if err := s.service.clearOAuth(s.key); err != nil {
				return nil, err
			}
			return nil, errors.New("OAuth 授权已失效，请重新触发同步以完成浏览器登录")
		}
		// Token endpoints may echo secrets in error bodies; never expose raw errors.
		return nil, errors.New("OAuth 刷新暂时失败，会话已保留，请稍后重试")
	}
	if err := s.save(token); err != nil {
		return nil, err
	}
	return token, nil
}

func (s *oauthSessionSource) secrets() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var session oauthSession
	_ = json.Unmarshal([]byte(s.saved), &session)
	return []string{session.Token.AccessToken, session.Token.RefreshToken, session.Config.ClientSecret}
}

// ForgetOAuth removes cached sources after deleting a service's credentials.
func (u *Service) ForgetOAuth(serverID string) {
	u.oauthMu.Lock()
	defer u.oauthMu.Unlock()
	for key := range u.oauthTokens {
		if strings.HasPrefix(key, "oauth/"+serverID+"/") {
			delete(u.oauthTokens, key)
		}
	}
}
