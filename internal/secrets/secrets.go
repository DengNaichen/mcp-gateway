package secrets

import (
	"os"
	"strings"
	"sync"
)

// Store contains ephemeral secrets. Values are never persisted.
// The zero value is ready to use.
type Store struct {
	mu     sync.RWMutex
	values map[string]string
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

// Get returns only an in-memory value; profile IDs are not environment names.
func (s *Store) Get(key string) string {
	if s == nil {
		return ""
	}
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.values[key]
}
