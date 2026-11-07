package vpn

import (
	"context"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
)

func casProfiles() map[string]config.Profile {
	return map[string]config.Profile{"campus": {Protocol: "atrust", AuthType: "auth/cas", LoginDomain: "example.edu", Server: "https://vpn.example.edu"}}
}
func TestLoginSessionSurvivesInteractiveClientAndIsReused(t *testing.T) {
	var calls atomic.Int32
	var sessionContext context.Context
	session := &testSession{dial: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("fixture target") }}
	m := NewManager(context.Background(), casProfiles(), func(ctx context.Context, _ config.Profile) (Session, error) {
		calls.Add(1)
		sessionContext = ctx
		attempt := ctx.Value(casLoginKey{}).(*casLoginAttempt)
		_, err := attempt.callback(attempt.ctx, "https://vpn.example.edu/passport/v1/public/casLogin?sfDomain=example.edu")
		return session, err
	})
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	err := m.Login(ctx, "campus", BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "callback", nil }})
	cancel()
	if err != nil || sessionContext.Err() != nil || m.Status()["campus"] != "ready" {
		t.Fatal(err, m.Status())
	}
	if err = m.Login(context.Background(), "campus", BootstrapOptions{Browser: func(context.Context, string) (string, error) {
		t.Error("healthy session relaunched browser")
		return "", errors.New("unexpected")
	}}); err != nil {
		t.Fatal(err)
	}
	m.Dial(context.Background(), "campus", "target:22")
	if calls.Load() != 1 || session.closed.Load() != 0 {
		t.Fatal(calls.Load(), session.closed.Load())
	}
}
func TestExplicitLoginRetriesFailedAuthenticationWithoutCooldown(t *testing.T) {
	var calls atomic.Int32
	m := NewManager(context.Background(), casProfiles(), func(ctx context.Context, _ config.Profile) (Session, error) {
		calls.Add(1)
		if _, ok := ctx.Value(casLoginKey{}).(*casLoginAttempt); !ok {
			return nil, errors.New("interactive authentication required")
		}
		return &testSession{dial: func(context.Context, string, string) (net.Conn, error) { return nil, errors.New("fixture") }}, nil
	})
	defer m.Close()
	m.Dial(context.Background(), "campus", "target:22")
	if m.Status()["campus"] != "failed" {
		t.Fatal(m.Status())
	}
	if err := m.Login(context.Background(), "campus", BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "", nil }}); err != nil {
		t.Fatal(err)
	}
	if calls.Load() != 2 || m.Status()["campus"] != "ready" {
		t.Fatal(calls.Load(), m.Status())
	}
}
func TestLoginCancellationReleasesAttemptWithoutParallelInitialization(t *testing.T) {
	started := make(chan struct{})
	var calls atomic.Int32
	m := NewManager(context.Background(), casProfiles(), func(ctx context.Context, _ config.Profile) (Session, error) {
		calls.Add(1)
		attempt := ctx.Value(casLoginKey{}).(*casLoginAttempt)
		close(started)
		<-attempt.ctx.Done()
		return nil, attempt.ctx.Err()
	})
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	callback := func(context.Context, string) (string, error) { return "", nil }
	go func() { done <- m.Login(ctx, "campus", BootstrapOptions{Browser: callback}) }()
	<-started
	if err := m.Login(context.Background(), "campus", BootstrapOptions{Browser: callback}); err == nil || !strings.Contains(err.Error(), "already connecting") {
		t.Fatal(err)
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("login ignored cancellation")
	}
	until := time.Now().Add(time.Second)
	for m.Status()["campus"] == "connecting" && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if calls.Load() != 1 || m.Status()["campus"] != "failed" {
		t.Fatal(calls.Load(), m.Status())
	}
}
func TestCASURLsMatchGatewayDomainAndDoNotLeakTicket(t *testing.T) {
	gateway, _ := url.Parse("https://vpn.example.edu")
	login, err := resolveCASLoginURL(gateway, "/passport/v1/public/casLogin?sfDomain=example.edu", "example.edu")
	if err != nil || !strings.HasPrefix(login, "https://vpn.example.edu/") {
		t.Fatal(login, err)
	}
	valid := "https://VPN.EXAMPLE.EDU:443/passport/v1/auth/cas?sfDomain=example.edu&ticket=private-fixture"
	if err = validateCASCallback(gateway, valid, "example.edu"); err != nil {
		t.Fatal(err)
	}
	for _, bad := range []string{
		strings.Replace(valid, "https:", "http:", 1),
		strings.Replace(valid, "VPN.EXAMPLE.EDU:443", "evil.example", 1),
		strings.Replace(valid, "VPN.EXAMPLE.EDU:443", "user:pass@VPN.EXAMPLE.EDU:443", 1),
		strings.Replace(valid, "/passport/v1/auth/cas", "/other", 1),
		valid + "#fragment", valid + "&ticket=another", valid + "&sfDomain=example.edu",
		strings.Replace(valid, "sfDomain=example.edu", "sfDomain=another", 1),
		strings.Replace(valid, "ticket=private-fixture", "ticket=", 1),
		valid + "&broken=%zz",
	} {
		err = validateCASCallback(gateway, bad, "example.edu")
		if err == nil || strings.Contains(err.Error(), "private-fixture") {
			t.Fatalf("unsafe callback error: %v", err)
		}
	}
	if _, err = resolveCASLoginURL(gateway, "https://evil.example/passport/v1/public/casLogin?sfDomain=example.edu", "example.edu"); err == nil {
		t.Fatal("accepted foreign login link")
	}
}

