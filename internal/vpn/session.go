package vpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"sync"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
)

type Session interface {
	DialContext(context.Context, string, string) (net.Conn, error)
	Close() error
}
type Factory func(context.Context, config.Profile) (Session, error)
type sessionEntry struct {
	ready   chan struct{}
	session Session
	err     error
	retryAt time.Time
}
type Manager struct {
	ctx      context.Context
	cancel   context.CancelFunc
	profiles map[string]config.Profile
	factory  Factory
	mu       sync.Mutex
	entries  map[string]*sessionEntry
	closed   bool
}

func NewManager(ctx context.Context, profiles map[string]config.Profile, factory Factory) *Manager {
	ctx, cancel := context.WithCancel(ctx)
	return &Manager{ctx: ctx, cancel: cancel, profiles: profiles, factory: factory, entries: make(map[string]*sessionEntry)}
}
func (m *Manager) Dial(ctx context.Context, via, address string) (net.Conn, error) {
	if via == "direct" {
		return (&net.Dialer{}).DialContext(ctx, "tcp", address)
	}
	entry, err := m.getEntry(ctx, via)
	if err != nil {
		return nil, err
	}
	conn, err := entry.session.DialContext(ctx, "tcp", address)
	if err != nil {
		if health, ok := entry.session.(interface{ Err() error }); ok {
			if terminal := health.Err(); terminal != nil {
				m.invalidate(via, entry, terminal)
			}
		}
	}
	return conn, err
}
func (m *Manager) getEntry(ctx context.Context, via string) (*sessionEntry, error) {
	profile, ok := m.profiles[via]
	if !ok {
		return nil, fmt.Errorf("unknown VPN profile %q", via)
	}
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return nil, errors.New("session service is stopping")
	}
	entry := m.entries[via]
	if entry != nil {
		select {
		case <-entry.ready:
			if entry.err != nil && time.Now().After(entry.retryAt) {
				entry = nil
			}
		default:
		}
	}
	if entry == nil {
		entry = &sessionEntry{ready: make(chan struct{})}
		m.entries[via] = entry
		go m.initialize(m.ctx, profile, entry)
	}
	m.mu.Unlock()
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-entry.ready:
	}
	if entry.err != nil {
		return nil, fmt.Errorf("VPN %s: %w", via, entry.err)
	}
	if health, ok := entry.session.(interface{ Err() error }); ok {
		if err := health.Err(); err != nil {
			m.invalidate(via, entry, err)
			return nil, fmt.Errorf("VPN %s unavailable: %w", via, err)
		}
	}
	return entry, nil
}
func (m *Manager) Ensure(ctx context.Context, via string) error {
	_, err := m.getEntry(ctx, via)
	return err
}
func (m *Manager) Logout(ctx context.Context, via string) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	p, ok := m.profiles[via]
	if !ok {
		return errors.New("unknown VPN profile")
	}
	m.mu.Lock()
	old := m.entries[via]
	if old != nil {
		select {
		case <-old.ready:
		default:
			m.mu.Unlock()
			return errors.New("VPN is connecting; cancel login before logout")
		}
	}
	blocked := &sessionEntry{ready: make(chan struct{})}
	m.entries[via] = blocked
	m.mu.Unlock()
	if old != nil && old.session != nil {
		old.session.Close()
	}
	err := ClearAuthentication(p)
	m.mu.Lock()
	blocked.err = ErrLoginRequired
	if err != nil {
		blocked.err = err
	}
	blocked.retryAt = time.Now().Add(5 * time.Second)
	close(blocked.ready)
	m.mu.Unlock()
	return err
}
func (m *Manager) invalidate(name string, old *sessionEntry, err error) {
	failed := &sessionEntry{ready: make(chan struct{}), err: err, retryAt: time.Now().Add(5 * time.Second)}
	close(failed.ready)
	m.mu.Lock()
	changed := m.entries[name] == old && !m.closed
	if changed {
		m.entries[name] = failed
	}
	m.mu.Unlock()
	if changed {
		old.session.Close()
	}
}
func (m *Manager) initialize(ctx context.Context, profile config.Profile, entry *sessionEntry) {
	session, err := m.factory(ctx, profile)
	if attempt, ok := ctx.Value(casLoginKey{}).(*casLoginAttempt); ok && attempt.ctx.Err() != nil {
		if session != nil {
			session.Close()
			session = nil
		}
		err = attempt.ctx.Err()
	}
	if err != nil && session != nil {
		session.Close()
		session = nil
	}
	if err == nil && session == nil {
		err = errors.New("VPN backend returned no session")
	}
	if _, interactive := ctx.Value(casLoginKey{}).(*casLoginAttempt); interactive && err != nil {
		category, status := LoginFailureDetails(err)
		err = LoginFailure(category, status)
	}
	m.mu.Lock()
	if m.closed && session != nil {
		session.Close()
		session = nil
		err = errors.New("session service stopped")
	}
	if session != nil && err == nil {
		session = newTrackedSession(session)
	}
	entry.session, entry.err = session, err
	if err != nil {
		entry.retryAt = time.Now().Add(5 * time.Second)
	}
	close(entry.ready)
	m.mu.Unlock()
}
func (m *Manager) Close() {
	m.cancel()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return
	}
	m.closed = true
	var sessions []Session
	for _, e := range m.entries {
		select {
		case <-e.ready:
			if e.session != nil {
				sessions = append(sessions, e.session)
			}
		default:
		}
	}
	m.mu.Unlock()
	for _, s := range sessions {
		s.Close()
	}
}
func (m *Manager) Status() map[string]string {
	m.mu.Lock()
	defer m.mu.Unlock()
	result := make(map[string]string, len(m.profiles))
	for name := range m.profiles {
		result[name] = "idle"
		if e := m.entries[name]; e != nil {
			select {
			case <-e.ready:
				result[name] = "ready"
				if e.err != nil {
					result[name] = "failed"
				} else if health, ok := e.session.(interface{ Err() error }); ok && health.Err() != nil {
					result[name] = "failed"
				}
			default:
				result[name] = "connecting"
			}
		}
	}
	return result
}
