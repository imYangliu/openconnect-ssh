package vpn

import (
	"context"
	"errors"
	"net/netip"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/authstate"
)

func TestPingCancelUnblocksWriterAndSkipsExpiredQueuedPacket(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	defer unblock()
	var writes atomic.Int32
	var session *packetSession
	write := func(packet []byte) error {
		if writes.Add(1) == 1 {
			close(entered)
			<-release
		}
		session.dispatchEcho(packetTestEchoReply(packet, nil))
		return nil
	}
	read := func(ctx context.Context) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	var err error
	session, err = newPacketSession(context.Background(), []netip.Addr{netip.MustParseAddr("10.99.0.1")}, nil, 1400, read, write, func() error { unblock(); return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { session.Close() })
	m := packetTestManager(t, session)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	first := make(chan error, 1)
	go func() { _, err := m.Ping(ctx, "fixture", "10.99.0.2", 1); first <- err }()
	select {
	case <-entered:
	case <-time.After(3 * time.Second):
		t.Fatal("writer not reached")
	}
	cancel()
	select {
	case err := <-first:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation blocked on transport write")
	}
	queued, cancelQueued := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancelQueued()
	if _, err := m.Ping(queued, "fixture", "10.99.0.2", 2); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("queued ping error=%v", err)
	}
	unblock()
	ctx, stop := context.WithTimeout(context.Background(), time.Second)
	defer stop()
	if _, err := m.Ping(ctx, "fixture", "10.99.0.2", 3); err != nil {
		t.Fatal(err)
	}
	if writes.Load() != 2 {
		t.Fatalf("expired queued ping transmitted, writes=%d", writes.Load())
	}
}

func TestSessionDoneFollowsNativeFailure(t *testing.T) {
	for _, wrapped := range []bool{false, true} {
		t.Run(map[bool]string{false: "native", true: "saved-auth"}[wrapped], func(t *testing.T) {
			s := idlePacketSession(t, nil)
			var backend Session = s
			if wrapped {
				store, err := authstate.Open(filepath.Join(t.TempDir(), "auth", "state.json"), authstate.Binding{Gateway: "https://fixture.invalid", Protocol: "anyconnect"})
				if err != nil {
					t.Fatal(err)
				}
				backend = &storedSession{Session: s, store: store}
			}
			m := packetTestManager(t, backend)
			ctx, cancel := context.WithTimeout(context.Background(), time.Second)
			defer cancel()
			done, err := m.SessionDone(ctx, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			s.Close()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("native close left association lifetime open")
			}
		})
	}
}
