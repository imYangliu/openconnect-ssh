package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

const pingProtocolVersion = 1

var errPingProtocol = errors.New("daemon does not support this ping protocol; restart it with the current OCH build")

// ValidatePingTarget accepts an IP literal or an ASCII DNS hostname without a port.
func ValidatePingTarget(host string) error {
	if address, err := netip.ParseAddr(host); err == nil && address.Zone() == "" {
		return nil
	}
	if strings.Contains(host, ".") && strings.Trim(host, "0123456789.") == "" {
		return errors.New("ping target contains an invalid IPv4 address")
	}
	name := strings.TrimSuffix(host, ".")
	if len(name) == 0 || len(name) > 253 {
		return errors.New("ping target must be an IP address or DNS hostname without a port")
	}
	for _, label := range strings.Split(name, ".") {
		if len(label) == 0 || len(label) > 63 || label[0] == '-' || label[len(label)-1] == '-' {
			return errors.New("ping target must be an IP address or DNS hostname without a port")
		}
		for _, c := range label {
			if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '-') {
				return errors.New("ping target must be an IP address or DNS hostname without a port")
			}
		}
	}
	return nil
}

func validatePingRequest(request Request, profiles map[string]string) error {
	if request.Via == "" || request.Via == "direct" || request.Policy != "" || request.Port != "" {
		return errors.New("ping requires --via NAME selecting a configured VPN profile")
	}
	if _, ok := profiles[request.Via]; !ok {
		return errors.New("unknown VPN profile for ping")
	}
	if request.Timeout <= 0 || request.Timeout > 60*time.Second {
		return errors.New("ping timeout must be greater than zero and at most 60s")
	}
	if request.PingSequence < 1 || request.PingSequence > 65535 {
		return errors.New("ping sequence must be between 1 and 65535")
	}
	return ValidatePingTarget(request.Host)
}

func (d *daemon) handlePing(ctx context.Context, conn net.Conn, reader *bufio.Reader, request Request) {
	if err := validatePingRequest(request, d.sessions.Status()); err != nil {
		json.NewEncoder(conn).Encode(Reply{Error: err.Error()})
		return
	}
	ctx, cancel := context.WithTimeout(ctx, request.Timeout)
	defer cancel()
	defer conn.Close()
	go func() {
		io.Copy(io.Discard, reader)
		cancel()
	}()
	result, err := d.sessions.Ping(ctx, request.Via, request.Host, request.PingSequence)
	if err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		json.NewEncoder(conn).Encode(Reply{Error: safePingError(err)})
		return
	}
	json.NewEncoder(conn).Encode(Reply{PingVersion: pingProtocolVersion, Ping: &result})
}

func safePingError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "ping canceled"
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
		return "ping timeout"
	}
	category, status := vpn.LoginFailureDetails(err)
	if category != "unknown" {
		return vpn.LoginFailure(category, status).Error()
	}
	switch {
	case errors.Is(err, vpn.ErrResourceDenied):
		return "VPN resource denied ping to this target"
	case errors.Is(err, vpn.ErrAddressFamily):
		return "VPN backend does not support ping for this target address family"
	default:
		return "VPN ping failed; check VPN authentication, target access and service logs"
	}
}

// Ping asks the shared daemon to send one echo request through a VPN profile.
func Ping(ctx context.Context, c config.Config, via, host string, sequence int, timeout time.Duration) (vpn.PingResult, error) {
	request := Request{Action: "ping", PingSequence: sequence, Timeout: timeout, Request: routing.Request{Via: via, Host: host}}
	profiles := make(map[string]string, len(c.Profiles))
	for name := range c.Profiles {
		profiles[name] = ""
	}
	if err := validatePingRequest(request, profiles); err != nil {
		return vpn.PingResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, reply, err := RequestControl(ctx, c, request)
	if conn != nil {
		conn.Close()
	}
	if err != nil {
		if ctx.Err() != nil {
			return vpn.PingResult{}, ctx.Err()
		}
		if reply.Error == "unknown action" {
			return vpn.PingResult{}, errPingProtocol
		}
		return vpn.PingResult{}, err
	}
	if reply.PingVersion != pingProtocolVersion || reply.Ping == nil {
		return vpn.PingResult{}, errPingProtocol
	}
	result := *reply.Ping
	address, err := netip.ParseAddr(result.Address)
	if err != nil || address.Zone() != "" || result.Sequence != sequence || result.Bytes <= 0 || result.RTT < 0 || result.RTT > timeout {
		return vpn.PingResult{}, errors.New("daemon returned an invalid ping result; restart it with the current OCH build")
	}
	return result, nil
}
