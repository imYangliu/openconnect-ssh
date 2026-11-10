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

type Decision struct {
	Via       string   `json:"via"`
	Address   string   `json:"address"`
	Policy    string   `json:"policy,omitempty"`
	Reason    string   `json:"reason"`
	RuleIndex int      `json:"rule_index,omitempty"`
	Match     string   `json:"match,omitempty"`
	DNS       string   `json:"dns"`
	Resolved  []string `json:"resolved,omitempty"`
}

type DNSError struct{ Err error }

func (e *DNSError) Error() string { return e.Err.Error() }
func (e *DNSError) Unwrap() error { return e.Err }

func (r *Router) Dial(ctx context.Context, request Request) (net.Conn, error) {
	decision, err := r.Resolve(ctx, request)
	if err != nil {
		return nil, err
	}
	return r.sessions.Dial(ctx, decision.Via, decision.Address)
}

func (r *Router) Resolve(ctx context.Context, request Request) (Decision, error) {
	host := strings.Trim(request.Host, "[]")
	if err := ValidateTarget(host, request.Port); err != nil {
		return Decision{}, err
	}
	if (request.Via == "") == (request.Policy == "") {
		return Decision{}, errors.New("specify exactly one of --via or --policy")
	}
	var addresses []netip.Addr
	selected := func(via, address, reason, match string, rule int) Decision {
		dns := "backend"
		addressHost, _, _ := net.SplitHostPort(address)
		if _, err := netip.ParseAddr(addressHost); err == nil {
			dns = "literal"
		} else if via == "direct" {
			dns = "system_at_dial"
		}
		d := Decision{Via: via, Address: address, Policy: request.Policy, Reason: reason, RuleIndex: rule, Match: match, DNS: dns}
		for _, ip := range addresses {
			d.Resolved = append(d.Resolved, ip.Unmap().String())
		}
		if len(addresses) > 0 && (reason == "cidr" || reason == "unmatched") {
			d.DNS = "system"
		}
		return d
	}
	if request.Via != "" {
		if err := r.settings.ValidateVia(request.Via); err != nil {
			return Decision{}, err
		}
		return selected(request.Via, net.JoinHostPort(host, request.Port), "fixed", "", 0), nil
	}
	policy, ok := r.settings.Policies[request.Policy]
	if !ok {
		return Decision{}, fmt.Errorf("unknown policy %q", request.Policy)
	}
	if ip, err := netip.ParseAddr(host); err == nil {
		for i, rule := range policy.Rules {
			if rule.CIDR != "" {
				prefix, err := netip.ParsePrefix(rule.CIDR)
				if err == nil && prefix.Contains(ip.Unmap()) {
					return selected(rule.Via, net.JoinHostPort(ip.String(), request.Port), "cidr", rule.CIDR, i+1), nil
				}
			}
		}
		return selected("direct", net.JoinHostPort(ip.String(), request.Port), "unmatched", "", 0), nil
	}
	domain := strings.TrimSuffix(strings.ToLower(host), ".")
	for i, rule := range policy.Rules {
		exact := strings.TrimSuffix(strings.ToLower(rule.Domain), ".")
		suffix := strings.Trim(strings.ToLower(rule.DomainSuffix), ".")
		if exact != "" && domain == exact {
			return selected(rule.Via, net.JoinHostPort(host, request.Port), "domain", rule.Domain, i+1), nil
		}
		if suffix != "" && (domain == suffix || strings.HasSuffix(domain, "."+suffix)) {
			return selected(rule.Via, net.JoinHostPort(host, request.Port), "domain_suffix", rule.DomainSuffix, i+1), nil
		}
		if rule.CIDR != "" {
			if len(addresses) == 0 {
				var err error
				addresses, err = r.resolver.LookupNetIP(ctx, "ip", host)
				if err != nil {
					return Decision{}, &DNSError{fmt.Errorf("resolve %s: %w", host, err)}
				}
				if len(addresses) == 0 {
					return Decision{}, &DNSError{fmt.Errorf("resolve %s: no addresses", host)}
				}
			}
			prefix, _ := netip.ParsePrefix(rule.CIDR)
			for _, ip := range addresses {
				if prefix.Contains(ip.Unmap()) {
					return selected(rule.Via, net.JoinHostPort(ip.Unmap().String(), request.Port), "cidr", rule.CIDR, i+1), nil
				}
			}
		}
	}
	address := host
	if len(addresses) > 0 {
		address = addresses[0].Unmap().String()
	}
	return selected("direct", net.JoinHostPort(address, request.Port), "unmatched", "", 0), nil
}

// Dialer selects a direct connection or a named VPN session.
type Dialer interface {
	Dial(context.Context, string, string) (net.Conn, error)
}

func New(c config.Config, sessions Dialer) *Router {
	return &Router{settings: c, sessions: sessions, resolver: net.DefaultResolver}
}
