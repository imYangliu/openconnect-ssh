package forward_test

import (
	"context"
	"encoding/pem"
	"io"
	"net"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/forward"
	"golang.org/x/crypto/ssh"
	"golang.org/x/crypto/ssh/agent"
	"golang.org/x/crypto/ssh/knownhosts"
)

func TestNativeLocalRemoteAndSharedSession(t *testing.T) {
	s := newSSHFixture(t, false)
	target := responseServer(t)
	c := config.Config{SSH: map[string]config.SSHProfile{"compute": s.profile}}
	rules := map[string]config.Forward{
		"a-local":  {SSH: "compute", Listen: "127.0.0.1:0", Target: target},
		"b-remote": {Direction: "remote", SSH: "compute", Listen: "127.0.0.1:0", Target: target},
		"c-direct": {Via: "direct", Listen: "127.0.0.1:0", Target: target},
	}
	events, _, _ := startForward(t, c, rules, directDial)
	local := readyEvent(t, events, "a-local")
	remote := readyEvent(t, events, "b-remote")
	direct := readyEvent(t, events, "c-direct")
	var wg sync.WaitGroup
	for _, address := range []string{local, local, remote, remote, direct} {
		wg.Add(1)
		go func() { defer wg.Done(); assertExchange(t, address) }()
	}
	wg.Wait()
	if got := s.logins.Load(); got != 1 {
		t.Fatalf("multiple rules authenticated %d SSH sessions", got)
	}
}

func TestTargetRefusalPreservesSSHAndTransportReconnects(t *testing.T) {
	s := newSSHFixture(t, false)
	target := responseServer(t)
	unavailable, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	dead := unavailable.Addr().String()
	unavailable.Close()
	c := config.Config{SSH: map[string]config.SSHProfile{"compute": s.profile}}
	rules := map[string]config.Forward{
		"a-bad":     {SSH: "compute", Listen: "127.0.0.1:0", Target: dead},
		"b-good":    {SSH: "compute", Listen: "127.0.0.1:0", Target: target},
		"c-reverse": {Direction: "remote", SSH: "compute", Listen: "127.0.0.1:0", Target: target},
	}
	events, _, _ := startForward(t, c, rules, directDial)
	bad := readyEvent(t, events, "a-bad")
	good := readyEvent(t, events, "b-good")
	readyEvent(t, events, "c-reverse")
	exchange(bad)
	assertExchange(t, good)
	if s.logins.Load() != 1 {
		t.Fatal("target refusal discarded healthy SSH session")
	}
	s.drop()
	rebound := readyEvent(t, events, "c-reverse")
	assertExchange(t, rebound)
	assertExchange(t, good)
	if s.logins.Load() != 2 {
		t.Fatalf("recovery authenticated %d sessions", s.logins.Load())
	}
}

func TestHostKeysAndAuthenticationFailClosed(t *testing.T) {
	s := newSSHFixture(t, false)
	wrong := newSSHFixture(t, false)
	for _, tc := range []struct {
		name  string
		alter func(*config.SSHProfile)
		want  string
	}{
		{"unknown", func(p *config.SSHProfile) {
			p.KnownHostsFile = filepath.Join(t.TempDir(), "empty")
			os.WriteFile(p.KnownHostsFile, nil, 0600)
		}, "host key verification failed"},
		{"changed", func(p *config.SSHProfile) {
			body, _ := os.ReadFile(wrong.profile.KnownHostsFile)
			body = []byte(strings.ReplaceAll(string(body), strings.Split(string(body), " ")[0], strings.Split(mustRead(t, s.profile.KnownHostsFile), " ")[0]))
			p.KnownHostsFile = filepath.Join(t.TempDir(), "changed")
			os.WriteFile(p.KnownHostsFile, body, 0600)
		}, "host key verification failed"},
		{"authentication", func(p *config.SSHProfile) { p.Username = "rejected" }, "unable to authenticate"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			p := s.profile
			tc.alter(&p)
			c := config.Config{SSH: map[string]config.SSHProfile{"compute": p}}
			err := forward.Run(context.Background(), c, map[string]config.Forward{"test": {SSH: "compute", Listen: "127.0.0.1:0", Target: "127.0.0.1:80"}}, directDial, nil)
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("got %v, want %s", err, tc.want)
			}
		})
	}
}

