package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/proxy"
)

func RequestControl(ctx context.Context, c config.Config, request Request) (net.Conn, Reply, error) {
	var reply Reply
	conn, err := (&net.Dialer{}).DialContext(ctx, "unix", c.Socket)
	if err != nil {
		return nil, reply, fmt.Errorf("OCH service unavailable; run och serve --config <path>: %w", err)
	}
	handshakeDone := make(chan struct{})
	defer close(handshakeDone)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-handshakeDone:
		}
	}()
	conn.SetDeadline(time.Now().Add(65 * time.Second))
	if err = json.NewEncoder(conn).Encode(request); err != nil {
		conn.Close()
		return nil, reply, err
	}
	reader := bufio.NewReaderSize(conn, 64*1024)
	line, err := reader.ReadSlice('\n')
	if err == nil {
		err = json.Unmarshal(line, &reply)
	}
	if err == nil && reply.Error != "" {
		err = errors.New(reply.Error)
	}
	if err != nil {
		conn.Close()
		return nil, reply, err
	}
	conn.SetDeadline(time.Time{})
	return proxy.Buffered(conn, reader), reply, nil
}
func ProxyCommand(ctx context.Context, c config.Config, request Request, input io.Reader, output io.Writer) error {
	conn, _, err := RequestControl(ctx, c, request)
	if err != nil {
		return err
	}
	defer conn.Close()
	finished := make(chan struct{})
	defer close(finished)
	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-finished:
		}
	}()
	go func() {
		_, err := io.Copy(conn, input)
		if err != nil {
			conn.Close()
			return
		}
		if cw, ok := conn.(interface{ CloseWrite() error }); ok {
			cw.CloseWrite()
		}
	}()
	_, err = io.Copy(output, conn)
	return err
}