func TestCanceledPostCallbackSetupCannotInstallSession(t *testing.T) {
	afterCallback := make(chan struct{})
	release := make(chan struct{})
	session := &testSession{}
	m := NewManager(context.Background(), casProfiles(), func(ctx context.Context, _ config.Profile) (Session, error) {
		attempt := ctx.Value(casLoginKey{}).(*casLoginAttempt)
		if _, err := attempt.callback(attempt.ctx, "https://vpn.example.edu/passport/v1/public/casLogin?sfDomain=example.edu"); err != nil {
			return nil, err
		}
		close(afterCallback)
		<-release
		return session, nil
	})
	defer m.Close()
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		done <- m.Login(ctx, "campus", BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "callback", nil }})
	}()
	<-afterCallback
	cancel()
	if err := <-done; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	close(release)
	until := time.Now().Add(time.Second)
	for m.Status()["campus"] == "connecting" && time.Now().Before(until) {
		time.Sleep(time.Millisecond)
	}
	if m.Status()["campus"] != "failed" || session.closed.Load() != 1 {
		t.Fatal(m.Status(), session.closed.Load())
	}
}
func TestLoginFactoryErrorIsSanitizedBeforeCache(t *testing.T) {
	m := NewManager(context.Background(), casProfiles(), func(context.Context, config.Profile) (Session, error) {
		return nil, errors.New("GET https://vpn.example.edu/cas?ticket=private-fixture")
	})
	defer m.Close()
	err := m.Login(context.Background(), "campus", BootstrapOptions{Browser: func(context.Context, string) (string, error) { return "", nil }})
	if err == nil || strings.Contains(err.Error(), "private-fixture") {
		t.Fatal(err)
	}
	_, err = m.Dial(context.Background(), "campus", "target:22")
	if err == nil || strings.Contains(err.Error(), "private-fixture") {
		t.Fatal("cached callback leaked", err)
	}
}

func TestManagerCloseCancelsInteractiveHandler(t *testing.T) {
	started := make(chan struct{})
	m := NewManager(context.Background(), casProfiles(), func(ctx context.Context, _ config.Profile) (Session, error) {
		attempt := ctx.Value(casLoginKey{}).(*casLoginAttempt)
		close(started)
		_, err := attempt.callback(attempt.ctx, "https://vpn.example.edu/passport/v1/public/casLogin?sfDomain=example.edu")
		return nil, err
	})
	done := make(chan error, 1)
	go func() {
		done <- m.Login(context.Background(), "campus", BootstrapOptions{Browser: func(ctx context.Context, _ string) (string, error) { <-ctx.Done(); return "", ctx.Err() }})
	}()
	<-started
	m.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("service close did not cancel browser handler")
	}
}