func TestRemoteBindAfterSSHTransportCloseReturns(t *testing.T) {
	s := newSSHFixture(t, false)
	signer, err := ssh.ParsePrivateKey([]byte(mustRead(t, s.profile.IdentityFile)))
	if err != nil {
		t.Fatal(err)
	}
	verify, err := knownhosts.New(s.profile.KnownHostsFile)
	if err != nil {
		t.Fatal(err)
	}
	c, err := ssh.Dial("tcp", s.profile.Address, &ssh.ClientConfig{User: "fixture", Auth: []ssh.AuthMethod{ssh.PublicKeys(signer)}, HostKeyCallback: verify})
	if err != nil {
		t.Fatal(err)
	}
	c.Close()
	c.Wait()
	done := make(chan error, 1)
	go func() { _, err := c.Listen("tcp", "127.0.0.1:0"); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("remote bind succeeded after SSH shutdown")
		}
	case <-time.After(time.Second):
		t.Fatal("remote bind stalled after SSH shutdown")
	}
}

func mustRead(t *testing.T, path string) string {
	t.Helper()
	b, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestEncryptedKeyPasswordAndAgentAuthentication(t *testing.T) {
	s := newSSHFixture(t, false)
	rejected := newSSHFixture(t, false)
	target := responseServer(t)
	t.Setenv("OCH_TEST_PASSPHRASE", "fixture-passphrase")
	t.Setenv("OCH_TEST_SSH_PASSWORD", "fixture-password")
	block, err := ssh.MarshalPrivateKeyWithPassphrase(s.key, "fixture", []byte("fixture-passphrase"))
	if err != nil {
		t.Fatal(err)
	}
	encrypted := filepath.Join(t.TempDir(), "encrypted")
	if err := os.WriteFile(encrypted, pem.EncodeToMemory(block), 0600); err != nil {
		t.Fatal(err)
	}
	agentRoot, err := os.MkdirTemp("/tmp", "och-agent-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(agentRoot) })
	agentSocket := filepath.Join(agentRoot, "agent.sock")
	l, err := net.Listen("unix", agentSocket)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { l.Close() })
	failedAgent, err := net.Listen("unix", filepath.Join(agentRoot, "failed.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { failedAgent.Close() })
	go func() {
		for {
			c, err := failedAgent.Accept()
			if err != nil {
				return
			}
			c.Close()
		}
	}()
	stalledAgent, err := net.Listen("unix", filepath.Join(agentRoot, "stalled.sock"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { stalledAgent.Close() })
	go func() {
		for {
			c, err := stalledAgent.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); io.Copy(io.Discard, c) }()
		}
	}()
	keyring := agent.NewKeyring()
	if err := keyring.Add(agent.AddedKey{PrivateKey: s.key}); err != nil {
		t.Fatal(err)
	}
	go func() {
		for {
			c, err := l.Accept()
			if err != nil {
				return
			}
			go func() { defer c.Close(); agent.ServeAgent(keyring, c) }()
		}
	}()
	for _, mode := range []string{"encrypted-key", "password", "agent", "rejected-key-agent", "key-unavailable-agent", "key-failing-agent", "password-failing-agent", "key-stalled-agent", "password-stalled-agent", "rejected-key-password"} {
		t.Run(mode, func(t *testing.T) {
			p := s.profile
			t.Setenv("SSH_AUTH_SOCK", "")
			switch mode {
			case "encrypted-key":
				p.IdentityFile = encrypted
				p.KeyPassphraseEnv = "OCH_TEST_PASSPHRASE"
			case "password":
				p.IdentityFile = ""
				p.PasswordEnv = "OCH_TEST_SSH_PASSWORD"
			case "agent":
				p.IdentityFile = ""
				t.Setenv("SSH_AUTH_SOCK", agentSocket)
			case "rejected-key-agent":
				p.IdentityFile = rejected.profile.IdentityFile
				t.Setenv("SSH_AUTH_SOCK", agentSocket)
			case "key-unavailable-agent":
				t.Setenv("SSH_AUTH_SOCK", filepath.Join(agentRoot, "missing.sock"))
			case "key-failing-agent":
				t.Setenv("SSH_AUTH_SOCK", failedAgent.Addr().String())
			case "password-failing-agent":
				p.IdentityFile = ""
				p.PasswordEnv = "OCH_TEST_SSH_PASSWORD"
				t.Setenv("SSH_AUTH_SOCK", failedAgent.Addr().String())
			case "key-stalled-agent":
				t.Setenv("SSH_AUTH_SOCK", stalledAgent.Addr().String())
			case "password-stalled-agent":
				p.IdentityFile = ""
				p.PasswordEnv = "OCH_TEST_SSH_PASSWORD"
				t.Setenv("SSH_AUTH_SOCK", stalledAgent.Addr().String())
			case "rejected-key-password":
				p.IdentityFile = rejected.profile.IdentityFile
				p.PasswordEnv = "OCH_TEST_SSH_PASSWORD"
			}
			c := config.Config{SSH: map[string]config.SSHProfile{"compute": p}}
			events, _, _ := startForward(t, c, map[string]config.Forward{"test": {SSH: "compute", Listen: "127.0.0.1:0", Target: target}}, directDial)
			assertExchange(t, readyEvent(t, events, "test"))
		})
	}
}

func TestCancellationUnblocksHandshakeAndRemoteBind(t *testing.T) {
	for _, mode := range []string{"handshake", "remote-bind"} {
		t.Run(mode, func(t *testing.T) {
			s := newSSHFixture(t, true)
			p := s.profile
			if mode == "handshake" {
				l, err := net.Listen("tcp", "127.0.0.1:0")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { l.Close() })
				go func() {
					c, err := l.Accept()
					if err == nil {
						defer c.Close()
						buf := make([]byte, 1024)
						for {
							if _, err := c.Read(buf); err != nil {
								return
							}
						}
					}
				}()
				p.Address = l.Addr().String()
			}
			ctx, cancel := context.WithCancel(context.Background())
			done := make(chan error, 1)
			go func() {
				done <- forward.Run(ctx, config.Config{SSH: map[string]config.SSHProfile{"compute": p}}, map[string]config.Forward{"test": {Direction: "remote", SSH: "compute", Listen: "127.0.0.1:0", Target: "127.0.0.1:80"}}, directDial, nil)
			}()
			time.Sleep(100 * time.Millisecond)
			cancel()
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("cancellation left SSH operation blocked")
			}
		})
	}
}

