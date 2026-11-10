package proxy

import (
	"context"
	"errors"
	"net"
	"time"
)

// Accept retries temporary listener failures. The listener owner must close
// the listener to interrupt a blocked Accept call.
func Accept(ctx context.Context, listener net.Listener) (net.Conn, error) {
	delay := 5 * time.Millisecond
	for {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		conn, err := listener.Accept()
		if err == nil {
			return conn, nil
		}
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		var temporary net.Error
		if !errors.As(err, &temporary) || !temporary.Temporary() {
			return nil, err
		}
		timer := time.NewTimer(delay)
		select {
		case <-ctx.Done():
			timer.Stop()
			return nil, ctx.Err()
		case <-timer.C:
		}
		if delay < time.Second {
			delay *= 2
			if delay > time.Second {
				delay = time.Second
			}
		}
	}
}
