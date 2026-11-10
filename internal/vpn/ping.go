package vpn

import (
	"bytes"
	"context"
	"crypto/rand"
	"errors"
	"io"
	"net"
	"net/netip"
	"time"

	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
	"golang.org/x/net/ipv6"
)

type PingResult struct {
	Address  string        `json:"address"`
	Sequence int           `json:"sequence"`
	Bytes    int           `json:"bytes"`
	RTT      time.Duration `json:"rtt"`
}

// Ping sends one Echo request using the profile's shared userspace VPN session.
func (m *Manager) Ping(ctx context.Context, via, host string, sequence int) (PingResult, error) {
	result := PingResult{Sequence: sequence}
	if sequence < 1 || sequence > 65535 {
		return result, errors.New("ping sequence must be 1-65535")
	}
	c, err := m.DialNetwork(ctx, via, "ping", host)
	if err != nil {
		return result, err
	}
	defer c.Close()
	stop := context.AfterFunc(ctx, func() { c.Close() })
	defer stop()
	peer, err := netip.ParseAddr(c.RemoteAddr().String())
	if err != nil {
		return result, errors.New("VPN ping returned an invalid peer address")
	}
	result.Address = peer.String()
	if deadline, ok := ctx.Deadline(); ok {
		c.SetDeadline(deadline)
	}
	protocol := 1
	var request icmp.Type = ipv4.ICMPTypeEcho
	var response icmp.Type = ipv4.ICMPTypeEchoReply
	if peer.Is6() {
		protocol = 58
		request = ipv6.ICMPTypeEchoRequest
		response = ipv6.ICMPTypeEchoReply
	}
	nonce := make([]byte, 24)
	if _, err = rand.Read(nonce); err != nil {
		return result, err
	}
	msg := icmp.Message{Type: request, Body: &icmp.Echo{ID: 1, Seq: sequence, Data: nonce}}
	wire, err := msg.Marshal(nil)
	if err != nil {
		return result, err
	}
	start := time.Now()
	n, err := c.Write(wire)
	if err != nil {
		return result, pingContextError(ctx, err)
	}
	if n != len(wire) {
		return result, io.ErrShortWrite
	}
	buf := make([]byte, 1500)
	for {
		n, err = c.Read(buf)
		if err != nil {
			return result, pingContextError(ctx, err)
		}
		msg, err := icmp.ParseMessage(protocol, buf[:n])
		if err != nil || msg.Type != response || msg.Code != 0 {
			continue
		}
		echo, ok := msg.Body.(*icmp.Echo)
		if !ok || echo.Seq != sequence || !bytes.Equal(echo.Data, nonce) {
			continue
		}
		result.Bytes = n
		result.RTT = time.Since(start)
		return result, nil
	}
}

func pingContextError(ctx context.Context, err error) error {
	if ctx.Err() != nil {
		return ctx.Err()
	}
	var timeout net.Error
	if errors.As(err, &timeout) && timeout.Timeout() {
		return context.DeadlineExceeded
	}
	return err
}
