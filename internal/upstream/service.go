package upstream

import (
	"os"
	"sync"

	"golang.org/x/oauth2"
)

type Config struct {
	LookupEnv   func(string, string) string
	OpenBrowser func(string) error
	LoadOAuth   func(string) string
	SaveOAuth   func(string, string) error
}
type Service struct {
	lookupEnv   func(string, string) string
	openBrowser func(string) error
	loadOAuth   func(string) string
	saveOAuth   func(string, string) error
	sessionMu   sync.Mutex
	sessions    map[string]stdioSession
	oauthMu     sync.RWMutex
	oauthTokens map[string]oauth2.TokenSource
}

func New(config Config) *Service {
	if config.LookupEnv == nil {
		config.LookupEnv = func(_ string, key string) string { return os.Getenv(key) }
	}
	if config.OpenBrowser == nil {
		config.OpenBrowser = defaultOpenBrowserURL
	}
	return &Service{lookupEnv: config.LookupEnv, openBrowser: config.OpenBrowser, loadOAuth: config.LoadOAuth, saveOAuth: config.SaveOAuth, sessions: map[string]stdioSession{}, oauthTokens: map[string]oauth2.TokenSource{}}
}
func (u *Service) serverLookup(s Server) func(string) string {
	return func(key string) string { return u.lookupEnv(s.ID, key) }
}

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
		if session, ok := ts.(*oauthSessionSource); ok {
			secrets = append(secrets, session.secrets()...)
			continue
		}
		if tok, err := ts.Token(); err == nil && tok != nil && tok.AccessToken != "" {
			secrets = append(secrets, tok.AccessToken)
		}
	}
	return secrets
}
