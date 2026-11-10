package forward_test

import (
	"context"
	"crypto/ed25519"
	"crypto/rand"
	"encoding/pem"
	"fmt"
	"io"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/forward"
	"github.com/imyangliu/openconnect-ssh/internal/proxy"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/knownhosts"
)

type sshFixture struct {
	listener  net.Listener
	server    *ssh.ServerConfig
	profile   config.SSHProfile
	key       ed25519.PrivateKey
	mu        sync.Mutex
	streams   map[net.Conn]struct{}
	wg        sync.WaitGroup
	logins    atomic.Int32
	blockBind bool
}

func newSSHFixture(t *testing.T, blockBind bool) *sshFixture {
	t.Helper()
	t.Setenv("SSH_AUTH_SOCK", "")
	_, hostKey, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	hostSigner, err := ssh.NewSignerFromKey(hostKey)
	if err != nil {
		t.Fatal(err)
	}
	_, key, err := ed25519.GenerateKey(rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	clientSigner, err := ssh.NewSignerFromKey(key)
	if err != nil {
		t.Fatal(err)
	}
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	identity := filepath.Join(root, "identity")
	block, err := ssh.MarshalPrivateKey(key, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(identity, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	known := filepath.Join(root, "known_hosts")
	line := knownhosts.Line([]string{knownhosts.Normalize(l.Addr().String())}, hostSigner.PublicKey()) + "\n"
	if err := os.WriteFile(known, []byte(line), 0600); err != nil {
		t.Fatal(err)
	}
	s := &sshFixture{listener: l, streams: make(map[net.Conn]struct{}), blockBind: blockBind, key: key,
		profile: config.SSHProfile{Address: l.Addr().String(), Username: "fixture", Via: "direct", IdentityFile: identity, KnownHostsFile: known}}
	s.server = &ssh.ServerConfig{PublicKeyCallback: func(m ssh.ConnMetadata, k ssh.PublicKey) (*ssh.Permissions, error) {
		if m.User() == "fixture" && string(k.Marshal()) == string(clientSigner.PublicKey().Marshal()) {
			return nil, nil
		}
		return nil, fmt.Errorf("fixture authentication rejected")
	}, PasswordCallback: func(m ssh.ConnMetadata, password []byte) (*ssh.Permissions, error) {
		if m.User() == "fixture" && string(password) == "fixture-password" {
			return nil, nil
		}
		return nil, fmt.Errorf("fixture authentication rejected")
	}}
	s.server.AddHostKey(hostSigner)
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			s.mu.Lock()
			s.streams[c] = struct{}{}
			s.mu.Unlock()
			s.wg.Add(1)
			go func() { defer s.wg.Done(); s.serve(c) }()
		}
	}()
	t.Cleanup(func() { l.Close(); s.drop(); s.wg.Wait() })
	return s
}

func (s *sshFixture) drop() {
	s.mu.Lock()
	defer s.mu.Unlock()
	for c := range s.streams {
		c.Close()
	}
}

type directRequest struct {
	Host       string
	Port       uint32
	Origin     string
	OriginPort uint32
}
type bindRequest struct {
	Host string
	Port uint32
}

func (s *sshFixture) serve(raw net.Conn) {
	defer raw.Close()
	defer func() { s.mu.Lock(); delete(s.streams, raw); s.mu.Unlock() }()
	c, channels, requests, err := ssh.NewServerConn(raw, s.server)
	if err != nil {
		return
	}
	s.logins.Add(1)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	var wg sync.WaitGroup
	var mu sync.Mutex
	binds := make(map[string]net.Listener)
	wg.Add(1)
	go func() {
		defer wg.Done()
		for req := range requests {
			switch req.Type {
			case "tcpip-forward":
				if s.blockBind {
					continue
				}
				var b bindRequest
				if ssh.Unmarshal(req.Payload, &b) != nil {
					req.Reply(false, nil)
					continue
				}
				l, err := net.Listen("tcp", net.JoinHostPort(b.Host, strconv.Itoa(int(b.Port))))
				if err != nil {
					req.Reply(false, nil)
					continue
				}
				port := uint32(l.Addr().(*net.TCPAddr).Port)
				address := net.JoinHostPort(b.Host, strconv.Itoa(int(port)))
				mu.Lock()
				binds[address] = l
				mu.Unlock()
				req.Reply(true, ssh.Marshal(struct{ Port uint32 }{port}))
				wg.Add(1)
				go func() {
					defer wg.Done()
					for {
						target, err := l.Accept()
						if err != nil {
							return
						}
						wg.Add(1)
						go func() {
							defer wg.Done()
							defer target.Close()
							origin := target.RemoteAddr().(*net.TCPAddr)
							ch, reqs, err := c.OpenChannel("forwarded-tcpip", ssh.Marshal(directRequest{b.Host, port, origin.IP.String(), uint32(origin.Port)}))
							if err != nil {
								return
							}
							defer ch.Close()
							go ssh.DiscardRequests(reqs)
							proxy.Relay(ctx, channelConnection{ch}, target)
						}()
					}
				}()
			case "cancel-tcpip-forward":
				var b bindRequest
				ssh.Unmarshal(req.Payload, &b)
				address := net.JoinHostPort(b.Host, strconv.Itoa(int(b.Port)))
				mu.Lock()
				l := binds[address]
				delete(binds, address)
				mu.Unlock()
				if l != nil {
					l.Close()
				}
				req.Reply(l != nil, nil)
			default:
				req.Reply(false, nil)
			}
		}
	}()
	wg.Add(1)
	go func() {
		defer wg.Done()
		for ch := range channels {
			if ch.ChannelType() != "direct-tcpip" {
				ch.Reject(ssh.UnknownChannelType, "unsupported")
				continue
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				var d directRequest
				if ssh.Unmarshal(ch.ExtraData(), &d) != nil {
					ch.Reject(ssh.ConnectionFailed, "invalid")
					return
				}
				target, err := (&net.Dialer{Timeout: time.Second}).DialContext(ctx, "tcp", net.JoinHostPort(d.Host, strconv.Itoa(int(d.Port))))
				if err != nil {
					ch.Reject(ssh.ConnectionFailed, "target unavailable")
					return
				}
				defer target.Close()
				stream, reqs, err := ch.Accept()
				if err != nil {
					return
				}
				defer stream.Close()
				go ssh.DiscardRequests(reqs)
				proxy.Relay(ctx, channelConnection{stream}, target)
			}()
		}
	}()
	c.Wait()
	cancel()
	mu.Lock()
	for _, l := range binds {
		l.Close()
	}
	mu.Unlock()
	wg.Wait()
}

type channelConnection struct{ ssh.Channel }

func (channelConnection) LocalAddr() net.Addr              { return &net.TCPAddr{} }
func (channelConnection) RemoteAddr() net.Addr             { return &net.TCPAddr{} }
func (channelConnection) SetDeadline(time.Time) error      { return nil }
func (channelConnection) SetReadDeadline(time.Time) error  { return nil }
func (channelConnection) SetWriteDeadline(time.Time) error { return nil }

func responseServer(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			wg.Add(1)
			go func() {
				defer wg.Done()
				defer c.Close()
				stop := context.AfterFunc(ctx, func() { c.Close() })
				defer stop()
				body, err := io.ReadAll(c)
				if err == nil {
					c.Write(append([]byte("reply:"), body...))
				}
			}()
		}
	}()
	t.Cleanup(func() { cancel(); l.Close(); wg.Wait() })
	return l.Addr().String()
}

