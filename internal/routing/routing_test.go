package routing

import (
	"context"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"sync/atomic"
	"testing"

	"github.com/imyangliu/openconnect-ssh/internal/config"
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

type fakeResolver struct {
	addresses []netip.Addr
	calls     int
}

func (r *fakeResolver) LookupNetIP(context.Context, string, string) ([]netip.Addr, error) {
	r.calls++
	return r.addresses, nil
}
func TestRulesOrderedAndNoVPNFailureFallback(t *testing.T) {
	calls := []string{}
	session := &testSession{dial: func(ctx context.Context, n, a string) (net.Conn, error) {
		calls = append(calls, a)
		return nil, errors.New("VPN refused")
	}}
	c := config.Config{Profiles: map[string]config.Profile{"campus": {}}, Policies: map[string]config.Policy{"auto": {Rules: []config.Rule{{CIDR: "10.0.0.0/8", Via: "campus"}, {Domain: "server.example", Via: "direct"}}}}}
	sessions := vpn.NewManager(context.Background(), c.Profiles, func(context.Context, config.Profile) (vpn.Session, error) { return session, nil })
	defer sessions.Close()
	resolver := &fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("10.1.2.3"), netip.MustParseAddr("127.0.0.1")}}
	router := Router{settings: c, sessions: sessions, resolver: resolver}
	_, err := router.Dial(context.Background(), Request{Host: "server.example", Port: "22", Policy: "auto"})
	if err == nil || !strings.Contains(err.Error(), "VPN refused") {
		t.Fatal(err)
	}
	if resolver.calls != 1 || len(calls) != 1 || calls[0] != "10.1.2.3:22" {
		t.Fatal(resolver.calls, calls)
	}
	c.Policies["auto"] = config.Policy{Rules: []config.Rule{{DomainSuffix: "example", Via: "campus"}, {CIDR: "10.0.0.0/8", Via: "direct"}}}
	_, err = router.Dial(context.Background(), Request{Host: "server.example", Port: "22", Policy: "auto"})
	if err == nil || resolver.calls != 1 || calls[1] != "server.example:22" {
		t.Fatal(err, resolver.calls, calls)
	}
}
func TestUnmatchedDirectAndUnknownPolicy(t *testing.T) {
	address := echoTarget(t)
	host, port, _ := net.SplitHostPort(address)
	c := config.Config{Policies: map[string]config.Policy{"auto": {}}}
	sessions := vpn.NewManager(context.Background(), nil, func(context.Context, config.Profile) (vpn.Session, error) {
		t.Error("unmatched attempted VPN")
		return nil, errors.New("unexpected")
	})
	defer sessions.Close()
	r := Router{settings: c, sessions: sessions, resolver: net.DefaultResolver}
	conn, err := r.Dial(context.Background(), Request{Host: host, Port: port, Policy: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if _, err = r.Dial(context.Background(), Request{Host: host, Port: port, Policy: "missing"}); err == nil {
		t.Fatal("unknown policy accepted")
	}
}
func TestDomainRulePreservesNameAfterUnmatchedCIDR(t *testing.T) {
	var target string
	c := config.Config{Profiles: map[string]config.Profile{"campus": {}}, Policies: map[string]config.Policy{"auto": {Rules: []config.Rule{
		{CIDR: "192.168.0.0/16", Via: "direct"},
		{DomainSuffix: "internal.example", Via: "campus"},
	}}}}
	m := vpn.NewManager(context.Background(), c.Profiles, func(context.Context, config.Profile) (vpn.Session, error) {
		return &testSession{dial: func(_ context.Context, _, address string) (net.Conn, error) {
			target = address
			return nil, errors.New("fixture")
		}}, nil
	})
	defer m.Close()
	r := Router{settings: c, sessions: m, resolver: &fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("10.20.0.1")}}}
	r.Dial(context.Background(), Request{Host: "server.internal.example", Port: "22", Policy: "auto"})
	if target != "server.internal.example:22" {
		t.Fatalf("domain rule used system-resolved address: %s", target)
	}
}

type decisionDialer struct{ via, address string }

func (d *decisionDialer) Dial(_ context.Context, via, address string) (net.Conn, error) {
	d.via, d.address = via, address
	return nil, errors.New("fixture dial")
}

