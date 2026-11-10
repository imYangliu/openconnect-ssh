package vpn

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"testing"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	icmpv6 "golang.org/x/net/ipv6"
)

func TestNativePingUsesRealRemoteEcho(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		t.Run(fmt.Sprintf("ipv6=%v", ipv6), func(t *testing.T) {
			a, b := packetTestPair(t, ipv6, false)
			m := packetTestManager(t, a)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
			defer cancel()
			for _, seq := range []int{1, 65535} {
				result, err := m.Ping(ctx, "fixture", b.addresses[0].String(), seq)
				if err != nil {
					t.Fatal(err)
				}
				if result.Address != b.addresses[0].String() || result.Sequence != seq || result.Bytes != 32 || result.RTT <= 0 {
					t.Fatalf("unexpected real Echo result: %+v", result)
				}
			}
		})
	}
}

// packetTestImmediateEcho dispatches replies synchronously inside transport Write.
// Returning from Write therefore proves the reply arrived before the first Read.
func packetTestImmediateEcho(t *testing.T, ipv6 bool, reply func([]byte) []byte) *packetSession {
	t.Helper()
	local := netip.MustParseAddr("10.99.0.1")
	if ipv6 {
		local = netip.MustParseAddr("fd99::1")
	}
	var s *packetSession
	read := func(ctx context.Context) ([]byte, error) { <-ctx.Done(); return nil, ctx.Err() }
	write := func(request []byte) error {
		if request[0]>>4 == 4 {
			if len(request) < 20 || int(binary.BigEndian.Uint16(request[2:4])) != len(request) || packetTestChecksum(request[:20]) != 0 || packetTestChecksum(request[20:]) != 0 {
				return fmt.Errorf("invalid IPv4 Echo wire packet: %x", request)
			}
		} else {
			if len(request) < 40 || 40+int(binary.BigEndian.Uint16(request[4:6])) != len(request) {
				return fmt.Errorf("invalid IPv6 Echo wire packet: %x", request)
			}
			pseudo := icmp.IPv6PseudoHeader(net.IP(request[8:24]), net.IP(request[24:40]))
			binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(request)-40))
			if packetTestChecksum(append(pseudo, request[40:]...)) != 0 {
				return fmt.Errorf("invalid IPv6 Echo checksum: %x", request)
			}
		}
		if response := reply(bytes.Clone(request)); response != nil {
			s.dispatchEcho(response)
		}
		return nil
	}
	var err error
	s, err = newPacketSession(context.Background(), []netip.Addr{local}, nil, 1400, read, write, func() error { return nil })
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func packetTestChecksum(p []byte) uint16 {
	var sum uint32
	for i := 0; i < len(p); i += 2 {
		word := uint16(p[i]) << 8
		if i+1 < len(p) {
			word |= uint16(p[i+1])
		}
		sum += uint32(word)
	}
	for sum > 0xffff {
		sum = (sum & 0xffff) + sum>>16
	}
	return ^uint16(sum)
}

func packetTestEchoReply(request []byte, change func(*icmp.Echo)) []byte {
	hlen, protocol := 20, 1
	var kind icmp.Type = ipv4.ICMPTypeEchoReply
	if request[0]>>4 == 6 {
		hlen, protocol, kind = 40, 58, icmpv6.ICMPTypeEchoReply
	}
	message, err := icmp.ParseMessage(protocol, request[hlen:])
	if err != nil {
		panic(err)
	}
	echo := message.Body.(*icmp.Echo)
	if change != nil {
		change(echo)
	}
	response := icmp.Message{Type: kind, Body: echo}
	header := bytes.Clone(request[:hlen])
	var pseudo []byte
	if protocol == 1 {
		copy(header[12:16], request[16:20])
		copy(header[16:20], request[12:16])
	} else {
		copy(header[8:24], request[24:40])
		copy(header[24:40], request[8:24])
		pseudo = icmp.IPv6PseudoHeader(net.IP(header[8:24]), net.IP(header[24:40]))
	}
	body, err := response.Marshal(pseudo)
	if err != nil {
		panic(err)
	}
	if protocol == 1 {
		binary.BigEndian.PutUint16(header[2:4], uint16(hlen+len(body)))
		header[10], header[11] = 0, 0
		binary.BigEndian.PutUint16(header[10:12], packetTestChecksum(header))
	} else {
		binary.BigEndian.PutUint16(header[4:6], uint16(len(body)))
	}
	return append(header, body...)
}

