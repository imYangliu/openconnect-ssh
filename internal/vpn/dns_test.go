package vpn

import (
	"context"
	"encoding/binary"
	"fmt"
	"io"
	"net/netip"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"golang.org/x/net/dns/dnsmessage"
)

func TestTunnelDNSRetriesTruncatedUDPOverTCPAndFollowsCNAME(t *testing.T) {
	a, b := packetTestPair(t, false, true)
	address := netip.AddrPortFrom(b.addresses[0], 53)
	udp, err := b.stack.ListenUDPAddrPort(address)
	if err != nil {
		t.Fatal(err)
	}
	defer udp.Close()
	tcp, err := b.stack.ListenTCPAddrPort(address)
	if err != nil {
		t.Fatal(err)
	}
	defer tcp.Close()
	var udpQueries, tcpQueries atomic.Int32
	go func() {
		buf := make([]byte, 1500)
		for {
			n, peer, err := udp.ReadFrom(buf)
			if err != nil {
				return
			}
			var query dnsmessage.Message
			if query.Unpack(buf[:n]) != nil {
				continue
			}
			udpQueries.Add(1)
			response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true, Truncated: true}, Questions: query.Questions}
			wire, _ := response.Pack()
			udp.WriteTo(wire, peer)
		}
	}()
	go func() {
		conn, err := tcp.Accept()
		if err != nil {
			return
		}
		defer conn.Close()
		conn.SetDeadline(time.Now().Add(time.Second))
		var size [2]byte
		if _, err = io.ReadFull(conn, size[:]); err != nil {
			return
		}
		wire := make([]byte, int(binary.BigEndian.Uint16(size[:])))
		if _, err = io.ReadFull(conn, wire); err != nil {
			return
		}
		var query dnsmessage.Message
		if query.Unpack(wire) != nil || len(query.Questions) != 1 {
			return
		}
		tcpQueries.Add(1)
		alias := dnsmessage.MustNewName("canonical.fixture.invalid.")
		response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true}, Questions: query.Questions, Answers: []dnsmessage.Resource{
			{Header: dnsmessage.ResourceHeader{Name: query.Questions[0].Name, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET}, Body: &dnsmessage.CNAMEResource{CNAME: alias}},
			{Header: dnsmessage.ResourceHeader{Name: alias, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: b.addresses[0].As4()}},
		}}
		wire, _ = response.Pack()
		wire = append(binary.BigEndian.AppendUint16(nil, uint16(len(wire))), wire...)
		conn.Write(wire)
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	addresses, err := a.lookupPacketHost(ctx, "alias.fixture.invalid", nil)
	if err != nil || len(addresses) != 1 || addresses[0] != b.addresses[0] || udpQueries.Load() != 1 || tcpQueries.Load() != 1 {
		t.Fatalf("lookup=%v err=%v UDP=%d TCP=%d", addresses, err, udpQueries.Load(), tcpQueries.Load())
	}
}

func TestTunnelDNSCNAMEBudgetAcrossResponses(t *testing.T) {
	for _, aliases := range []int{4, 8, 9} {
		t.Run(strconv.Itoa(aliases), func(t *testing.T) {
			a, b := packetTestPair(t, false, true)
			udp, err := b.stack.ListenUDPAddrPort(netip.AddrPortFrom(b.addresses[0], 53))
			if err != nil {
				t.Fatal(err)
			}
			defer udp.Close()
			var queries atomic.Int32
			go func() {
				buf := make([]byte, 1500)
				for {
					n, peer, err := udp.ReadFrom(buf)
					if err != nil {
						return
					}
					var query dnsmessage.Message
					if query.Unpack(buf[:n]) != nil || len(query.Questions) != 1 {
						continue
					}
					question := query.Questions[0]
					hop, err := strconv.Atoi(strings.TrimPrefix(strings.Split(question.Name.String(), ".")[0], "hop"))
					if err != nil {
						continue
					}
					queries.Add(1)
					response := dnsmessage.Message{Header: dnsmessage.Header{ID: query.ID, Response: true}, Questions: query.Questions}
					if hop < aliases {
						response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeCNAME, Class: dnsmessage.ClassINET}, Body: &dnsmessage.CNAMEResource{CNAME: dnsmessage.MustNewName(fmt.Sprintf("hop%d.fixture.invalid.", hop+1))}}}
					} else {
						response.Answers = []dnsmessage.Resource{{Header: dnsmessage.ResourceHeader{Name: question.Name, Type: dnsmessage.TypeA, Class: dnsmessage.ClassINET}, Body: &dnsmessage.AResource{A: b.addresses[0].As4()}}}
					}
					wire, _ := response.Pack()
					udp.WriteTo(wire, peer)
				}
			}()
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			addresses, err := a.lookupPacketHost(ctx, "hop0.fixture.invalid", nil)
			if aliases <= 8 {
				if err != nil || len(addresses) != 1 || addresses[0] != b.addresses[0] || queries.Load() != int32(aliases+1) {
					t.Fatalf("%d aliases: addresses=%v err=%v queries=%d", aliases, addresses, err, queries.Load())
				}
			} else if err == nil || !strings.Contains(err.Error(), "chain is too long") || queries.Load() != 9 {
				t.Fatalf("unbounded chain: addresses=%v err=%v queries=%d", addresses, err, queries.Load())
			}
		})
	}
}
