package secrets

import (
	"encoding/json"
	"errors"
	"fmt"
	"maps"
)

// Error is a safe, actionable credential-store failure for the control plane.
type Error struct{ Message string }

func (e *Error) Error() string { return e.Message }

// Backend stores one encrypted credential collection per instance namespace.
// Implementations must never include secret values in errors.
type Backend interface {
	Load() ([]byte, error)
	Save([]byte) error
}

func Open(backend Backend) (*Store, error) {
	s := &Store{backend: backend}
	if backend == nil {
		return s, nil
	}
	data, err := backend.Load()
	if err != nil {
		var se *Error
		if errors.As(err, &se) {
			return nil, se
		}
		return nil, &Error{Message: "无法读取系统钥匙串凭据，请解锁登录钥匙串并允许 MCP Gateway 访问"}
	}
	if len(data) > 0 {
		if err := json.Unmarshal(data, &s.values); err != nil {
			return nil, errors.New("无法读取已保存的凭据，请检查系统钥匙串")
		}
	}
	return s, nil
}

// Update persists credentials before committing configuration, then publishes
// the new in-memory snapshot. A failed configuration write restores credentials.
func (s *Store) Update(changes map[string]string, commit func() error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	next := maps.Clone(s.values)
	if next == nil {
		next = map[string]string{}
	}
	for k, v := range changes {
		if v == "" {
			delete(next, k)
		} else {
			next[k] = v
		}
	}
	if len(changes) > 0 {
		if err := s.persist(next); err != nil {
			return err
		}
	}
	if commit != nil {
		if err := commit(); err != nil {
			if len(changes) > 0 {
				if rollbackErr := s.persist(s.values); rollbackErr != nil {
					return errors.Join(err, fmt.Errorf("恢复凭据失败: %w", rollbackErr))
				}
			}
			return err
		}
	}
	s.values = next
	return nil
}

func (s *Store) persist(values map[string]string) error {
	if s.backend == nil {
		return nil
	}
	data, err := json.Marshal(values)
	if err != nil {
		return errors.New("无法编码凭据")
	}
	if err := s.backend.Save(data); err != nil {
		var se *Error
		if errors.As(err, &se) {
			return se
		}
		return &Error{Message: "无法保存凭据，请解锁系统钥匙串并允许 MCP Gateway 访问后重试"}
	}
	return nil
}

// Prefix returns only key names, for lifecycle cleanup. Values never leave Store.
func (s *Store) Prefix(prefix string) []string {
	s.mu.RLock()
	defer s.mu.RUnlock()
	var keys []string
	for key := range s.values {
		if len(key) >= len(prefix) && key[:len(prefix)] == prefix {
			keys = append(keys, key)
		}
	}
	return keys
}

func (s *Store) Persistent() bool { return s.backend != nil }