func directDial(ctx context.Context, _ string, address string) (net.Conn, error) {
	return (&net.Dialer{}).DialContext(ctx, "tcp", address)
}

func startForward(t *testing.T, c config.Config, rules map[string]config.Forward, dial forward.DialFunc) (<-chan forward.Event, context.CancelFunc, <-chan error) {
	t.Helper()
	ctx, cancel := context.WithCancel(context.Background())
	events := make(chan forward.Event, 128)
	done := make(chan error, 1)
	go func() { done <- forward.Run(ctx, c, rules, dial, func(e forward.Event) { events <- e }) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			t.Error("forward cleanup timed out")
		}
	})
	return events, cancel, done
}

func readyEvent(t *testing.T, events <-chan forward.Event, name string) string {
	t.Helper()
	deadline := time.NewTimer(8 * time.Second)
	defer deadline.Stop()
	for {
		select {
		case e := <-events:
			if e.Name == name && e.State == "ready" {
				return e.Address
			}
		case <-deadline.C:
			t.Fatalf("forward %s never became ready", name)
			return ""
		}
	}
}

func exchange(address string) ([]byte, error) {
	c, err := net.DialTimeout("tcp", address, time.Second)
	if err != nil {
		return nil, err
	}
	defer c.Close()
	c.SetDeadline(time.Now().Add(2 * time.Second))
	if _, err = c.Write([]byte("fixture\x00\xff")); err != nil {
		return nil, err
	}
	if err = c.(*net.TCPConn).CloseWrite(); err != nil {
		return nil, err
	}
	return io.ReadAll(c)
}

func assertExchange(t *testing.T, address string) {
	t.Helper()
	got, err := exchange(address)
	if err != nil || string(got) != "reply:fixture\x00\xff" {
		t.Fatalf("exchange returned %q, %v", got, err)
	}
}
