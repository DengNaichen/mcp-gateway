// Package store owns SQLite initialization, migrations and data-directory lifetime.
package store

import (
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sync"

	"github.com/gofrs/flock"
	_ "modernc.org/sqlite"
)

var ErrLocked = errors.New("gateway data directory is already in use")

type Store struct {
	*sql.DB
	lock     *flock.Flock
	once     sync.Once
	closeErr error
}

// Open takes a nonblocking process lock before opening or migrating the DB.
// The lock file is retained: unlinking it would let a third process lock a
// different inode while another process still owns the original one.
func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0700); err != nil { //nolint:gosec // G703: path is the user's own local data/socket path from configuration, not remote input
		return nil, fmt.Errorf("create data directory: %w", err)
	}
	absolute, err := filepath.EvalSymlinks(dir)
	if err != nil {
		return nil, err
	}
	lock := flock.New(filepath.Join(absolute, "gateway.lock"), flock.SetPermissions(0600))
	ok, err := lock.TryLock()
	if err != nil {
		return nil, fmt.Errorf("lock gateway data directory: %w", err)
	}
	if !ok {
		return nil, fmt.Errorf("%w: %s", ErrLocked, absolute)
	}
	db, err := openDatabase(filepath.Join(absolute, "gateway.db"))
	if err != nil {
		_ = lock.Unlock()
		return nil, err
	}
	return &Store{DB: db, lock: lock}, nil
}

func (s *Store) Close() error {
	s.once.Do(func() { s.closeErr = errors.Join(s.DB.Close(), s.lock.Unlock()) })
	return s.closeErr
}
