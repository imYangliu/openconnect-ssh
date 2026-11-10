package forward

import (
	"context"
	"io"
	"net"
	"syscall"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
)

type transientListener struct {
	net.Listener
	failed bool
}

func (l *transientListener) Accept() (net.Conn, error) {
	if !l.failed {
		l.failed = true
		return nil, &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}
	}
	return l.Listener.Accept()
}

func TestForwardAcceptRecoversTemporaryError(t *testing.T) {
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	r := runner{ctx: ctx, dial: func(context.Context, string, string) (net.Conn, error) {
		a, b := net.Pipe()
		go func() { defer b.Close(); io.Copy(b, b) }()
		return a, nil
	}}
	defer func() { cancel(); l.Close(); r.wg.Wait() }()
	r.wg.Add(1)
	go func() {
		defer r.wg.Done()
		r.accept(binding{rule: config.Forward{Via: "direct", Target: "fixture:1"}, listener: &transientListener{Listener: l}}, false)
	}()
	c, err := net.Dial("tcp", l.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err := c.Write([]byte("hello")); err != nil {
		t.Fatal(err)
	}
	body := make([]byte, 5)
	if _, err := io.ReadFull(c, body); err != nil || string(body) != "hello" {
		t.Fatalf("forward did not recover temporary accept failure: %q %v", body, err)
	}
}
