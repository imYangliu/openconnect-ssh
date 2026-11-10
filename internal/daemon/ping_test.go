package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type pingRemoteConn struct{ net.Conn }

func (pingRemoteConn) RemoteAddr() net.Addr { return &net.IPAddr{IP: net.ParseIP("192.0.2.9")} }

func echoPingSession(t *testing.T) vpn.Session {
	t.Helper()
	return &testSession{dial: func(ctx context.Context, network, address string) (net.Conn, error) {
		if network != "ping" || address != "192.0.2.9" {
			return nil, errors.New("unexpected ping destination")
		}
		client, server := net.Pipe()
		go func() {
			defer server.Close()
			packet := make([]byte, 1500)
			n, err := server.Read(packet)
			if err != nil {
				return
			}
			message, err := icmp.ParseMessage(1, packet[:n])
			if err != nil || message.Type != ipv4.ICMPTypeEcho {
				return
			}
			message.Type = ipv4.ICMPTypeEchoReply
			reply, err := message.Marshal(nil)
			if err == nil {
				server.Write(reply)
			}
		}()
		return pingRemoteConn{client}, nil
	}}
}

func TestPingIPCRealEcho(t *testing.T) {
	c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) { return echoPingSession(t), nil })
	result, err := Ping(context.Background(), c, "campus", "192.0.2.9", 7, time.Second)
	if err != nil {
		t.Fatal(err)
	}
	if result.Address != "192.0.2.9" || result.Sequence != 7 || result.Bytes <= 0 || result.RTT < 0 {
		t.Fatalf("wrong echo result %+v", result)
	}
}

func TestPingIPCValidation(t *testing.T) {
	c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) {
		t.Error("invalid ping initialized VPN")
		return nil, errors.New("unexpected initialization")
	})
	base := Request{Action: "ping", PingSequence: 1, Timeout: time.Second, Request: routing.Request{Via: "campus", Host: "192.0.2.9"}}
	for _, change := range []func(*Request){
		func(r *Request) { r.Via = "direct" },
		func(r *Request) { r.Via = "unknown" },
		func(r *Request) { r.Policy = "auto" },
		func(r *Request) { r.Port = "80" },
		func(r *Request) { r.Timeout = 0 },
		func(r *Request) { r.Timeout = 61 * time.Second },
		func(r *Request) { r.PingSequence = 0 },
		func(r *Request) { r.PingSequence = 65536 },
		func(r *Request) { r.Host = "https://example.test" },
		func(r *Request) { r.Host = "example.test:80" },
	} {
		request := base
		change(&request)
		conn, _, err := RequestControl(context.Background(), c, request)
		if conn != nil {
			conn.Close()
		}
		if err == nil {
			t.Fatalf("accepted invalid request %+v", request)
		}
	}
}

func TestPingTimeoutCancellationAndDisconnect(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel", "disconnect"} {
		t.Run(mode, func(t *testing.T) {
			started := make(chan struct{})
			stopped := make(chan struct{})
			c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) {
				return &testSession{dial: func(ctx context.Context, _, _ string) (net.Conn, error) {
					close(started)
					<-ctx.Done()
					close(stopped)
					return nil, ctx.Err()
				}}, nil
			})
			request := Request{Action: "ping", Timeout: time.Second, PingSequence: 1, Request: routing.Request{Via: "campus", Host: "192.0.2.9"}}
			if mode == "disconnect" {
				conn, err := net.Dial("unix", c.Socket)
				if err != nil {
					t.Fatal(err)
				}
				json.NewEncoder(conn).Encode(request)
				<-started
				conn.Close()
			} else {
				ctx, cancel := context.WithCancel(context.Background())
				defer cancel()
				if mode == "timeout" {
					request.Timeout = 40 * time.Millisecond
				}
				done := make(chan error, 1)
				go func() {
					conn, _, err := RequestControl(ctx, c, request)
					if conn != nil {
						conn.Close()
					}
					done <- err
				}()
				<-started
				if mode == "cancel" {
					cancel()
				}
				select {
				case err := <-done:
					if err == nil || mode == "timeout" && !strings.Contains(err.Error(), "timeout") {
						t.Fatalf("wrong %s result %v", mode, err)
					}
				case <-time.After(2 * time.Second):
					t.Fatal("ping client did not return")
				}
			}
			select {
			case <-stopped:
			case <-time.After(500 * time.Millisecond):
				t.Fatal("probe ignored timeout or client disconnect")
			}
		})
	}
}

