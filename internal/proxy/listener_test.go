package proxy

import (
	"context"
	"errors"
	"net"
	"syscall"
	"testing"
	"time"
)

type failedListener struct {
	err     error
	attempt chan struct{}
}

func (l failedListener) Accept() (net.Conn, error) {
	select {
	case l.attempt <- struct{}{}:
	default:
	}
	return nil, l.err
}
func (failedListener) Close() error   { return nil }
func (failedListener) Addr() net.Addr { return &net.TCPAddr{} }

func TestAcceptCancellationDuringBackoff(t *testing.T) {
	l := failedListener{err: &net.OpError{Op: "accept", Net: "tcp", Err: syscall.EMFILE}, attempt: make(chan struct{}, 1)}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() { _, err := Accept(ctx, l); done <- err }()
	<-l.attempt
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("accept returned %v, want context cancellation", err)
		}
	case <-time.After(time.Second):
		t.Fatal("cancellation did not interrupt accept backoff")
	}
}

func TestAcceptPreservesPermanentErrors(t *testing.T) {
	_, err := Accept(context.Background(), failedListener{err: net.ErrClosed})
	if !errors.Is(err, net.ErrClosed) {
		t.Fatalf("accept changed permanent error: %v", err)
	}
}
