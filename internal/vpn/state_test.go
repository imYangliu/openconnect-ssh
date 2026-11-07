package vpn

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/authstate"
	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
)

func TestATrustNativeStateRestoresWithoutBrowserAndSurvivesNetworkFailure(t *testing.T) {
	seed := httptest.NewTLSServer(http.NotFoundHandler())
	tlsConfig := seed.TLS.Clone()
	seed.Close()
	node, err := tls.Listen("tcp", "127.0.0.1:0", tlsConfig)
	if err != nil {
		t.Fatal(err)
	}
	defer node.Close()
	go func() {
		for {
			c, e := node.Accept()
			if e != nil {
				return
			}
			go func() {
				defer c.Close()
				c.SetDeadline(time.Now().Add(5 * time.Second))
				b := make([]byte, 4096)
				n, e := c.Read(b)
				if e != nil || n == 0 {
					return
				}
				body := []byte(`{"code":0}`)
				response := append([]byte{5, 0xd0, 0x53, 0, byte(len(body) >> 8), byte(len(body))}, body...)
				response = append(response, 5, 0, 0, 1, 10, 20, 0, 2, 0, 0)
				c.Write(response)
			}()
		}
	}()
	var mode atomic.Int32
	var cookieRequests atomic.Int32
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/public/manifest":
			fmt.Fprint(w, `{"code":0,"data":{}}`)
		case "/passport/v1/public/authConfig":
			if mode.Load() == 1 {
				http.Error(w, "fixture transport unavailable", 503)
				return
			}
			cookie, e := r.Cookie("sid")
			if e != nil || cookie.Value != "fixture-session" {
				t.Error("saved cookie missing")
			}
			cookieRequests.Add(1)
			if mode.Load() == 2 {
				fmt.Fprint(w, `{"code":0,"data":{"isLogin":0}}`)
			} else {
				fmt.Fprint(w, `{"code":0,"data":{"isLogin":1}}`)
			}
		case "/passport/v1/user/onlineInfo":
			fmt.Fprint(w, `{"code":0,"data":{"username":"fixture"}}`)
		case "/controller/v1/user/clientResource":
			fmt.Fprintf(w, `{"data":{"appList":{"data":{"config":{"nodeGroupConf":{"majorNodeGroup":{"id":"one"},"nodeGroupList":[{"id":"one","addressInfo":[{"address":%q,"type":"wan"}]}]}}}}}}`, node.Addr().String())
		default:
			t.Errorf("unexpected auth request %s", r.URL.Path)
			http.Error(w, "unexpected", 500)
		}
	}))
	defer gateway.Close()
	p := config.Profile{Protocol: "atrust", Server: gateway.URL, AuthType: "auth/cas", LoginDomain: "campus", Insecure: true, AuthStateFile: filepath.Join(t.TempDir(), "auth", "state.json")}
	u, _ := url.Parse(gateway.URL)
	saved, _ := json.Marshal(auth.ClientAuthData{DeviceID: "fixture-device", Cookies: []auth.Cookie{{Host: u.Host, Scheme: "https", Name: "sid", Value: "fixture-session"}}})
	binding, _ := stateBinding(p)
	store, e := authstate.Open(p.AuthStateFile, binding)
	if e != nil {
		t.Fatal(e)
	}
	if e = store.Save(saved); e != nil {
		t.Fatal(e)
	}
	store.Close()
	for i := 0; i < 2; i++ {
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		session, e := NewNativeSession(ctx, p)
		if e != nil {
			cancel()
			t.Fatal("cache restore", e)
		}
		if e = session.Close(); e != nil {
			t.Fatal(e)
		}
		cancel()
	}
	if cookieRequests.Load() != 2 {
		t.Fatal("restore did not use saved session")
	}
	childCtx, childCancel := context.WithTimeout(context.Background(), 15*time.Second)
	assertFreshProcessRestore(t, childCtx, p)
	childCancel()
	if cookieRequests.Load() != 3 {
		t.Fatal("fresh process did not restore saved aTrust cookie")
	}
	mode.Store(1)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, e = NewNativeSession(ctx, p); e == nil {
		t.Fatal("network failure accepted")
	}
	if _, e = os.Stat(p.AuthStateFile); e != nil {
		t.Fatal("network failure deleted cache")
	}
	mode.Store(2)
	if _, e = NewNativeSession(ctx, p); !errors.Is(e, ErrLoginRequired) {
		t.Fatal("expired session did not request login", e)
	}
	if _, e = os.Stat(p.AuthStateFile); !os.IsNotExist(e) {
		t.Fatal("expired state not cleared")
	}
}
func TestATrustCookieCannotCrossGateway(t *testing.T) {
	p := config.Profile{Protocol: "atrust", Server: "https://vpn.example", AuthType: "auth/cas"}
	for _, host := range []string{"evil.example", "vpn.example:444", "user@vpn.example", "vpn.example/path"} {
		raw, _ := json.Marshal(auth.ClientAuthData{DeviceID: "fixture", Cookies: []auth.Cookie{{Host: host, Scheme: "https", Name: "sid", Value: "private-fixture"}}})
		e := validateATrustState(raw, p)
		if e == nil || strings.Contains(e.Error(), "private-fixture") {
			t.Fatal("cross gateway cookie accepted", host, e)
		}
	}
}
func TestAuthenticationBindingIncludesIdentityAndCanonicalOrigin(t *testing.T) {
	a := config.Profile{Protocol: "atrust", Server: "https://VPN.EXAMPLE:443/", AuthType: "auth/cas", LoginDomain: "one", Username: "user"}
	b := a
	b.Server = "https://vpn.example"
	x, _ := stateBinding(a)
	y, _ := stateBinding(b)
	if x != y {
		t.Fatal("equivalent origin differs")
	}
	b.LoginDomain = "two"
	y, _ = stateBinding(b)
	if x == y {
		t.Fatal("domain not bound")
	}
}
func TestLogoutClosesProfileStreamsAndClearsState(t *testing.T) {
	p := config.Profile{Protocol: "atrust", Server: "https://vpn.example", AuthStateFile: filepath.Join(t.TempDir(), "auth", "state.json")}
	binding, _ := stateBinding(p)
	s, e := authstate.Open(p.AuthStateFile, binding)
	if e != nil {
		t.Fatal(e)
	}
	s.Save([]byte(`{"fixture":true}`))
	s.Close()
	server, client := net.Pipe()
	defer server.Close()
	m := NewManager(context.Background(), map[string]config.Profile{"campus": p}, func(context.Context, config.Profile) (Session, error) {
		return &testSession{dial: func(context.Context, string, string) (net.Conn, error) { return client, nil }}, nil
	})
	defer m.Close()
	conn, e := m.Dial(context.Background(), "campus", "host:22")
	if e != nil {
		t.Fatal(e)
	}
	defer conn.Close()
	if e = m.Logout(context.Background(), "campus"); e != nil {
		t.Fatal(e)
	}
	if _, e = conn.Write([]byte("x")); e == nil {
		t.Fatal("logout left stream open")
	}
	if _, e = os.Stat(p.AuthStateFile); !os.IsNotExist(e) {
		t.Fatal("logout left cache")
	}
}

