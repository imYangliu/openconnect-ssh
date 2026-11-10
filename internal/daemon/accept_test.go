package daemon

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"syscall"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

type transientListener struct {
	net.Listener
	first atomic.Bool
}

func TestMixedListenersKeepTheirConfiguredVPN(t *testing.T) {
	origin := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { io.WriteString(w, "ok") }))
	defer origin.Close()
	var first, second atomic.Int32
	c := config.Config{
		Profiles: map[string]config.Profile{"one": {Username: "one"}, "two": {Username: "two"}},
		Listeners: map[string]config.Listener{
			"first":  {Listen: "127.0.0.1:0", Via: "one"},
			"second": {Listen: "127.0.0.1:0", Via: "two"},
		},
	}
	c, addresses := startDaemon(t, c, func(ctx context.Context, p config.Profile) (vpn.Session, error) {
		if p.Username == "one" {
			first.Add(1)
		} else {
			second.Add(1)
		}
		return directFactory(ctx, p)
	})
	for i, name := range []string{"first", "second"} {
		conn, err := net.Dial("tcp", addresses[name])
		if err != nil {
			t.Fatal(err)
		}
		conn.SetDeadline(time.Now().Add(2 * time.Second))
		fmt.Fprintf(conn, "GET %s/ HTTP/1.1\r\nHost: ignored.invalid\r\n\r\n", origin.URL)
		response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "GET"})
		if err != nil {
			conn.Close()
			t.Fatal(err)
		}
		body, err := io.ReadAll(response.Body)
		response.Body.Close()
		conn.Close()
		if err != nil || string(body) != "ok" || first.Load() != 1 || second.Load() != int32(i) {
			t.Fatalf("listener %s used wrong VPN: first=%d second=%d body=%q err=%v", name, first.Load(), second.Load(), body, err)
		}
	}
	conn, reply, err := RequestControl(context.Background(), c, Request{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	for name, address := range addresses {
		if reply.Listeners[name] != address {
			t.Fatalf("listener %s status address changed: got %q want %q", name, reply.Listeners[name], address)
		}
	}
}

func (l *transientListener) Accept() (net.Conn, error) {
	if l.first.CompareAndSwap(false, true) {
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	}
	return l.Listener.Accept()
}
func TestDaemonAcceptRecoversTemporaryError(t *testing.T) {
	transient := &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	if !transient.Temporary() {
		t.Skip("EMFILE not temporary on this platform")
	}
	base, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer base.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	d := &daemon{active: make(map[net.Conn]struct{})}
	defer func() { cancel(); base.Close(); d.wg.Wait() }()
	accepted := make(chan struct{}, 1)
	d.wg.Add(1)
	go d.accept(ctx, &transientListener{Listener: base}, func(net.Conn) { accepted <- struct{}{} })
	c, err := net.Dial("tcp", base.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	select {
	case <-accepted:
	case <-time.After(200 * time.Millisecond):
		t.Fatal("temporary accept error permanently stopped daemon listener")
	}
}
