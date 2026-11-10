package vpn

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func (s *packetSession) lookupTunnelDNS(ctx context.Context, host string) ([]netip.Addr, error) {
	if len(s.dnsServers) == 0 {
		return nil, errors.New("VPN assigned no DNS server")
	}
	name, err := dnsmessage.NewName(strings.TrimSuffix(host, ".") + ".")
	if err != nil {
		return nil, err
	}
	var addresses []netip.Addr
	var lastError error
	var v4, v6 bool
	for _, local := range s.addresses {
		if local.Unmap().Is4() {
			v4 = true
		} else {
			v6 = true
		}
	}
	for _, kind := range []dnsmessage.Type{dnsmessage.TypeA, dnsmessage.TypeAAAA} {
		if kind == dnsmessage.TypeA && !v4 || kind == dnsmessage.TypeAAAA && !v6 {
			continue
		}
		found, err := s.lookupDNSRecord(ctx, name, kind, 0)
		if err != nil {
			lastError = err
		}
		addresses = append(addresses, found...)
	}
	if ctx.Err() != nil {
		return nil, ctx.Err()
	}
	if len(addresses) > 0 {
		return addresses, nil
	}
	if lastError != nil {
		return nil, lastError
	}
	return nil, &net.DNSError{Err: "no addresses", Name: host, IsNotFound: true}
}

func (s *packetSession) lookupDNSRecord(ctx context.Context, name dnsmessage.Name, kind dnsmessage.Type, depth int) ([]netip.Addr, error) {
	if depth > 8 {
		return nil, errors.New("VPN DNS CNAME chain is too long")
	}
	var seed [2]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	request := dnsmessage.Message{Header: dnsmessage.Header{ID: binary.BigEndian.Uint16(seed[:]), RecursionDesired: true}, Questions: []dnsmessage.Question{{Name: name, Type: kind, Class: dnsmessage.ClassINET}}}
	wire, err := request.Pack()
	if err != nil {
		return nil, err
	}
	var lastError error
	for _, server := range s.dnsServers {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		response, err := s.exchangeDNS(ctx, server, request, wire, "udp")
		if err != nil {
			lastError = err
			continue
		}
		if response.Truncated {
			response, err = s.exchangeDNS(ctx, server, request, wire, "tcp")
			if err != nil {
				lastError = err
				continue
			}
		}
		if response.RCode != dnsmessage.RCodeSuccess {
			lastError = &net.DNSError{Err: "VPN DNS returned " + response.RCode.String(), Name: name.String(), IsNotFound: response.RCode == dnsmessage.RCodeNameError}
			continue
		}
		current := name
		for hops := depth; ; {
			var addresses []netip.Addr
			var alias *dnsmessage.Name
			for _, answer := range response.Answers {
				if answer.Header.Class != dnsmessage.ClassINET || !strings.EqualFold(answer.Header.Name.String(), current.String()) {
					continue
				}
				switch body := answer.Body.(type) {
				case *dnsmessage.AResource:
					if kind == dnsmessage.TypeA {
						addresses = append(addresses, netip.AddrFrom4(body.A))
					}
				case *dnsmessage.AAAAResource:
					if kind == dnsmessage.TypeAAAA {
						addresses = append(addresses, netip.AddrFrom16(body.AAAA))
					}
				case *dnsmessage.CNAMEResource:
					copy := body.CNAME
					alias = &copy
				}
			}
			if len(addresses) > 0 {
				return addresses, nil
			}
			if alias == nil {
				if current != name {
					return s.lookupDNSRecord(ctx, current, kind, hops)
				}
				break
			}
			if hops >= 8 {
				return nil, errors.New("VPN DNS CNAME chain is too long")
			}
			hops++
			current = *alias
		}
		lastError = &net.DNSError{Err: "no matching VPN DNS record", Name: name.String(), IsNotFound: true}
	}
	if lastError == nil {
		lastError = errors.New("VPN DNS query failed")
	}
	return nil, lastError
}

func (s *packetSession) exchangeDNS(ctx context.Context, server netip.Addr, request dnsmessage.Message, wire []byte, network string) (dnsmessage.Message, error) {
	ctx, cancel := context.WithTimeout(ctx, 2*time.Second)
	defer cancel()
	c, err := s.stack.DialContext(ctx, network, net.JoinHostPort(server.String(), "53"))
	if err != nil {
		return dnsmessage.Message{}, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	deadline, _ := ctx.Deadline()
	c.SetDeadline(deadline)
	if network == "tcp" {
		frame := binary.BigEndian.AppendUint16(nil, uint16(len(wire)))
		frame = append(frame, wire...)
		if _, err = io.Copy(c, bytes.NewReader(frame)); err != nil {
			return dnsmessage.Message{}, err
		}
	} else if n, err := c.Write(wire); err != nil {
		return dnsmessage.Message{}, err
	} else if n != len(wire) {
		return dnsmessage.Message{}, io.ErrShortWrite
	}
	buf := make([]byte, 65535)
	for {
		var n int
		if network == "tcp" {
			var length [2]byte
			if _, err = io.ReadFull(c, length[:]); err == nil {
				n = int(binary.BigEndian.Uint16(length[:]))
				_, err = io.ReadFull(c, buf[:n])
			}
		} else {
			n, err = c.Read(buf)
		}
		if err != nil {
			return dnsmessage.Message{}, err
		}
		var parser dnsmessage.Parser
		header, parseErr := parser.Start(buf[:n])
		if parseErr != nil || header.ID != request.ID || !header.Response {
			continue
		}
		var response dnsmessage.Message
		if header.Truncated && network == "udp" {
			return dnsmessage.Message{Header: header}, nil
		}
		if response.Unpack(buf[:n]) != nil || len(response.Questions) != 1 {
			continue
		}
		question := response.Questions[0]
		expected := request.Questions[0]
		if question.Type != expected.Type || question.Class != expected.Class || !strings.EqualFold(question.Name.String(), expected.Name.String()) {
			continue
		}
		return response, nil
	}
}
