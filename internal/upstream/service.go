package upstream

import (
	"os"
	"sync"

	"golang.org/x/oauth2"
)

type Config struct {
	LookupEnv   func(string) string
	OpenBrowser func(string) error
}
type Service struct {
	lookupEnv   func(string) string
	openBrowser func(string) error
	sessionMu   sync.Mutex
	sessions    map[string]stdioSession
	oauthMu     sync.RWMutex
	oauthTokens map[string]oauth2.TokenSource
}

func New(config Config) *Service {
	if config.LookupEnv == nil {
		config.LookupEnv = os.Getenv
	}
	if config.OpenBrowser == nil {
		config.OpenBrowser = defaultOpenBrowserURL
	}
	return &Service{lookupEnv: config.LookupEnv, openBrowser: config.OpenBrowser, sessions: map[string]stdioSession{}, oauthTokens: map[string]oauth2.TokenSource{}}
}
func (u *Service) expandEnv(value string) string { return os.Expand(value, u.lookupEnv) }

// TokenSecrets snapshots token sources under their owner lock, then resolves
// them without holding it. Callers use these values only for error redaction.
func (u *Service) TokenSecrets() []string {
	u.oauthMu.RLock()
	sources := make([]oauth2.TokenSource, 0, len(u.oauthTokens))
	for _, ts := range u.oauthTokens {
		sources = append(sources, ts)
	}
	u.oauthMu.RUnlock()
	secrets := []string{}
	for _, ts := range sources {
		if tok, err := ts.Token(); err == nil && tok != nil && tok.AccessToken != "" {
			secrets = append(secrets, tok.AccessToken)
		}
	}
	return secrets
}
