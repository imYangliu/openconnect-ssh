package vpn

import (
	"context"
	"errors"
	"net"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
)

type testSession struct {
	dial   func(context.Context, string, string) (net.Conn, error)
	closed atomic.Int32
}

func (s *testSession) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return s.dial(ctx, n, a)
}
func (s *testSession) Close() error { s.closed.Add(1); return nil }
func TestSessionInitializationSharedAndCancellationIndependent(t *testing.T) {
	var count atomic.Int32
	started := make(chan struct{})
	release := make(chan struct{})
	session := &testSession{dial: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("test dial") }}
	m := NewManager(context.Background(), map[string]config.Profile{"campus": {}}, func(ctx context.Context, p config.Profile) (Session, error) {
		count.Add(1)
		close(started)
		select {
		case <-release:
			return session, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	ctx, cancel := context.WithCancel(context.Background())
	first := make(chan error, 1)
	go func() { _, err := m.Dial(ctx, "campus", "127.0.0.1:22"); first <- err }()
	<-started
	cancel()
	if err := <-first; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	var wg sync.WaitGroup
	for i := 0; i < 8; i++ {
		wg.Add(1)
		go func() { defer wg.Done(); m.Dial(context.Background(), "campus", "127.0.0.1:22") }()
	}
	close(release)
	wg.Wait()
	m.Close()
	m.Close()
	if count.Load() != 1 || session.closed.Load() != 1 {
		t.Fatal(count.Load(), session.closed.Load())
	}
}
func TestAuthenticationFailureCooldown(t *testing.T) {
	var calls atomic.Int32
	m := NewManager(context.Background(), map[string]config.Profile{"campus": {}}, func(context.Context, config.Profile) (Session, error) {
		calls.Add(1)
		return nil, errors.New("authentication rejected")
	})
	defer m.Close()
	for i := 0; i < 5; i++ {
		if _, err := m.Dial(context.Background(), "campus", "host:22"); err == nil {
			t.Fatal("unexpected successful auth")
		}
	}
	if calls.Load() != 1 || m.Status()["campus"] != "failed" {
		t.Fatal(calls.Load(), m.Status())
	}
}

type healthSession struct {
	testSession
	failed atomic.Bool
}

func (s *healthSession) Err() error {
	if s.failed.Load() {
		return errors.New("tunnel lost")
	}
	return nil
}
func TestTerminalSessionFailsThenReinitializes(t *testing.T) {
	var created atomic.Int32
	first := &healthSession{testSession: testSession{dial: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("target refused") }}}
	m := NewManager(context.Background(), map[string]config.Profile{"campus": {}}, func(context.Context, config.Profile) (Session, error) {
		created.Add(1)
		return first, nil
	})
	defer m.Close()
	m.Dial(context.Background(), "campus", "host:22")
	m.Dial(context.Background(), "campus", "host:22")
	if created.Load() != 1 || m.Status()["campus"] != "ready" {
		t.Fatal("target refusal invalidated healthy tunnel")
	}
	first.failed.Store(true)
	if m.Status()["campus"] != "failed" {
		t.Fatal("dead tunnel reported ready")
	}
	m.Dial(context.Background(), "campus", "host:22")
	m.Dial(context.Background(), "campus", "host:22")
	if created.Load() != 1 || first.closed.Load() != 1 {
		t.Fatal("cooldown or close failed")
	}
	time.Sleep(5100 * time.Millisecond)
	first.failed.Store(false)
	m.Dial(context.Background(), "campus", "host:22")
	if created.Load() != 2 {
		t.Fatal("terminal session was never reinitialized")
	}
}
