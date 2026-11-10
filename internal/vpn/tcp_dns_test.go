package vpn

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"testing"
	"time"
)

func TestNativeTCPDomainUsesCancellableTunnelDNS(t *testing.T) {
	a, b := packetTestPair(t, false, true)
	queries := packetTestDNS(t, b)
	listener, err := b.stack.ListenTCPAddrPort(netip.AddrPortFrom(b.addresses[0], 2222))
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	closed := make(chan error, 1)
	go func() {
		c, err := listener.Accept()
		if err != nil {
			closed <- err
			return
		}
		defer c.Close()
		c.SetReadDeadline(time.Now().Add(2 * time.Second))
		_, err = c.Read(make([]byte, 1))
		closed <- err
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	c, err := packetTestManager(t, a).DialNetwork(ctx, "fixture", "tcp", "och.fixture.invalid:2222")
	if err != nil {
		t.Fatal(err)
	}
	if c.RemoteAddr().String() != net.JoinHostPort(b.addresses[0].String(), "2222") {
		t.Fatalf("unexpected TCP peer %v", c.RemoteAddr())
	}
	c.Close()
	select {
	case err := <-closed:
		if !errors.Is(err, io.EOF) {
			t.Fatalf("target read=%v", err)
		}
	case <-ctx.Done():
		t.Fatal("target connection not closed")
	}
	if queries.Load() == 0 {
		t.Fatal("TCP domain bypassed tunnel DNS")
	}
}

func TestNativeTCPDNSCancellation(t *testing.T) {
	a, b := packetTestPair(t, false, true)
	dns, err := b.stack.ListenUDPAddrPort(netip.AddrPortFrom(b.addresses[0], 53))
	if err != nil {
		t.Fatal(err)
	}
	defer dns.Close()
	dns.SetReadDeadline(time.Now().Add(3 * time.Second))
	m := packetTestManager(t, a)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan error, 1)
	go func() {
		c, err := m.Dial(ctx, "fixture", "och.fixture.invalid:2222")
		if c != nil {
			c.Close()
		}
		finished <- err
	}()
	if _, _, err := dns.ReadFrom(make([]byte, 1500)); err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("cancel error=%v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("TCP dial remained blocked on canceled DNS")
	}
	if a.Err() != nil || m.Status()["fixture"] != "ready" {
		t.Fatal("DNS cancellation invalidated shared VPN")
	}
}