func TestAnyConnectCookieBootstrapAndRestartWithoutPassword(t *testing.T) {
	var authRequests atomic.Int32
	var connectRequests atomic.Int32
	var reject atomic.Bool
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "CONNECT" {
			connectRequests.Add(1)
			cookie, e := r.Cookie("webvpn")
			if reject.Load() || e != nil || cookie.Value != "fixture-session" {
				http.Error(w, "rejected", 401)
				return
			}
			conn, rw, e := w.(http.Hijacker).Hijack()
			if e != nil {
				t.Error(e)
				return
			}
			defer conn.Close()
			rw.WriteString("HTTP/1.1 200 CONNECTED\r\nX-CSTP-Version: 1\r\nX-CSTP-MTU: 1400\r\nX-CSTP-Address: 10.20.0.2\r\nX-CSTP-Netmask: 255.255.255.0\r\n\r\n")
			rw.Flush()
			b := make([]byte, 4096)
			for {
				if _, e := conn.Read(b); e != nil {
					return
				}
			}
		}
		authRequests.Add(1)
		body, _ := io.ReadAll(io.LimitReader(r.Body, 65536))
		w.Header().Set("Content-Type", "text/xml")
		if strings.Contains(string(body), "fixture-password") {
			fmt.Fprint(w, `<config-auth><auth id="success"/><session-token>fixture-session</session-token></config-auth>`)
		} else {
			fmt.Fprint(w, `<config-auth><auth id="main"><form method="post" action="/auth?group=fixture"><input type="text" name="username"/><input type="password" name="password"/></form></auth></config-auth>`)
		}
	}))
	defer gateway.Close()
	p := config.Profile{Protocol: "anyconnect", Server: gateway.URL, Username: "fixture", Insecure: true, AuthStateFile: filepath.Join(t.TempDir(), "auth", "state.json")}
	ctx, cancel := context.WithTimeout(context.Background(), 12*time.Second)
	defer cancel()
	updated, e := Bootstrap(ctx, p, BootstrapOptions{Password: "fixture-password"})
	if e != nil {
		t.Fatal("initial bootstrap", e)
	}
	data, e := os.ReadFile(p.AuthStateFile)
	if e != nil {
		t.Fatal(e)
	}
	if strings.Contains(string(data), "fixture-password") {
		t.Fatal("password persisted")
	}
	before := authRequests.Load()
	session, e := NewNativeSession(ctx, updated)
	if e != nil {
		t.Fatal("restart cache", e)
	}
	session.Close()
	if authRequests.Load() != before || connectRequests.Load() != 2 {
		t.Fatal("restart reauthenticated", authRequests.Load(), before, connectRequests.Load())
	}
	assertFreshProcessRestore(t, ctx, updated)
	if authRequests.Load() != before || connectRequests.Load() != 3 {
		t.Fatal("fresh process reauthenticated")
	}
	reject.Store(true)
	if _, e = NewNativeSession(ctx, p); !errors.Is(e, ErrLoginRequired) {
		t.Fatal("expired cookie did not require login", e)
	}
}

