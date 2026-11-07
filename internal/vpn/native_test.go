package vpn

import (
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	oc "github.com/sagernet/sing-openconnect"
)

func TestNativePacketStackTransfersTCP(t *testing.T) {
	t.Run("ready", func(t *testing.T) { testNativePacketStackTransfersTCP(t, false) })
	t.Run("reconnecting", func(t *testing.T) { testNativePacketStackTransfersTCP(t, true) })
}

func testNativePacketStackTransfersTCP(t *testing.T, reconnecting bool) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	left, right := make(chan []byte, 64), make(chan []byte, 64)
	dropped := make(chan struct{})
	makeSession := func(address string, in, out chan []byte, dropPayload []byte) *packetSession {
		read := func(ctx context.Context) ([]byte, error) {
			select {
			case b := <-in:
				return b, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		write := func(b []byte) error {
			if len(dropPayload) > 0 && bytes.HasSuffix(b, dropPayload) {
				dropPayload = nil
				close(dropped)
				return oc.ErrDataChannelNotReady
			}
			copy := append([]byte(nil), b...)
			select {
			case out <- copy:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		s, e := newPacketSession(ctx, []netip.Addr{netip.MustParseAddr(address)}, nil, 1400, read, write, func() error { return nil })
		if e != nil {
			t.Fatal(e)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	var dropPayload []byte
	if reconnecting {
		dropPayload = []byte("hello")
	}
	a := makeSession("10.20.0.1", left, right, dropPayload)
	b := makeSession("10.20.0.2", right, left, nil)
	listener, e := b.stack.ListenTCPAddrPort(netip.MustParseAddrPort("10.20.0.2:2222"))
	if e != nil {
		t.Fatal(e)
	}
	defer listener.Close()
	result := make(chan error, 1)
	go func() {
		c, e := listener.Accept()
		if e != nil {
			result <- e
			return
		}
		defer c.Close()
		_ = c.SetDeadline(time.Now().Add(3 * time.Second))
		data := make([]byte, 5)
		_, e = io.ReadFull(c, data)
		if e == nil && string(data) != "hello" {
			e = errors.New("payload changed")
		}
		if e == nil {
			_, e = c.Write([]byte("world"))
		}
		result <- e
	}()
	c, e := a.DialContext(ctx, "tcp", "10.20.0.2:2222")
	if e != nil {
		t.Fatal(e)
	}
	defer c.Close()
	_ = c.SetDeadline(time.Now().Add(3 * time.Second))
	if _, e = c.Write([]byte("hello")); e != nil {
		t.Fatal(e)
	}
	data := make([]byte, 5)
	if _, e = io.ReadFull(c, data); e != nil {
		t.Fatalf("read TCP response: %v (packet session: %v)", e, a.Err())
	}
	if string(data) != "world" {
		t.Fatalf("response %q", data)
	}
	if e = <-result; e != nil {
		t.Fatal(e)
	}
	if reconnecting {
		select {
		case <-dropped:
		default:
			t.Fatal("fixture did not drop the first TCP data packet")
		}
	}
}
func TestNativeRejectsMissingCredentialsBeforeConnecting(t *testing.T) {
	t.Setenv("OCH_TEST_MISSING_PASSWORD", "")
	_, err := NewNativeSession(context.Background(), config.Profile{Protocol: "anyconnect", Server: "https://vpn.invalid", PasswordEnv: "OCH_TEST_MISSING_PASSWORD"})
	if err == nil {
		t.Fatal("accepted empty configured secret")
	}
}

func TestNativeRejectsUntrustedGatewayBeforeHTTP(t *testing.T) {
	t.Setenv("OCH_TLS_FIXTURE_PASSWORD", "fixture")
	for _, protocol := range []string{"anyconnect", "atrust"} {
		t.Run(protocol, func(t *testing.T) {
			var requests atomic.Int32
			server := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				requests.Add(1)
				http.Error(w, "denied", http.StatusUnauthorized)
			}))
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			session, err := NewNativeSession(ctx, config.Profile{Protocol: protocol, Server: server.URL, Username: "fixture", PasswordEnv: "OCH_TLS_FIXTURE_PASSWORD"})
			if session != nil {
				session.Close()
			}
			if err == nil {
				t.Fatal("accepted self-signed gateway")
			}
			if requests.Load() != 0 {
				t.Fatal("sent HTTP/auth data through untrusted TLS")
			}
		})
	}
}

func idlePacketSession(t *testing.T, readError <-chan error) *packetSession {
	t.Helper()
	read := func(ctx context.Context) ([]byte, error) {
		select {
		case err := <-readError:
			return nil, err
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
	s, err := newPacketSession(context.Background(), []netip.Addr{netip.MustParseAddr("10.20.0.1")}, nil, 1400, read, func([]byte) error { return nil }, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}
func TestNativeHealthAfterCloseAndTransportFailure(t *testing.T) {
	t.Run("close", func(t *testing.T) {
		s := idlePacketSession(t, nil)
		if s.Err() != nil {
			t.Fatal(s.Err())
		}
		s.Close()
		if !errors.Is(s.Err(), net.ErrClosed) {
			t.Fatalf("health after Close: %v", s.Err())
		}
	})
	t.Run("transport", func(t *testing.T) {
		fail := make(chan error, 1)
		s := idlePacketSession(t, fail)
		want := errors.New("fixture transport failure")
		fail <- want
		deadline := time.After(time.Second)
		for s.Err() == nil {
			select {
			case <-deadline:
				t.Fatal("transport failure did not change health")
			default:
				time.Sleep(time.Millisecond)
			}
		}
		s.Close()
		if !errors.Is(s.Err(), want) {
			t.Fatalf("lost original failure: %v", s.Err())
		}
	})
}
func TestNativeConfigurationChangeInvalidatesStack(t *testing.T) {
	for _, change := range []string{"address", "dns", "mtu"} {
		t.Run(change, func(t *testing.T) {
			s := idlePacketSession(t, nil)
			var guard packetConfigurationGuard
			addresses := []netip.Prefix{netip.MustParsePrefix("10.20.0.1/32")}
			dns := []netip.Addr{netip.MustParseAddr("10.20.0.53")}
			mtu := uint32(1400)
			key := packetConfigurationKey(addresses, dns, mtu)
			guard.bind(s, key)
			guard.observe(key)
			if s.Err() != nil {
				t.Fatal("unchanged config invalidated session")
			}
			switch change {
			case "address":
				addresses[0] = netip.MustParsePrefix("10.20.0.2/32")
			case "dns":
				dns[0] = netip.MustParseAddr("10.20.0.54")
			case "mtu":
				mtu = 1300
			}
			guard.observe(packetConfigurationKey(addresses, dns, mtu))
			if s.Err() == nil {
				t.Fatal("changed config kept stale stack healthy")
			}
		})
	}
}
