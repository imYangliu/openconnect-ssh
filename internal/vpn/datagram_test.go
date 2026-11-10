package vpn

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"golang.org/x/net/dns/dnsmessage"
)

func packetTestPair(t *testing.T, ipv6 bool, dns bool) (*packetSession, *packetSession) {
	t.Helper()
	local, remote := "10.99.0.1", "10.99.0.2"
	if ipv6 {
		local, remote = "fd99::1", "fd99::2"
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	left, right := make(chan []byte, 64), make(chan []byte, 64)
	makeSession := func(address string, in, out chan []byte, servers []netip.Addr) *packetSession {
		read := func(ctx context.Context) ([]byte, error) {
			select {
			case p := <-in:
				return p, nil
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		write := func(p []byte) error {
			select {
			case out <- bytes.Clone(p):
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}
		s, err := newPacketSession(ctx, []netip.Addr{netip.MustParseAddr(address)}, servers, 1400, read, write, func() error { return nil })
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		return s
	}
	var servers []netip.Addr
	if dns {
		servers = []netip.Addr{netip.MustParseAddr(remote)}
	}
	return makeSession(local, left, right, servers), makeSession(remote, right, left, nil)
}

func packetTestManager(t *testing.T, session Session) *Manager {
	t.Helper()
	m := NewManager(context.Background(), map[string]config.Profile{"fixture": {}}, func(context.Context, config.Profile) (Session, error) { return session, nil })
	t.Cleanup(m.Close)
	return m
}

func TestNativeUDPDatagramBoundaries(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		t.Run(fmt.Sprintf("ipv6=%v", ipv6), func(t *testing.T) {
			a, b := packetTestPair(t, ipv6, false)
			remote := netip.AddrPortFrom(b.addresses[0], 5354)
			listener, err := b.stack.ListenUDPAddrPort(remote)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			c, err := packetTestManager(t, a).DialNetwork(ctx, "fixture", "udp", remote.String())
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(2 * time.Second))
			listener.SetDeadline(time.Now().Add(2 * time.Second))
			if c.RemoteAddr().String() != remote.String() {
				t.Fatalf("remote address = %v, want %v", c.RemoteAddr(), remote)
			}
			payloads := [][]byte{[]byte("first"), {}, []byte("third datagram"), {0, 1, 0xff}}
			for _, want := range payloads {
				if n, err := c.Write(want); err != nil || n != len(want) {
					t.Fatalf("write %x: n=%d err=%v", want, n, err)
				}
			}
			for _, want := range payloads {
				buf := make([]byte, 128)
				n, peer, err := listener.ReadFrom(buf)
				if err != nil || !bytes.Equal(buf[:n], want) {
					t.Fatalf("received datagram %x, want %x: %v", buf[:n], want, err)
				}
				if _, err := listener.WriteTo(buf[:n], peer); err != nil {
					t.Fatal(err)
				}
			}
			for _, want := range payloads {
				buf := make([]byte, 128)
				n, err := c.Read(buf)
				if err != nil || !bytes.Equal(buf[:n], want) {
					t.Fatalf("reply datagram %x, want %x: %v", buf[:n], want, err)
				}
			}
		})
	}
}

func packetTestDNS(t *testing.T, remote *packetSession) *atomic.Int32 {
	t.Helper()
	server, err := remote.stack.ListenUDPAddrPort(netip.AddrPortFrom(remote.addresses[0], 53))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { server.Close() })
	queries := new(atomic.Int32)
	go func() {
		buf := make([]byte, 1500)
		for {
			n, peer, err := server.ReadFrom(buf)
			if err != nil {
				return
			}
			var request dnsmessage.Message
			if request.Unpack(buf[:n]) != nil || len(request.Questions) != 1 {
				continue
			}
			question := request.Questions[0]
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: request.ID, Response: true, Authoritative: true, RecursionAvailable: true}, Questions: request.Questions}
			if question.Name.String() == "och.fixture.invalid." || question.Name.String() == "localhost." {
				queries.Add(1)
				if question.Type == dnsmessage.TypeA {
					response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET, TTL: 10}, Body: &dnsmessage.AResource{A: remote.addresses[0].As4()}}}
				}
			} else {
				response.RCode = dnsmessage.RCodeNameError
			}
			wire, err := response.Pack()
			if err == nil {
				server.WriteTo(wire, peer)
			}
		}
	}()
	return queries
}

func TestNativePacketDNSIgnoresLocalHosts(t *testing.T) {
	a, b := packetTestPair(t, false, true)
	queries := packetTestDNS(t, b)
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	addresses, err := a.lookupPacketHost(ctx, "localhost", nil)
	if err != nil || len(addresses) != 1 || addresses[0] != b.addresses[0] || queries.Load() == 0 {
		t.Fatalf("VPN localhost lookup = %v, err=%v, tunnel queries=%d", addresses, err, queries.Load())
	}
}