func TestExpiredPasswordStatePromptsBeforeAnyPasswordSubmission(t *testing.T) {
	var posts atomic.Int32
	gateway := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == "POST" {
			posts.Add(1)
			http.Error(w, "must not submit an empty password", 500)
			return
		}
		switch r.URL.Path {
		case "/public/manifest":
			fmt.Fprint(w, `{"code":0,"data":{}}`)
		case "/passport/v1/public/authConfig":
			fmt.Fprint(w, `{"code":0,"data":{"isLogin":0}}`)
		default:
			http.Error(w, "unexpected", 500)
		}
	}))
	defer gateway.Close()
	p := config.Profile{Protocol: "atrust", Server: gateway.URL, AuthType: "auth/psw", Username: "fixture", Insecure: true, AuthStateFile: filepath.Join(t.TempDir(), "auth", "state.json")}
	u, _ := url.Parse(gateway.URL)
	saved, _ := json.Marshal(auth.ClientAuthData{DeviceID: "fixture", Cookies: []auth.Cookie{{Host: u.Host, Scheme: "https", Name: "sid", Value: "expired-fixture"}}})
	binding, _ := stateBinding(p)
	store, e := authstate.Open(p.AuthStateFile, binding)
	if e != nil {
		t.Fatal(e)
	}
	store.Save(saved)
	store.Close()
	var prompts atomic.Int32
	stop := errors.New("fixture prompt cancellation")
	_, e = Bootstrap(context.Background(), p, BootstrapOptions{PasswordPrompt: func(context.Context) (string, error) { prompts.Add(1); return "", stop }})
	if !errors.Is(e, stop) || prompts.Load() != 1 || posts.Load() != 0 {
		t.Fatal("stale state submitted credentials before asking", e, prompts.Load(), posts.Load())
	}
}

