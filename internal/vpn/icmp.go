package vpn

import (
	"context"
	"crypto/rand"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"net/netip"
	"os"
	"sync"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type echoConn struct {
	session         *packetSession
	local           netip.Addr
	remote          netip.Addr
	id              uint16
	packets         chan []byte
	closed          chan struct{}
	ctx             context.Context
	cancel          context.CancelFunc
	closeOnce       sync.Once
	mu              sync.Mutex
	readDeadline    time.Time
	writeDeadline   time.Time
	deadlineChanged chan struct{}
}

func (s *packetSession) newEchoConn(local, remote netip.Addr) (*echoConn, error) {
	s.echoMu.Lock()
	defer s.echoMu.Unlock()
	if err := s.Err(); err != nil {
		return nil, err
	}
	if len(s.echoes) >= 1024 {
		return nil, errors.New("too many active VPN ping sockets")
	}
	var seed [2]byte
	if _, err := rand.Read(seed[:]); err != nil {
		return nil, err
	}
	id := binary.BigEndian.Uint16(seed[:])
	for s.echoes[id] != nil {
		id++
	}
	ctx, cancel := context.WithCancel(s.life)
	c := &echoConn{session: s, local: local, remote: remote, id: id, packets: make(chan []byte, 16), closed: make(chan struct{}), ctx: ctx, cancel: cancel, deadlineChanged: make(chan struct{})}
	s.echoes[id] = c
	return c, nil
}

func (c *echoConn) LocalAddr() net.Addr  { return &net.IPAddr{IP: net.IP(c.local.AsSlice())} }
func (c *echoConn) RemoteAddr() net.Addr { return &net.IPAddr{IP: net.IP(c.remote.AsSlice())} }
func (c *echoConn) Close() error {
	c.closeOnce.Do(func() {
		c.cancel()
		c.session.echoMu.Lock()
		delete(c.session.echoes, c.id)
		c.session.echoMu.Unlock()
		close(c.closed)
	})
	return nil
}

func (c *echoConn) SetReadDeadline(t time.Time) error {
	c.mu.Lock()
	c.readDeadline = t
	close(c.deadlineChanged)
	c.deadlineChanged = make(chan struct{})
	c.mu.Unlock()
	return nil
}
func (c *echoConn) SetWriteDeadline(t time.Time) error {
	c.mu.Lock()
	c.writeDeadline = t
	c.mu.Unlock()
	return nil
}
func (c *echoConn) SetDeadline(t time.Time) error {
	c.SetWriteDeadline(t)
	return c.SetReadDeadline(t)
}

func (c *echoConn) Read(p []byte) (int, error) {
	for {
		c.mu.Lock()
		deadline, changed := c.readDeadline, c.deadlineChanged
		c.mu.Unlock()
		var timer *time.Timer
		var expired <-chan time.Time
		if !deadline.IsZero() {
			if !time.Now().Before(deadline) {
				return 0, os.ErrDeadlineExceeded
			}
			timer = time.NewTimer(time.Until(deadline))
			expired = timer.C
		}
		select {
		case <-c.closed:
			if timer != nil {
				timer.Stop()
			}
			return 0, net.ErrClosed
		case <-changed:
			if timer != nil {
				timer.Stop()
			}
			continue
		case <-expired:
			return 0, os.ErrDeadlineExceeded
		case packet := <-c.packets:
			if timer != nil {
				timer.Stop()
			}
			if len(p) < len(packet) {
				return 0, io.ErrShortBuffer
			}
			return copy(p, packet), nil
		}
	}
}

func (c *echoConn) Write(p []byte) (int, error) {
	select {
	case <-c.closed:
		return 0, net.ErrClosed
	default:
	}
	c.mu.Lock()
	deadline := c.writeDeadline
	c.mu.Unlock()
	if !deadline.IsZero() && !time.Now().Before(deadline) {
		return 0, os.ErrDeadlineExceeded
	}
	protocol := 1
	var kind icmp.Type = ipv4.ICMPTypeEcho
	if c.remote.Is6() {
		protocol = 58
		kind = ipv6.ICMPTypeEchoRequest
	}
	msg, err := icmp.ParseMessage(protocol, p)
	if err != nil {
		return 0, err
	}
	echo, ok := msg.Body.(*icmp.Echo)
	if !ok || msg.Type != kind || msg.Code != 0 {
		return 0, errors.New("ping socket requires an ICMP Echo request")
	}
	echo.ID = int(c.id)
	var pseudo []byte
	if c.remote.Is6() {
		pseudo = icmp.IPv6PseudoHeader(net.IP(c.local.AsSlice()), net.IP(c.remote.AsSlice()))
	}
	body, err := msg.Marshal(pseudo)
	if err != nil {
		return 0, err
	}
	if len(body) > 1280-40 {
		return 0, errors.New("ICMP probe exceeds the supported packet size")
	}
	var packet []byte
	if c.remote.Is4() {
		packet = make([]byte, 20, 20+len(body))
		packet[0] = 0x45
		binary.BigEndian.PutUint16(packet[2:4], uint16(20+len(body)))
		packet[8], packet[9] = 64, 1
		copy(packet[12:16], c.local.AsSlice())
		copy(packet[16:20], c.remote.AsSlice())
		binary.BigEndian.PutUint16(packet[10:12], internetChecksum(packet))
		packet = append(packet, body...)
	} else {
		packet = make([]byte, 40, 40+len(body))
		packet[0] = 0x60
		binary.BigEndian.PutUint16(packet[4:6], uint16(len(body)))
		packet[6] = 58
		packet[7] = 64
		copy(packet[8:24], c.local.AsSlice())
		copy(packet[24:40], c.remote.AsSlice())
		packet = append(packet, body...)
	}
	ctx := c.ctx
	if !deadline.IsZero() {
		var cancel context.CancelFunc
		ctx, cancel = context.WithDeadline(ctx, deadline)
		defer cancel()
	}
	if err := c.session.sendPacket(ctx, packet); err != nil {
		return 0, err
	}
	return len(p), nil
}

func internetChecksum(b []byte) uint16 {
	var sum uint32
	for len(b) >= 2 {
		sum += uint32(binary.BigEndian.Uint16(b))
		b = b[2:]
	}
	if len(b) == 1 {
		sum += uint32(b[0]) << 8
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func (s *packetSession) dispatchEcho(packet []byte) bool {
	if len(packet) < 20 {
		return false
	}
	var src, dst netip.Addr
	var body []byte
	switch packet[0] >> 4 {
	case 4:
		hlen := int(packet[0]&15) * 4
		total := int(binary.BigEndian.Uint16(packet[2:4]))
		if hlen < 20 || total < hlen+8 || len(packet) < total || packet[9] != 1 || binary.BigEndian.Uint16(packet[6:8])&0x3fff != 0 {
			return false
		}
		if internetChecksum(packet[:hlen]) != 0 {
			return false
		}
		src = netip.AddrFrom4([4]byte(packet[12:16]))
		dst = netip.AddrFrom4([4]byte(packet[16:20]))
		body = packet[hlen:total]
		if body[0] != 0 || body[1] != 0 || internetChecksum(body) != 0 {
			return false
		}
	case 6:
		if len(packet) < 48 || packet[6] != 58 {
			return false
		}
		total := 40 + int(binary.BigEndian.Uint16(packet[4:6]))
		if total < 48 || len(packet) < total {
			return false
		}
		src = netip.AddrFrom16([16]byte(packet[8:24]))
		dst = netip.AddrFrom16([16]byte(packet[24:40]))
		body = packet[40:total]
		if body[0] != 129 || body[1] != 0 {
			return false
		}
		pseudo := icmp.IPv6PseudoHeader(net.IP(src.AsSlice()), net.IP(dst.AsSlice()))
		binary.BigEndian.PutUint32(pseudo[32:36], uint32(len(body)))
		if internetChecksum(append(pseudo, body...)) != 0 {
			return false
		}
	default:
		return false
	}
	id := binary.BigEndian.Uint16(body[4:6])
	s.echoMu.Lock()
	c := s.echoes[id]
	s.echoMu.Unlock()
	if c == nil || c.remote != src || c.local != dst {
		return false
	}
	select {
	case c.packets <- append([]byte(nil), body...):
	default:
	}
	return true
}