func TestStartupRollbackAndHeldStreamCancellation(t *testing.T) {
	occupied, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer occupied.Close()
	first, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := first.Addr().String()
	first.Close()
	rules := map[string]config.Forward{
		"a": {Via: "direct", Listen: address, Target: "127.0.0.1:80"},
		"b": {Via: "direct", Listen: occupied.Addr().String(), Target: "127.0.0.1:80"},
	}
	if err := forward.Run(context.Background(), config.Config{}, rules, directDial, nil); err == nil {
		t.Fatal("accepted occupied listener")
	}
	l, err := net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("startup leaked prior listener: %v", err)
	}
	l.Close()
	s := newSSHFixture(t, false)
	remoteRules := map[string]config.Forward{
		"a-local":  {Via: "direct", Listen: address, Target: "127.0.0.1:80"},
		"b-remote": {Direction: "remote", SSH: "compute", Listen: occupied.Addr().String(), Target: "127.0.0.1:80"},
	}
	settings := config.Config{SSH: map[string]config.SSHProfile{"compute": s.profile}}
	if err := forward.Run(context.Background(), settings, remoteRules, directDial, nil); err == nil {
		t.Fatal("accepted occupied remote listener")
	}
	l, err = net.Listen("tcp", address)
	if err != nil {
		t.Fatalf("remote bind failure leaked local listener: %v", err)
	}
	l.Close()
	events, cancel, _ := startForward(t, config.Config{SSH: map[string]config.SSHProfile{"compute": s.profile}}, map[string]config.Forward{"held": {SSH: "compute", Listen: "127.0.0.1:0", Target: responseServer(t)}}, directDial)
	held := readyEvent(t, events, "held")
	c, err := net.Dial("tcp", held)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	c.Write([]byte("unfinished"))
	cancel()
	c.SetReadDeadline(time.Now().Add(2 * time.Second))
	buf := make([]byte, 1)
	if _, err := c.Read(buf); err == nil {
		t.Fatal("held stream survived cancellation")
	}
}