func TestPasswordPromptCancellationDoesNotKeepProfileConnecting(t *testing.T) {
	started := make(chan struct{})
	exited := make(chan struct{})
	p := config.Profile{Protocol: "anyconnect", Server: "https://vpn.invalid", Username: "fixture"}
	m := NewManager(context.Background(), map[string]config.Profile{"campus": p}, NewNativeSession)
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.Login(ctx, "campus", BootstrapOptions{PasswordPrompt: func(ctx context.Context) (string, error) {
			close(started)
			<-ctx.Done()
			close(exited)
			return "", ctx.Err()
		}})
	}()
	<-started
	cancel()
	select {
	case <-exited:
	case <-time.After(time.Second):
		t.Fatal("password callback ignored login cancellation")
	}
	if e := <-done; !errors.Is(e, context.Canceled) {
		t.Fatal(e)
	}
	deadline := time.Now().Add(time.Second)
	for m.Status()["campus"] == "connecting" && time.Now().Before(deadline) {
		time.Sleep(time.Millisecond)
	}
	if m.Status()["campus"] == "connecting" {
		t.Fatal("canceled prompt left profile connecting")
	}
}

type blockingCloseSession struct {
	testSession
	started, release chan struct{}
}

func (s *blockingCloseSession) Close() error {
	close(s.started)
	<-s.release
	return s.testSession.Close()
}
func TestLogoutSerializesLoginAndEnsureUntilStateCleared(t *testing.T) {
	s := &blockingCloseSession{started: make(chan struct{}), release: make(chan struct{})}
	var count atomic.Int32
	m := NewManager(context.Background(), casProfiles(), func(context.Context, config.Profile) (Session, error) { count.Add(1); return s, nil })
	defer m.Close()
	if e := m.Ensure(context.Background(), "campus"); e != nil {
		t.Fatal(e)
	}
	done := make(chan error, 1)
	go func() { done <- m.Logout(context.Background(), "campus") }()
	<-s.started
	if e := m.Login(context.Background(), "campus", BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "", nil }}); e == nil {
		t.Fatal("login raced logout")
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	if e := m.Ensure(ctx, "campus"); !errors.Is(e, context.DeadlineExceeded) {
		t.Fatal("ensure bypassed logout barrier", e)
	}
	if count.Load() != 1 {
		t.Fatal("second initializer started during logout")
	}
	close(s.release)
	if e := <-done; e != nil {
		t.Fatal(e)
	}
}

func TestAuthenticationStateProcessHelper(t *testing.T) {
	path := os.Getenv("OCH_TEST_RESTORE_PROFILE")
	if path == "" {
		t.Skip("child process fixture")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var profile config.Profile
	if err = json.Unmarshal(data, &profile); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	session, err := NewNativeSession(ctx, profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = session.Close(); err != nil {
		t.Fatal(err)
	}
	fmt.Println("restored-without-credentials")
}

func assertFreshProcessRestore(t *testing.T, ctx context.Context, profile config.Profile) {
	t.Helper()
	profilePath := filepath.Join(t.TempDir(), "child-profile.json")
	encoded, err := json.Marshal(profile)
	if err != nil {
		t.Fatal(err)
	}
	if err = os.WriteFile(profilePath, encoded, 0600); err != nil {
		t.Fatal(err)
	}
	child := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestAuthenticationStateProcessHelper$", "-test.count=1")
	child.Env = []string{"PATH=" + os.Getenv("PATH"), "HOME=" + t.TempDir(), "OCH_TEST_RESTORE_PROFILE=" + profilePath}
	output, err := child.CombinedOutput()
	if err != nil || !strings.Contains(string(output), "restored-without-credentials") {
		t.Fatalf("fresh process restore failed: %v %s", err, output)
	}
}
