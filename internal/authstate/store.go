// Package authstate stores gateway-scoped bearer authentication state.
package authstate

import (
	"bytes"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"
)

const maxSize = 2 << 20

type Binding struct {
	Gateway     string `json:"gateway"`
	Protocol    string `json:"protocol"`
	AuthType    string `json:"auth_type"`
	LoginDomain string `json:"login_domain"`
	Username    string `json:"username"`
	AuthGroup   string `json:"auth_group"`
}
type envelope struct {
	Version int             `json:"version"`
	Binding Binding         `json:"binding"`
	SavedAt time.Time       `json:"saved_at"`
	Data    json.RawMessage `json:"data"`
}
type Store struct {
	path    string
	binding Binding
	lock    *os.File
	mu      sync.Mutex
	closed  bool
}

// Open holds an exclusive lock until Close so an old session cannot overwrite a newer login.
func Open(path string, binding Binding) (*Store, error) {
	if !filepath.IsAbs(path) {
		return nil, errors.New("auth_state_file must be absolute")
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0700); err != nil {
		return nil, errors.New("cannot create private authentication state directory")
	}
	info, err := os.Lstat(dir)
	if err != nil || !info.IsDir() || !ownedMode(info, 0700) {
		return nil, errors.New("authentication state directory must be owned, private (0700), and not a symlink")
	}
	lock, err := os.OpenFile(path+".lock", os.O_CREATE|os.O_RDWR|syscall.O_NOFOLLOW, 0600)
	if err != nil {
		return nil, errors.New("cannot open authentication state lock")
	}
	info, err = lock.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownedMode(info, 0600) {
		lock.Close()
		return nil, errors.New("authentication state lock must be a private regular file")
	}
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		lock.Close()
		return nil, errors.New("authentication state is in use by another session")
	}
	return &Store{path: path, binding: binding, lock: lock}, nil
}
func ownedMode(info os.FileInfo, mode os.FileMode) bool {
	st, ok := info.Sys().(*syscall.Stat_t)
	return ok && int(st.Uid) == os.Getuid() && info.Mode().Perm() == mode
}
func (s *Store) read() ([]byte, error) {
	f, err := os.OpenFile(s.path, os.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_NONBLOCK, 0)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, errors.New("cannot read authentication state")
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() || !ownedMode(info, 0600) {
		return nil, errors.New("authentication state must be an owned private regular file (0600)")
	}
	b, err := io.ReadAll(io.LimitReader(f, maxSize+1))
	if err != nil || len(b) > maxSize {
		return nil, errors.New("authentication state is unreadable or too large")
	}
	return b, nil
}
func (s *Store) Load() ([]byte, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("authentication state store is closed")
	}
	b, err := s.read()
	if err != nil || b == nil {
		return nil, err
	}
	e, err := decodeEnvelope(b, s.binding)
	if err != nil {
		return nil, err
	}

	return append([]byte(nil), e.Data...), nil
}
func decodeEnvelope(b []byte, binding Binding) (envelope, error) {
	var e envelope
	d := json.NewDecoder(bytes.NewReader(b))
	d.DisallowUnknownFields()
	if err := d.Decode(&e); err != nil {
		return envelope{}, errors.New("authentication state is malformed; use logout to clear it")
	}
	var extra any
	if d.Decode(&extra) != io.EOF || e.Version != 1 || len(e.Data) == 0 || !json.Valid(e.Data) {
		return envelope{}, errors.New("authentication state format is invalid")
	}
	if e.Binding != binding {
		return envelope{}, errors.New("authentication state belongs to a different gateway or identity")
	}
	return e, nil
}
func (s *Store) Save(data []byte) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("authentication state store is closed")
	}
	if len(data) == 0 || len(data) > maxSize/2 || !json.Valid(data) {
		return errors.New("invalid authentication state payload")
	}
	// Refuse replacement of unsafe existing paths, even though rename would not follow a symlink.
	if old, err := s.read(); err != nil {
		return err
	} else if old != nil {
		if _, err := decodeEnvelope(old, s.binding); err != nil {
			return err
		}
	}
	b, err := json.Marshal(envelope{Version: 1, Binding: s.binding, SavedAt: time.Now().UTC(), Data: append([]byte(nil), data...)})
	if err != nil {
		return errors.New("cannot encode authentication state")
	}
	f, err := os.CreateTemp(filepath.Dir(s.path), ".och-auth-")
	if err != nil {
		return errors.New("cannot write authentication state")
	}
	defer os.Remove(f.Name())
	if _, err = f.Write(b); err == nil {
		err = f.Sync()
	}
	closeErr := f.Close()
	if err == nil {
		err = closeErr
	}
	if err != nil {
		return errors.New("cannot persist authentication state")
	}
	if err = os.Rename(f.Name(), s.path); err != nil {
		return errors.New("cannot replace authentication state")
	}
	dir, err := os.Open(filepath.Dir(s.path))
	if err != nil {
		return errors.New("cannot sync authentication state directory")
	}
	defer dir.Close()
	if err = dir.Sync(); err != nil {
		return errors.New("cannot sync authentication state directory")
	}
	return nil
}
func (s *Store) Clear() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return errors.New("authentication state store is closed")
	}
	if old, err := s.read(); err != nil {
		return err
	} else if old != nil {
		var e envelope
		if json.Unmarshal(old, &e) == nil && e.Version == 1 && e.Binding != s.binding {
			return errors.New("authentication state belongs to a different gateway or identity")
		}
	}
	err := os.Remove(s.path)
	if err == nil {
		dir, e := os.Open(filepath.Dir(s.path))
		if e != nil {
			return errors.New("cannot sync cleared authentication state")
		}
		defer dir.Close()
		return dir.Sync()
	}
	if errors.Is(err, os.ErrNotExist) {
		return nil
	}
	return err
}
func (s *Store) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil
	}
	s.closed = true
	syscall.Flock(int(s.lock.Fd()), syscall.LOCK_UN)
	return s.lock.Close()
}
func Clear(path string, binding Binding) error {
	s, e := Open(path, binding)
	if e != nil {
		return e
	}
	defer s.Close()
	return s.Clear()
}
