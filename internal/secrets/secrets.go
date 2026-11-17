package secrets

import (
	"os"
	"regexp"
	"strings"
	"sync"
)

// Store caches secrets and optionally persists them to a secure backend.
// The zero value is ready to use.
type Store struct {
	mu      sync.RWMutex
	values  map[string]string
	backend Backend
}

func (s *Store) Lookup(key string) string {
	if v := s.Get(key); v != "" {
		return v
	}
	return os.Getenv(key)
}
func (s *Store) Expand(value string) string { return os.Expand(value, s.Lookup) }
func (s *Store) Set(key, value string) {
	if s == nil || strings.TrimSpace(key) == "" {
		return
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.values == nil {
		s.values = map[string]string{}
	}
	if value == "" {
		delete(s.values, key)
	} else {
		s.values[key] = value
	}
}

// Get reads the loaded credential cache without consulting process environment.
func (s *Store) Get(key string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[key]
}

var envName = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_]*$`)

// ValidEnvName recognizes the environment variable names used by runtime secrets.
func ValidEnvName(name string) bool { return envName.MatchString(name) }
