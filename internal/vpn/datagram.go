package vpn

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"strconv"
	"strings"
	"time"
)

func (s *packetSession) dialTCP(ctx context.Context, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	if _, err := netip.ParseAddr(host); err == nil {
		return s.stack.DialContext(ctx, "tcp", address)
	}
	addresses, err := s.lookupPacketHost(ctx, host, nil)
	if err != nil {
		return nil, err
	}
	for i, ip := range addresses {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		dialCtx := ctx
		cancel := func() {}
		if deadline, ok := ctx.Deadline(); ok {
			remaining := time.Until(deadline) / time.Duration(len(addresses)-i)
			dialCtx, cancel = context.WithTimeout(ctx, remaining)
		}
		var conn net.Conn
		conn, err = s.stack.DialContext(dialCtx, "tcp", net.JoinHostPort(ip.String(), port))
		cancel()
		if err == nil {
			return conn, nil
		}
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	return nil, err
}

var (
	ErrResourceDenied = errors.New("VPN target is not permitted")
	ErrAddressFamily  = errors.New("VPN does not support the target address family")
)

func (s *packetSession) lookupPacketHost(ctx context.Context, host string, hosts map[string][]net.IP) ([]netip.Addr, error) {
	if ip, err := netip.ParseAddr(host); err == nil {
		return []netip.Addr{ip.Unmap()}, nil
	}
	canonical := strings.ToLower(strings.TrimSuffix(host, "."))
	var addresses []netip.Addr
	if mapped := hosts[canonical]; len(mapped) > 0 {
		for _, ip := range mapped {
			if addr, ok := netip.AddrFromSlice(ip); ok {
				addresses = append(addresses, addr.Unmap())
			}
		}
	} else {
		resolved, err := s.lookupTunnelDNS(ctx, canonical)
		if err != nil {
			return nil, fmt.Errorf("VPN DNS lookup failed: %w", err)
		}
		for _, ip := range resolved {
			addresses = append(addresses, ip.Unmap())
		}
	}
	if len(addresses) == 0 {
		return nil, errors.New("VPN DNS returned no addresses")
	}
	return addresses, nil
}

func (s *packetSession) dialPacket(ctx context.Context, network, address string, hosts map[string][]net.IP, check func(netip.Addr, string, int) error) (net.Conn, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if err := s.Err(); err != nil {
		return nil, err
	}
	host, port, protocol := address, -1, "icmp"
	switch network {
	case "udp":
		var portText string
		var err error
		host, portText, err = net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		port, err = strconv.Atoi(portText)
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid UDP target port")
		}
		protocol = "udp"
	case "ping", "ping4", "ping6":
	default:
		return nil, errors.New("unsupported VPN packet network")
	}
	addresses, err := s.lookupPacketHost(ctx, host, hosts)
	if err != nil {
		return nil, err
	}
	lastError := ErrAddressFamily
	for _, remote := range addresses {
		if remote.Zone() != "" || remote.IsUnspecified() || remote.IsMulticast() {
			continue
		}
		if network == "ping4" && !remote.Is4() || network == "ping6" && !remote.Is6() {
			continue
		}
		var local netip.Addr
		for _, candidate := range s.addresses {
			candidate = candidate.Unmap()
			if candidate.Is4() == remote.Is4() {
				local = candidate
				break
			}
		}
		if !local.IsValid() {
			continue
		}
		if check != nil {
			if err := check(remote, protocol, port); err != nil {
				lastError = err
				continue
			}
		}
		if protocol == "udp" {
			return s.stack.DialContext(ctx, "udp", net.JoinHostPort(remote.String(), strconv.Itoa(port)))
		}
		return s.newEchoConn(local, remote)
	}
	return nil, lastError
}