func TestNativePacketDomainsUseTunnelDNS(t *testing.T) {
	a, b := packetTestPair(t, false, true)
	queries := packetTestDNS(t, b)
	listener, err := b.stack.ListenUDPAddrPort(netip.AddrPortFrom(b.addresses[0], 5354))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	listener.SetDeadline(time.Now().Add(2 * time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	m := packetTestManager(t, a)
	c, err := m.DialNetwork(ctx, "fixture", "udp", "och.fixture.invalid:5354")
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("resolved through tunnel")); err != nil {
		t.Fatal(err)
	}
	buf := make([]byte, 128)
	n, peer, err := listener.ReadFrom(buf)
	if err != nil || string(buf[:n]) != "resolved through tunnel" {
		t.Fatalf("resolved UDP destination received %q: %v", buf[:n], err)
	}
	if _, err := listener.WriteTo(buf[:n], peer); err != nil {
		t.Fatal(err)
	}
	if n, err := c.Read(buf); err != nil || string(buf[:n]) != "resolved through tunnel" {
		t.Fatalf("resolved UDP reply = %q: %v", buf[:n], err)
	}
	if want := net.JoinHostPort(b.addresses[0].String(), "5354"); c.RemoteAddr().String() != want {
		t.Fatalf("resolved peer = %v, want %v", c.RemoteAddr(), want)
	}
	if queries.Load() == 0 {
		t.Fatal("UDP domain did not query the tunnel DNS fixture")
	}
	before := queries.Load()
	result, err := m.Ping(ctx, "fixture", "och.fixture.invalid", 7)
	if err != nil || result.Address != b.addresses[0].String() {
		t.Fatalf("domain ping = %+v, err=%v", result, err)
	}
	if queries.Load() <= before {
		t.Fatal("ping domain did not query the tunnel DNS fixture")
	}
}

type packetPolicySession struct {
	*packetSession
	check func(netip.Addr, string, int) error
}

func (s packetPolicySession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return s.dialPacket(ctx, network, address, nil, s.check)
}

func TestNativePacketPolicyErrorsKeepSessionHealthy(t *testing.T) {
	var sent atomic.Int32
	s := packetTestImmediateEcho(t, false, func([]byte) []byte { sent.Add(1); return nil })
	var checked atomic.Int32
	m := packetTestManager(t, packetPolicySession{s, func(ip netip.Addr, protocol string, port int) error {
		checked.Add(1)
		if ip != netip.MustParseAddr("10.99.0.2") || !(protocol == "udp" && port == 53 || protocol == "icmp" && port == -1) {
			t.Errorf("unexpected resource check: %s %s %d", ip, protocol, port)
		}
		return ErrResourceDenied
	}})
	for _, tc := range []struct {
		network, address string
		want             error
	}{
		{"udp", "10.99.0.2:53", ErrResourceDenied},
		{"udp", "[fd99::2]:53", ErrAddressFamily},
		{"ping6", "fd99::2", ErrAddressFamily},
		{"ping6", "10.99.0.2", ErrAddressFamily},
	} {
		c, err := m.DialNetwork(context.Background(), "fixture", tc.network, tc.address)
		if c != nil || !errors.Is(err, tc.want) {
			t.Fatalf("%s %s: conn=%v err=%v, want %v", tc.network, tc.address, c, err, tc.want)
		}
		if s.Err() != nil || m.Status()["fixture"] != "ready" {
			t.Fatalf("target rejection invalidated the VPN: health=%v status=%v", s.Err(), m.Status())
		}
	}
	if _, err := m.Ping(context.Background(), "fixture", "10.99.0.2", 1); !errors.Is(err, ErrResourceDenied) {
		t.Fatalf("resource-denied ping error = %v", err)
	}
	if sent.Load() != 0 {
		t.Fatalf("denied target emitted %d packets", sent.Load())
	}
	if checked.Load() != 2 {
		t.Fatalf("resource checker called %d times; mismatched families must fail before authorization", checked.Load())
	}
}

func TestManagerCloseAndLogoutClosePacketSockets(t *testing.T) {
	for _, action := range []string{"close", "logout"} {
		t.Run(action, func(t *testing.T) {
			a, b := packetTestPair(t, false, false)
			m := packetTestManager(t, a)
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			defer cancel()
			var sockets []net.Conn
			for _, tc := range []struct{ network, address string }{{"udp", net.JoinHostPort(b.addresses[0].String(), "5354")}, {"ping4", b.addresses[0].String()}} {
				c, err := m.DialNetwork(ctx, "fixture", tc.network, tc.address)
				if err != nil {
					t.Fatal(err)
				}
				sockets = append(sockets, c)
			}
			done, err := m.SessionDone(ctx, "fixture")
			if err != nil {
				t.Fatal(err)
			}
			reads := make(chan error, len(sockets))
			for _, c := range sockets {
				go func(c net.Conn) { _, err := c.Read(make([]byte, 1500)); reads <- err }(c)
			}
			if action == "logout" {
				if err := m.Logout(ctx, "fixture"); err != nil {
					t.Fatal(err)
				}
			} else {
				m.Close()
			}
			select {
			case <-done:
			default:
				t.Fatal("session lifetime remained open")
			}
			for range sockets {
				select {
				case err := <-reads:
					if err == nil {
						t.Fatal("closed packet socket read succeeded")
					}
				case <-ctx.Done():
					t.Fatal("teardown did not unblock packet read")
				}
			}
		})
	}
}

func TestNativePacketDNSCancellationKeepsSessionHealthy(t *testing.T) {
	a, b := packetTestPair(t, false, true)
	server, err := b.stack.ListenUDPAddrPort(netip.AddrPortFrom(b.addresses[0], 53))
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	server.SetDeadline(time.Now().Add(time.Second))
	m := packetTestManager(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	result := make(chan error, 1)
	go func() {
		c, err := m.DialNetwork(ctx, "fixture", "udp", "och.fixture.invalid:53")
		if c != nil {
			c.Close()
		}
		result <- err
	}()
	// Wait for the real userspace resolver to send its DNS request. Withhold
	// a response and cancel while resolution is in progress.
	if _, _, err := server.ReadFrom(make([]byte, 1500)); err != nil {
		t.Fatalf("DNS request did not enter the VPN: %v", err)
	}
	cancel()
	select {
	case err := <-result:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled VPN DNS lookup error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("VPN DNS lookup ignored cancellation")
	}
	if a.Err() != nil || m.Status()["fixture"] != "ready" {
		t.Fatalf("DNS cancellation invalidated shared session: %v, %v", a.Err(), m.Status())
	}
}
