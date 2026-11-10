package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

func TestTCPingIPCConnectsAndClosesWithoutData(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer listener.Close()
	read := make(chan error, 1)
	go func() {
		conn, err := listener.Accept()
		if err != nil {
			read <- err
			return
		}
		defer conn.Close()
		conn.SetReadDeadline(time.Now().Add(time.Second))
		var b [1]byte
		n, err := conn.Read(b[:])
		if n != 0 || err != io.EOF {
			read <- fmt.Errorf("received application bytes or no EOF: %d, %v", n, err)
			return
		}
		read <- nil
	}()
	_, port, _ := net.SplitHostPort(listener.Addr().String())
	c, _ := startDaemon(t, config.Config{Profiles: map[string]config.Profile{
		"campus": {Server: "selected"}, "other": {Server: "wrong"},
	}}, func(_ context.Context, p config.Profile) (vpn.Session, error) {
		if p.Server != "selected" {
			t.Errorf("initialized wrong profile %q", p.Server)
		}
		return &testSession{dial: func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != net.JoinHostPort("example.test", port) {
				t.Errorf("wrong target %s %s", network, address)
			}
			return (&net.Dialer{}).DialContext(ctx, network, listener.Addr().String())
		}}, nil
	})
	result, err := TCPing(context.Background(), c, "campus", "example.test", port, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Address != net.JoinHostPort("example.test", port) || result.Duration < 0 || result.Duration > time.Second {
		t.Fatalf("wrong result %+v", result)
	}
	select {
	case err := <-read:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("target did not receive EOF")
	}
}

func TestTCPingRefusedLoopback(t *testing.T) {
	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ := net.SplitHostPort(listener.Addr().String())
	listener.Close()
	c, _ := startDaemon(t, config.Config{}, directFactory)
	_, err = TCPing(context.Background(), c, "campus", host, port, time.Second)
	if err == nil || err.Error() != "tcping connection refused" {
		t.Fatalf("wrong refused result %v", err)
	}
}

func TestTCPingIPv6RequestedAddress(t *testing.T) {
	c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) {
		return &testSession{dial: func(_ context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != "[2001:db8::1]:443" {
				t.Errorf("wrong IPv6 target %s %s", network, address)
			}
			client, server := net.Pipe()
			server.Close()
			return client, nil
		}}, nil
	})
	result, err := TCPing(context.Background(), c, "campus", "2001:db8::1", "443", time.Second)
	if err != nil || result.Address != "[2001:db8::1]:443" {
		t.Fatalf("IPv6 result %+v, %v", result, err)
	}
}

func TestTCPingValidationNeverInitializesVPN(t *testing.T) {
	c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) {
		t.Error("invalid request initialized VPN")
		return nil, errors.New("unexpected initialization")
	})
	base := Request{Action: "tcping", Timeout: time.Second, Request: routing.Request{Via: "campus", Host: "example.test", Port: "443"}}
	changes := []func(*Request){
		func(r *Request) { r.Via = "" }, func(r *Request) { r.Via = "direct" }, func(r *Request) { r.Via = "unknown" },
		func(r *Request) { r.Policy = "auto" }, func(r *Request) { r.Timeout = 0 }, func(r *Request) { r.Timeout = 61 * time.Second },
	}
	for _, host := range []string{"", "https://secret.invalid/?token=private", "[::1]", "fe80::1%lo0", "host:80", "999.0.0.1", "a b", "bücher.test"} {
		changes = append(changes, func(r *Request) { r.Host = host })
	}
	for _, port := range []string{"", "0", "65536", "-1", "+80", "http", " 80", "8\n0", "８０"} {
		changes = append(changes, func(r *Request) { r.Port = port })
	}
	for _, change := range changes {
		request := base
		change(&request)
		conn, _, err := RequestControl(context.Background(), c, request)
		if conn != nil {
			conn.Close()
		}
		if err == nil || strings.Contains(err.Error(), "private") {
			t.Fatalf("invalid request accepted or leaked %+v, %v", request, err)
		}
		if request.Policy != "" {
			continue
		}
		_, err = TCPing(context.Background(), c, request.Via, request.Host, request.Port, request.Timeout)
		if err == nil {
			t.Fatalf("client accepted invalid request %+v", request)
		}
	}
}