func TestPingSanitizesBackendErrors(t *testing.T) {
	for _, tc := range []struct {
		err  error
		want string
	}{
		{errors.New("Cookie=private-cookie; SID=private-session https://vpn.invalid/?ticket=private-ticket"), "VPN ping failed"},
		{vpn.ErrLoginRequired, "login"},
		{vpn.ErrResourceDenied, "resource denied"},
		{vpn.ErrAddressFamily, "address family"},
	} {
		c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) { return nil, tc.err })
		_, err := Ping(context.Background(), c, "campus", "192.0.2.9", 1, time.Second)
		if err == nil || !strings.Contains(strings.ToLower(err.Error()), strings.ToLower(tc.want)) || strings.Contains(err.Error(), "private-") {
			t.Fatalf("unsafe or misleading ping error %v", err)
		}
	}
}

func TestPingRejectsOldAndMalformedProtocol(t *testing.T) {
	for _, body := range []string{
		`{"error":"unknown action"}`,
		`{}`,
		`{"ping_version":2,"ping":{"address":"192.0.2.9","sequence":1,"bytes":64,"rtt":1000}}`,
		`{"ping_version":1,"ping":{"address":"invalid","sequence":1,"bytes":64,"rtt":1000}}`,
		`{"ping_version":1,"ping":{"address":"192.0.2.9","sequence":2,"bytes":64,"rtt":1000}}`,
	} {
		dir, err := os.MkdirTemp("", "och-ping-old-")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { os.RemoveAll(dir) })
		path := filepath.Join(dir, "och.sock")
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
		_, err = Ping(context.Background(), config.Config{Socket: path, Profiles: map[string]config.Profile{"campus": {}}}, "campus", "192.0.2.9", 1, time.Second)
		listener.Close()
		<-done
		if err == nil || !strings.Contains(err.Error(), "restart") {
			t.Fatalf("accepted old or malformed protocol %s, %v", body, err)
		}
	}
}

func TestPingTargetSyntax(t *testing.T) {
	for _, host := range []string{"192.0.2.9", "2001:db8::1", "host", "example.test.", "xn--bcher-kva.test"} {
		if err := ValidatePingTarget(host); err != nil {
			t.Fatalf("rejected %q: %v", host, err)
		}
	}
	for _, host := range []string{"", "-host", "host-", "a..b", "a/b", "a:b", "[::1]", "fe80::1%lo0", "a b", "a\nb", "https://a.test", "999.0.0.1", "192.168.001.1", strings.Repeat("a", 64) + ".test"} {
		if err := ValidatePingTarget(host); err == nil {
			t.Fatalf("accepted malformed target %q", host)
		}
	}
}

func TestPingTimeoutIncludesSharedInitialization(t *testing.T) {
	started := make(chan struct{})
	release := make(chan struct{})
	defer close(release)
	c, _ := startDaemon(t, config.Config{}, func(ctx context.Context, _ config.Profile) (vpn.Session, error) {
		close(started)
		select {
		case <-release:
			return echoPingSession(t), nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	})
	_, err := Ping(context.Background(), c, "campus", "192.0.2.9", 1, 40*time.Millisecond)
	if err == nil {
		t.Fatal("initialization exceeded timeout but ping succeeded")
	}
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("ping did not start shared initialization")
	}
	conn, status, err := RequestControl(context.Background(), c, Request{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if status.Profiles["campus"] != "connecting" {
		t.Fatalf("one probe timeout canceled shared initialization: %+v", status)
	}
	release <- struct{}{}
	if _, err := Ping(context.Background(), c, "campus", "192.0.2.9", 2, time.Second); err != nil {
		t.Fatalf("shared session did not survive probe timeout: %v", err)
	}
}

func TestPingDisconnectClosesOpenSocket(t *testing.T) {
	written := make(chan struct{})
	closed := make(chan struct{})
	c, _ := startDaemon(t, config.Config{}, func(context.Context, config.Profile) (vpn.Session, error) {
		return &testSession{dial: func(context.Context, string, string) (net.Conn, error) {
			client, server := net.Pipe()
			go func() {
				defer server.Close()
				buffer := make([]byte, 1500)
				if _, err := server.Read(buffer); err != nil {
					return
				}
				close(written)
				if _, err := server.Read(buffer); err != nil {
					close(closed)
				}
			}()
			return pingRemoteConn{client}, nil
		}}, nil
	})
	conn, err := net.Dial("unix", c.Socket)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	request := Request{Action: "ping", Timeout: time.Second, PingSequence: 1, Request: routing.Request{Via: "campus", Host: "192.0.2.9"}}
	if err := json.NewEncoder(conn).Encode(request); err != nil {
		t.Fatal(err)
	}
	select {
	case <-written:
	case <-time.After(time.Second):
		t.Fatal("ping did not send echo")
	}
	conn.Close()
	select {
	case <-closed:
	case <-time.After(500 * time.Millisecond):
		t.Fatal("client disconnect left the tracked ping socket open")
	}
}
