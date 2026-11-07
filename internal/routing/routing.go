package routing

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"

	"github.com/imyangliu/openconnect-ssh/internal/config"
)

type Request struct {
	Host   string `json:"host,omitempty"`
	Port   string `json:"port,omitempty"`
	Via    string `json:"via,omitempty"`
	Policy string `json:"policy,omitempty"`
}
type ipResolver interface {
	LookupNetIP(context.Context, string, string) ([]netip.Addr, error)
}
type Router struct {
	settings config.Config
	sessions Dialer
	resolver ipResolver
}

func ValidateTarget(host, port string) error {
	if host == "" || strings.ContainsAny(host, "\x00\r\n") {
		return errors.New("target host is required and must not contain control characters")
	}
	p, err := strconv.Atoi(port)
	if err != nil || p < 1 || p > 65535 {
		return errors.New("invalid target port")
	}
	return nil
}
func (r *Router) Dial(ctx context.Context, request Request) (net.Conn, error) {
	host := strings.Trim(request.Host, "[]")
	if err := ValidateTarget(host, request.Port); err != nil {
		return nil, err
	}
	if (request.Via == "") == (request.Policy == "") {
		return nil, errors.New("specify exactly one of --via or --policy")
	}
	if request.Via != "" {
		if err := r.settings.ValidateVia(request.Via); err != nil {
			return nil, err
		}
		return r.sessions.Dial(ctx, request.Via, net.JoinHostPort(host, request.Port))
	}
	policy, ok := r.settings.Policies[request.Policy]
	if !ok {
		return nil, fmt.Errorf("unknown policy %q", request.Policy)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		return r.sessions.Dial(ctx, matchIP(policy, ip), net.JoinHostPort(ip.String(), request.Port))
	}
	domain := strings.TrimSuffix(strings.ToLower(host), ".")
	var addresses []netip.Addr
	for _, rule := range policy.Rules {
		exact := strings.TrimSuffix(strings.ToLower(rule.Domain), ".")
		suffix := strings.Trim(strings.ToLower(rule.DomainSuffix), ".")
		if exact != "" && domain == exact || suffix != "" && (domain == suffix || strings.HasSuffix(domain, "."+suffix)) {
			return r.sessions.Dial(ctx, rule.Via, net.JoinHostPort(host, request.Port))
		}
		if rule.CIDR != "" {
			if len(addresses) == 0 {
				var err error
				addresses, err = r.resolver.LookupNetIP(ctx, "ip", host)
				if err != nil {
					return nil, fmt.Errorf("resolve %s: %w", host, err)
				}
				if len(addresses) == 0 {
					return nil, fmt.Errorf("resolve %s: no addresses", host)
				}
			}
			prefix, _ := netip.ParsePrefix(rule.CIDR)
			for _, address := range addresses {
				if prefix.Contains(address.Unmap()) {
					return r.sessions.Dial(ctx, rule.Via, net.JoinHostPort(address.Unmap().String(), request.Port))
				}
			}
		}
	}
	address := host
	if len(addresses) > 0 {
		address = addresses[0].Unmap().String()
	}
	return r.sessions.Dial(ctx, "direct", net.JoinHostPort(address, request.Port))
}
func matchIP(policy config.Policy, ip netip.Addr) string {
	for _, rule := range policy.Rules {
		if rule.CIDR != "" {
			prefix, err := netip.ParsePrefix(rule.CIDR)
			if err == nil && prefix.Contains(ip.Unmap()) {
				return rule.Via
			}
		}
	}
	return "direct"
}

// Dialer selects a direct connection or a named VPN session.
type Dialer interface {
	Dial(context.Context, string, string) (net.Conn, error)
}

func New(c config.Config, sessions Dialer) *Router {
	return &Router{settings: c, sessions: sessions, resolver: net.DefaultResolver}
}
