package vpn

import (
	"context"
	"errors"
	"net"
	"sync"
)

type trackedSession struct {
	Session
	mu          sync.Mutex
	closed      bool
	done        chan struct{}
	connections map[*trackedConn]struct{}
}
type trackedConn struct {
	net.Conn
	owner *trackedSession
	once  sync.Once
}

func newTrackedSession(s Session) *trackedSession {
	tracked := &trackedSession{Session: s, connections: make(map[*trackedConn]struct{}), done: make(chan struct{})}
	if health, ok := s.(interface{ Done() <-chan struct{} }); ok {
		if done := health.Done(); done != nil {
			go func() {
				select {
				case <-done:
					tracked.Close()
				case <-tracked.done:
				}
			}()
		}
	}
	return tracked
}
func (s *trackedSession) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return nil, net.ErrClosed
	}
	c, e := s.Session.DialContext(ctx, n, a)
	if e != nil {
		return nil, e
	}
	wrapped := &trackedConn{Conn: c, owner: s}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		c.Close()
		return nil, net.ErrClosed
	}
	s.connections[wrapped] = struct{}{}
	return wrapped, nil
}
func (c *trackedConn) Close() error {
	var e error
	c.once.Do(func() { c.owner.mu.Lock(); delete(c.owner.connections, c); c.owner.mu.Unlock(); e = c.Conn.Close() })
	return e
}
func (c *trackedConn) CloseWrite() error {
	if x, ok := c.Conn.(interface{ CloseWrite() error }); ok {
		return x.CloseWrite()
	}
	return errors.New("connection does not support half-close")
}
func (s *trackedSession) Close() error {
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil
	}
	s.closed = true
	close(s.done)
	conns := make([]*trackedConn, 0, len(s.connections))
	for c := range s.connections {
		conns = append(conns, c)
	}
	s.mu.Unlock()
	for _, c := range conns {
		c.Close()
	}
	return s.Session.Close()
}
func (s *trackedSession) Err() error {
	s.mu.Lock()
	closed := s.closed
	s.mu.Unlock()
	if closed {
		return net.ErrClosed
	}
	if x, ok := s.Session.(interface{ Err() error }); ok {
		return x.Err()
	}
	return nil
}
