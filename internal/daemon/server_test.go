package daemon

import (
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

type testSession struct {
	dial   func(context.Context, string, string) (net.Conn, error)
	closed atomic.Int32
}

func (s *testSession) DialContext(ctx context.Context, n, a string) (net.Conn, error) {
	return s.dial(ctx, n, a)
}
func (s *testSession) Close() error { s.closed.Add(1); return nil }
func directFactory(ctx context.Context, p config.Profile) (vpn.Session, error) {
	return &testSession{dial: (&net.Dialer{}).DialContext}, nil
}
func echoTarget(t *testing.T) string {
	t.Helper()
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { listener.Close() })
	go func() {
		for {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			go func() { defer conn.Close(); data, _ := io.ReadAll(conn); conn.Write(append([]byte("reply:"), data...)) }()
		}
	}()
	return listener.Addr().String()
}
func startDaemon(t *testing.T, c config.Config, factory vpn.Factory) (config.Config, map[string]string) {
	t.Helper()
	if c.Socket == "" {
		dir, err := os.MkdirTemp("", "och-test-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		c.Socket = filepath.Join(dir, "och.sock")
	}
	if c.Profiles == nil {
		c.Profiles = map[string]config.Profile{"campus": {Protocol: "anyconnect", Server: "https://vpn.invalid"}}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan map[string]string, 1)
	go func() { done <- Serve(ctx, c, factory, func(addresses map[string]string) { ready <- addresses }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("daemon shutdown hung")
		}
	})
	select {
	case addresses := <-ready:
		return c, addresses
	case err := <-done:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon not ready")
	}
	return c, nil
}
func TestProxyCommandIPCRealStream(t *testing.T) {
	address := echoTarget(t)
	host, port, _ := net.SplitHostPort(address)
	c, _ := startDaemon(t, config.Config{}, directFactory)
	payload := []byte("ssh-payload\x00\xff")
	var out bytes.Buffer
	err := ProxyCommand(context.Background(), c, Request{Action: "connect", Request: routing.Request{Host: host, Port: port, Via: "campus"}}, bytes.NewReader(payload), &out)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(out.Bytes(), append([]byte("reply:"), payload...)) {
		t.Fatalf("wrong stream %q", out.Bytes())
	}
	conn, reply, err := RequestControl(context.Background(), c, Request{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if reply.Profiles["campus"] != "ready" {
		t.Fatal(reply)
	}
	info, err := os.Stat(c.Socket)
	if err != nil || info.Mode().Perm() != 0600 {
		t.Fatal(info, err)
	}
}
func TestIPCPipelinedPayloadPreserved(t *testing.T) {
	address := echoTarget(t)
	host, port, _ := net.SplitHostPort(address)
	c, _ := startDaemon(t, config.Config{}, directFactory)
	conn, err := net.Dial("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	header, _ := json.Marshal(Request{Action: "connect", Request: routing.Request{Host: host, Port: port, Via: "campus"}})
	conn.Write(append(append(header, '\n'), []byte("pipelined")...))
	conn.(*net.UnixConn).CloseWrite()
	reader := bufio.NewReader(conn)
	line, err := reader.ReadString('\n')
	if err != nil || line != "{}\n" {
		t.Fatal(line, err)
	}
	data, err := io.ReadAll(reader)
	if err != nil || string(data) != "reply:pipelined" {
		t.Fatal(string(data), err)
	}
}
func TestMixedSOCKSAndCONNECT(t *testing.T) {
	address := echoTarget(t)
	host, portString, _ := net.SplitHostPort(address)
	c, addresses := startDaemon(t, config.Config{Listeners: map[string]config.Listener{"mixed": {Listen: "127.0.0.1:0", Via: "campus"}}}, directFactory)
	_ = c
	for _, mode := range []string{"socks", "connect"} {
		t.Run(mode, func(t *testing.T) {
			conn, err := net.Dial("tcp", addresses["mixed"])
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			conn.SetDeadline(time.Now().Add(5 * time.Second))
			reader := bufio.NewReader(conn)
			if mode == "socks" {
				conn.Write([]byte{5, 1, 0})
				reply := make([]byte, 2)
				io.ReadFull(reader, reply)
				if !bytes.Equal(reply, []byte{5, 0}) {
					t.Fatal(reply)
				}
				var port int
				fmt.Sscan(portString, &port)
				request := []byte{5, 1, 0, 3, byte(len(host))}
				request = append(request, host...)
				request = binary.BigEndian.AppendUint16(request, uint16(port))
				conn.Write(append(request, []byte("hello")...))
				response := make([]byte, 10)
				if _, err := io.ReadFull(reader, response); err != nil || response[1] != 0 {
					t.Fatal(response, err)
				}
			} else {
				fmt.Fprintf(conn, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\nhello", address, address)
				response, err := http.ReadResponse(reader, &http.Request{Method: "CONNECT"})
				if err != nil || response.StatusCode != 200 {
					t.Fatal(response, err)
				}
			}
			conn.(*net.TCPConn).CloseWrite()
			data, err := io.ReadAll(reader)
			if err != nil || string(data) != "reply:hello" {
				t.Fatal(string(data), err)
			}
		})
	}
}
func TestHTTPForwardDoesNotLeakProxyCredentials(t *testing.T) {
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Proxy-Authorization") != "" || r.Header.Get("X-Remove") != "" {
			t.Error("hop headers leaked")
		}
		fmt.Fprintf(w, "%s %s", r.Method, r.URL.Path)
	}))
	defer target.Close()
	_, addresses := startDaemon(t, config.Config{Listeners: map[string]config.Listener{"mixed": {Listen: "127.0.0.1:0", Via: "campus"}}}, directFactory)
	conn, err := net.Dial("tcp", addresses["mixed"])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	fmt.Fprintf(conn, "GET %s/test HTTP/1.1\r\nHost: ignored.invalid\r\nProxy-Authorization: secret\r\nConnection: X-Remove\r\nX-Remove: value\r\n\r\n", target.URL)
	response, err := http.ReadResponse(bufio.NewReader(conn), nil)
	if err != nil {
		t.Fatal(err)
	}
	defer response.Body.Close()
	body, _ := io.ReadAll(response.Body)
	if response.StatusCode != 200 || string(body) != "GET /test" {
		t.Fatal(response.StatusCode, string(body))
	}
}
func TestProxyCommandCancellation(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	accepted := make(chan net.Conn, 1)
	go func() {
		conn, err := listener.Accept()
		if err == nil {
			accepted <- conn
		}
	}()
	c, _ := startDaemon(t, config.Config{}, directFactory)
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	input, writer := io.Pipe()
	defer input.Close()
	defer writer.Close()
	done := make(chan error, 1)
	go func() {
		done <- ProxyCommand(ctx, c, Request{Action: "connect", Request: routing.Request{Host: host, Port: port, Via: "campus"}}, input, io.Discard)
	}()
	var remote net.Conn
	select {
	case remote = <-accepted:
		defer remote.Close()
	case <-time.After(3 * time.Second):
		t.Fatal("target not connected")
	}
	cancel()
	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("ProxyCommand ignored cancellation")
	}
}
func TestServiceStopClosesActiveStreams(t *testing.T) {
	address := echoTarget(t)
	host, port, _ := net.SplitHostPort(address)
	c, _ := startDaemon(t, config.Config{}, directFactory)
	stream, _, err := RequestControl(context.Background(), c, Request{Action: "connect", Request: routing.Request{Host: host, Port: port, Via: "campus"}})
	if err != nil {
		t.Fatal(err)
	}
	defer stream.Close()
	control, _, err := RequestControl(context.Background(), c, Request{Action: "stop"})
	if err != nil {
		t.Fatal(err)
	}
	control.Close()
	stream.SetReadDeadline(time.Now().Add(3 * time.Second))
	_, err = stream.Read(make([]byte, 1))
	if err == nil {
		t.Fatal("stream stayed open")
	}
	if e, ok := err.(net.Error); ok && e.Timeout() {
		t.Fatal("stop did not close stream")
	}
}
func TestOversizedHTTPHeadersRejected(t *testing.T) {
	_, addresses := startDaemon(t, config.Config{Listeners: map[string]config.Listener{"mixed": {Listen: "127.0.0.1:0", Via: "campus"}}}, func(context.Context, config.Profile) (vpn.Session, error) {
		t.Error("oversized header reached VPN")
		return nil, errors.New("unexpected")
	})
	conn, err := net.Dial("tcp", addresses["mixed"])
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	fmt.Fprintf(conn, "CONNECT host:22 HTTP/1.1\r\nHost: host:22\r\nX-Padding: %s\r\n\r\n", strings.Repeat("x", 70*1024))
	response, err := http.ReadResponse(bufio.NewReader(conn), &http.Request{Method: "CONNECT"})
	if err != nil || response.StatusCode != 431 {
		t.Fatal(response, err)
	}
	response.Body.Close()
}
func TestControlHandshakeHonorsCancellation(t *testing.T) {
	started := make(chan struct{})
	c, _ := startDaemon(t, config.Config{}, func(ctx context.Context, p config.Profile) (vpn.Session, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		conn, _, err := RequestControl(ctx, c, Request{Action: "connect", Request: routing.Request{Host: "host.invalid", Port: "22", Via: "campus"}})
		if conn != nil {
			conn.Close()
		}
		done <- err
	}()
	<-started
	cancel()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("cancelled handshake succeeded")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("handshake ignored cancellation")
	}
}
func TestStopAfterClientHalfCloseDoesNotWaitForTarget(t *testing.T) {
	for _, via := range []string{"direct", "campus"} {
		t.Run(via, func(t *testing.T) {
			listener, err := net.Listen("tcp", "127.0.0.1:0")
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			release := make(chan struct{})
			defer close(release)
			eof := make(chan struct{})
			go func() {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				io.Copy(io.Discard, conn)
				close(eof)
				<-release
			}()
			dir, err := os.MkdirTemp("", "och-stop-")
			if err != nil {
				t.Fatal(err)
			}
			defer os.RemoveAll(dir)
			c := config.Config{Socket: filepath.Join(dir, "och.sock"), Profiles: map[string]config.Profile{"campus": {}}}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			ready := make(chan struct{})
			done := make(chan error, 1)
			go func() { done <- Serve(ctx, c, directFactory, func(map[string]string) { close(ready) }) }()
			select {
			case <-ready:
			case err := <-done:
				t.Fatal(err)
			case <-time.After(3 * time.Second):
				t.Fatal("not ready")
			}
			host, port, _ := net.SplitHostPort(listener.Addr().String())
			conn, _, err := RequestControl(ctx, c, Request{Action: "connect", Request: routing.Request{Via: via, Host: host, Port: port}})
			if err != nil {
				t.Fatal(err)
			}
			defer conn.Close()
			if err = conn.(interface{ CloseWrite() error }).CloseWrite(); err != nil {
				t.Fatal(err)
			}
			select {
			case <-eof:
			case <-time.After(3 * time.Second):
				t.Fatal("target did not receive EOF")
			}
			stop, _, err := RequestControl(ctx, c, Request{Action: "stop"})
			if err != nil {
				t.Fatal(err)
			}
			stop.Close()
			select {
			case err := <-done:
				if err != nil {
					t.Fatal(err)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("stop waited for half-closed target")
			}
		})
	}
}