func TestResolveSharesExactSelectionWithDial(t *testing.T) {
	for _, tc := range []struct {
		name                                      string
		request                                   Request
		rules                                     []config.Rule
		wantVia, wantAddress, wantReason, wantDNS string
		wantRule, wantLookups                     int
	}{
		{"fixed-vpn", Request{Host: "host.internal", Port: "22", Via: "campus"}, nil, "campus", "host.internal:22", "fixed", "backend", 0, 0},
		{"fixed-direct", Request{Host: "localhost", Port: "22", Via: "direct"}, nil, "direct", "localhost:22", "fixed", "system_at_dial", 0, 0},
		{"cidr-before-domain", Request{Host: "host.internal", Port: "22", Policy: "auto"}, []config.Rule{{CIDR: "10.0.0.0/8", Via: "campus"}, {Domain: "host.internal", Via: "direct"}}, "campus", "10.2.3.4:22", "cidr", "system", 1, 1},
		{"domain-after-unmatched-cidr", Request{Host: "host.internal", Port: "22", Policy: "auto"}, []config.Rule{{CIDR: "192.168.0.0/16", Via: "direct"}, {DomainSuffix: "internal", Via: "campus"}}, "campus", "host.internal:22", "domain_suffix", "backend", 2, 1},
		{"domain-first", Request{Host: "host.internal", Port: "22", Policy: "auto"}, []config.Rule{{Domain: "host.internal", Via: "campus"}, {CIDR: "10.0.0.0/8", Via: "direct"}}, "campus", "host.internal:22", "domain", "backend", 1, 0},
		{"literal-cidr", Request{Host: "10.9.8.7", Port: "22", Policy: "auto"}, []config.Rule{{CIDR: "10.0.0.0/8", Via: "campus"}}, "campus", "10.9.8.7:22", "cidr", "literal", 1, 0},
		{"suffix-boundary", Request{Host: "notinternal", Port: "22", Policy: "auto"}, []config.Rule{{DomainSuffix: "internal", Via: "campus"}}, "direct", "notinternal:22", "unmatched", "system_at_dial", 0, 0},
		{"unmatched-cidr", Request{Host: "host.internal", Port: "22", Policy: "auto"}, []config.Rule{{CIDR: "192.168.0.0/16", Via: "campus"}}, "direct", "203.0.113.1:22", "unmatched", "system", 0, 1},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c := config.Config{Profiles: map[string]config.Profile{"campus": {}}, Policies: map[string]config.Policy{"auto": {Rules: tc.rules}}}
			resolver := &fakeResolver{addresses: []netip.Addr{netip.MustParseAddr("203.0.113.1"), netip.MustParseAddr("10.2.3.4")}}
			dialer := &decisionDialer{}
			r := Router{settings: c, resolver: resolver, sessions: dialer}
			d, err := r.Resolve(context.Background(), tc.request)
			if err != nil {
				t.Fatal(err)
			}
			if d.Via != tc.wantVia || d.Address != tc.wantAddress || d.Reason != tc.wantReason || d.DNS != tc.wantDNS || d.RuleIndex != tc.wantRule || resolver.calls != tc.wantLookups {
				t.Fatalf("unexpected decision %+v; DNS lookups %d", d, resolver.calls)
			}
			resolver.calls = 0
			r.Dial(context.Background(), tc.request)
			if dialer.via != d.Via || dialer.address != d.Address || resolver.calls != tc.wantLookups {
				t.Fatalf("explanation diverged from dial: %+v -> %+v, lookups %d", d, dialer, resolver.calls)
			}
		})
	}
}

func TestResolveDNSFailureIsTyped(t *testing.T) {
	r := Router{settings: config.Config{Policies: map[string]config.Policy{"auto": {Rules: []config.Rule{{CIDR: "10.0.0.0/8", Via: "direct"}}}}}, resolver: &fakeResolver{}}
	_, err := r.Resolve(context.Background(), Request{Host: "fixture.invalid", Port: "22", Policy: "auto"})
	var dns *DNSError
	if !errors.As(err, &dns) {
		t.Fatalf("missing DNS failure category: %v", err)
	}
}