func TestTCPingTimeoutCancelDisconnectAndLateConnection(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "disconnect", "late success"} {
		t.Run(mode, func(t *testing.T) {
			started, stopped, closed := make(chan struct{}), make(chan struct{}), make(chan struct{})
			session := &testSession{dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
				close(started)
				<-ctx.Done()
				close(stopped)
				if mode != "late success" {
					return nil, ctx.Err()
				}
				client, server := net.Pipe()
				go func() {
					defer server.Close()
					var b [1]byte
					n, err := server.Read(b[:])
					if n == 0 && err == io.EOF {
						close(closed)
					}
				}()
				return client, nil
			}}
			c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) { return session, nil })
			request := Request{Action: "tcping", Timeout: time.Second, Request: routing.Request{Via: "campus", Host: "example.test", Port: "443"}}
			if mode == "disconnect" || mode == "late success" {
				conn, err := net.Dial("unix", c.Socket)
				if err != nil {
					t.Fatal(err)
				}
				if err := json.NewEncoder(conn).Encode(request); err != nil {
					t.Fatal(err)
				}
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("probe did not start")
				}
				conn.Close()
			} else {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "timeout" {
					request.Timeout = 40 * time.Millisecond
				}
				done := make(chan error, 1)
				go func() {
					_, err := TCPing(ctx, c, request.Via, request.Host, request.Port, request.Timeout)
					done <- err
				}()
				select {
				case <-started:
				case <-time.After(time.Second):
					t.Fatal("probe did not start")
				}
				if mode == "cancel" {
					cancel()
				}
				select {
				case err := <-done:
					if err == nil {
						t.Fatal("canceled probe succeeded")
					}
				case <-time.After(time.Second):
					t.Fatal("client did not stop")
				}
			}
			select {
			case <-stopped:
			case <-time.After(time.Second):
				t.Fatal("backend ignored cancellation")
			}
			if mode == "late success" {
				select {
				case <-closed:
				case <-time.After(time.Second):
					t.Fatal("late connection was not closed")
				}
			}
			if session.closed.Load() != 0 {
				t.Fatal("cancel closed shared VPN session")
			}
		})
	}
}

func TestTCPingTimeoutIncludesSharedInitialization(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	defer close(release)
	c, _ := startDaemon(t, config.Config{}, func(ctx context.Context, _ config.Profile) (vpn.Session, error) {
		close(started)
		select {
		case <-release:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		return &testSession{dial: func(context.Context, string, string) (net.Conn, error) { c, s := net.Pipe(); s.Close(); return c, nil }}, nil
	})
	_, err := TCPing(context.Background(), c, "campus", "example.test", "443", 40*time.Millisecond)
	if err == nil {
		t.Fatal("initialization exceeded probe budget but succeeded")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("no shared initialization")
	}
	conn, status, err := RequestControl(context.Background(), c, Request{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if status.Profiles["campus"] != "connecting" {
		t.Fatalf("probe canceled shared initialization %+v", status)
	}
	release <- struct{}{}
	if _, err := TCPing(context.Background(), c, "campus", "example.test", "443", time.Second); err != nil {
		t.Fatalf("shared session did not survive %v", err)
	}
}

func TestTCPingSanitizesBackendErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("Cookie=private-cookie; SID=private-session https://secret.invalid/?ticket=private-ticket"), "VPN tcping failed"},
		{fmt.Errorf("private-cookie: %w", syscall.ECONNREFUSED), "connection refused"},
		{vpn.ErrLoginRequired, "login"}, {vpn.LoginFailure("cas_http", 403), "403"},
		{vpn.ErrResourceDenied, "resource denied"}, {vpn.ErrAddressFamily, "address family"},
		{context.Canceled, "canceled"}, {context.DeadlineExceeded, "timeout"},
	} {
		c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) { return nil, tc.err })
		_, err := TCPing(context.Background(), c, "campus", "example.test", "443", time.Second)
		if err == nil || !strings.Contains(err.Error(), tc.want) || strings.Contains(err.Error(), "private") || strings.Contains(err.Error(), "https://") {
			t.Fatalf("unsafe error %v", err)
		}
	}
}

func TestTCPingRejectsOldAndMalformedProtocol(t *testing.T) {
	for _, body := range []string{
		`{"error":"unknown action"}`, `{}`, `invalid JSON`, `{"tcping_version":"invalid"}`, `{"tcping_version":2,"tcping":{"address":"example.test:443","duration":1}}`,
		`{"tcping_version":1}`, `{"tcping_version":1,"tcping":{"address":"127.0.0.1:443","duration":1}}`,
		`{"tcping_version":1,"tcping":{"address":"example.test:443","duration":-1}}`,
		`{"tcping_version":1,"tcping":{"address":"example.test:443","duration":2000000000}}`,
	} {
		dir, err := os.MkdirTemp("/tmp", "och-tcping-old-")
		if err != nil {
			t.Fatal(err)
		}
		defer os.RemoveAll(dir)
		path := filepath.Join(dir, "s")
		listener, err := net.Listen("unix", path)
		if err != nil {
			t.Fatal(err)
		}
		done := make(chan struct{})
		go func() {
			defer close(done)
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			defer conn.Close()
			bufio.NewReader(conn).ReadString('\n')
			conn.Write([]byte(body + "\n"))
		}()
		_, err = TCPing(context.Background(), config.Config{Socket: path, Profiles: map[string]config.Profile{"campus": {}}}, "campus", "example.test", "443", time.Second)
		listener.Close()
		<-done
		if err == nil || !strings.Contains(err.Error(), "restart") {
			t.Fatalf("accepted malformed result %s, %v", body, err)
		}
	}
}