func TestNativeEchoReplyQueuedBeforeRead(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		t.Run(fmt.Sprintf("ipv6=%v", ipv6), func(t *testing.T) {
			s := packetTestImmediateEcho(t, ipv6, func(request []byte) []byte { return packetTestEchoReply(request, nil) })
			network, remote, protocol := "ping4", "10.99.0.2", 1
			var kind icmp.Type = ipv4.ICMPTypeEcho
			var responseKind icmp.Type = ipv4.ICMPTypeEchoReply
			if ipv6 {
				network, remote, protocol, kind, responseKind = "ping6", "fd99::2", 58, icmpv6.ICMPTypeEchoRequest, icmpv6.ICMPTypeEchoReply
			}
			c, err := s.DialContext(context.Background(), network, remote)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			c.SetDeadline(time.Now().Add(time.Second))
			payload := []byte("reply arrives during Write")
			request, err := (&icmp.Message{Type: kind, Body: &icmp.Echo{ID: 1, Seq: 7, Data: payload}}).Marshal(nil)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := c.Write(request); err != nil {
				t.Fatal(err)
			}
			buf := make([]byte, 1500)
			n, err := c.Read(buf)
			if err != nil {
				t.Fatalf("read reply that preceded Read: %v", err)
			}
			message, err := icmp.ParseMessage(protocol, buf[:n])
			if err != nil {
				t.Fatal(err)
			}
			echo, ok := message.Body.(*icmp.Echo)
			if !ok || message.Type != responseKind || echo.Seq != 7 || !bytes.Equal(echo.Data, payload) {
				t.Fatalf("reply changed: %+v", message)
			}
		})
	}
}

func TestNativePingIgnoresUnrelatedOrCorruptReplies(t *testing.T) {
	for _, ipv6 := range []bool{false, true} {
		for _, fault := range []string{"corrupt-checksum", "wrong-id", "wrong-source", "wrong-sequence", "wrong-nonce"} {
			t.Run(fmt.Sprintf("ipv6=%v/%s", ipv6, fault), func(t *testing.T) {
				s := packetTestImmediateEcho(t, ipv6, func(request []byte) []byte {
					response := packetTestEchoReply(request, func(e *icmp.Echo) {
						switch fault {
						case "wrong-id":
							e.ID ^= 1
						case "wrong-sequence":
							e.Seq++
						case "wrong-nonce":
							e.Data[0] ^= 1
						}
					})
					switch fault {
					case "corrupt-checksum":
						response[len(response)-1] ^= 1
					case "wrong-source":
						// Change the request destination before rebuilding the reply, so
						// both IPv4 and IPv6 checksums remain valid for the stray peer.
						if ipv6 {
							request[39] ^= 1
						} else {
							request[19] ^= 1
						}
						response = packetTestEchoReply(request, nil)
					}
					return response
				})
				remote := "10.99.0.2"
				if ipv6 {
					remote = "fd99::2"
				}
				ctx, cancel := context.WithTimeout(context.Background(), 60*time.Millisecond)
				defer cancel()
				if _, err := packetTestManager(t, s).Ping(ctx, "fixture", remote, 7); !errors.Is(err, context.DeadlineExceeded) {
					t.Fatalf("invalid reply completed probe: %v", err)
				}
				if s.Err() != nil {
					t.Fatalf("invalid reply killed healthy session: %v", s.Err())
				}
			})
		}
	}
}

func TestNativePingTimeoutAndCancellation(t *testing.T) {
	for _, mode := range []string{"timeout", "cancel"} {
		t.Run(mode, func(t *testing.T) {
			written := make(chan struct{}, 1)
			s := packetTestImmediateEcho(t, false, func([]byte) []byte { written <- struct{}{}; return nil })
			m := packetTestManager(t, s)
			ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
			defer cancel()
			if mode == "cancel" {
				ctx, cancel = context.WithCancel(context.Background())
				defer cancel()
				go func() {
					select {
					case <-written:
						cancel()
					case <-ctx.Done():
					}
				}()
			}
			result := make(chan error, 1)
			go func() { _, err := m.Ping(ctx, "fixture", "10.99.0.2", 7); result <- err }()
			want := context.DeadlineExceeded
			if mode == "cancel" {
				want = context.Canceled
			}
			select {
			case err := <-result:
				if !errors.Is(err, want) {
					t.Fatalf("probe error = %v, want %v", err, want)
				}
			case <-time.After(time.Second):
				t.Fatal("probe ignored context cancellation")
			}
			s.echoMu.Lock()
			active := len(s.echoes)
			s.echoMu.Unlock()
			if active != 0 {
				t.Fatalf("context ended with %d ping sockets still registered", active)
			}
			if s.Err() != nil {
				t.Fatalf("probe context stopped the shared session: %v", s.Err())
			}
		})
	}
}
