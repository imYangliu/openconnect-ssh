package daemon

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"io"
	"net"
	"strconv"
	"syscall"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

const tcpingProtocolVersion = 1

var errTCPingProtocol = errors.New("daemon does not support this tcping protocol; restart it with the current OCH build")

// TCPingResult reports the requested endpoint and backend DNS+connect elapsed time.
type TCPingResult struct {
	Address  string        `json:"address"`
	Duration time.Duration `json:"duration"`
}

// ValidateTCPingTarget accepts a bare IP or ASCII DNS hostname and a decimal TCP port.
func ValidateTCPingTarget(host, port string) error {
	if err := ValidatePingTarget(host); err != nil {
		return errors.New("tcping target must be an IP address or DNS hostname without a port")
	}
	if len(port) == 0 || len(port) > 5 {
		return errors.New("tcping port must be decimal digits between 1 and 65535")
	}
	for _, digit := range port {
		if digit < '0' || digit > '9' {
			return errors.New("tcping port must be decimal digits between 1 and 65535")
		}
	}
	number, err := strconv.Atoi(port)
	if err != nil || number < 1 || number > 65535 {
		return errors.New("tcping port must be decimal digits between 1 and 65535")
	}
	return nil
}

func validateTCPingRequest(request Request, profiles map[string]string) error {
	if request.Via == "" || request.Via == "direct" || request.Policy != "" {
		return errors.New("tcping requires --via NAME selecting a configured VPN profile")
	}
	if _, ok := profiles[request.Via]; !ok {
		return errors.New("unknown VPN profile for tcping")
	}
	if request.Timeout <= 0 || request.Timeout > 60*time.Second {
		return errors.New("tcping timeout must be greater than zero and at most 60s")
	}
	return ValidateTCPingTarget(request.Host, request.Port)
}

func (d *daemon) handleTCPing(ctx context.Context, conn net.Conn, reader *bufio.Reader, request Request) {
	if err := validateTCPingRequest(request, d.sessions.Status()); err != nil {
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
	if err := d.sessions.Ensure(ctx, request.Via); err != nil {
		if ctx.Err() != nil {
			err = ctx.Err()
		}
		json.NewEncoder(conn).Encode(Reply{Error: safeTCPingError(err)})
		return
	}
	address := net.JoinHostPort(request.Host, request.Port)
	start := time.Now()
	remote, err := d.sessions.Dial(ctx, request.Via, address)
	duration := time.Since(start)
	if remote != nil {
		remote.Close()
	}
	if ctx.Err() != nil {
		err = ctx.Err()
	}
	if err != nil {
		json.NewEncoder(conn).Encode(Reply{Error: safeTCPingError(err)})
		return
	}
	json.NewEncoder(conn).Encode(Reply{TCPingVersion: tcpingProtocolVersion, TCPing: &TCPingResult{Address: address, Duration: duration}})
}

func safeTCPingError(err error) string {
	if errors.Is(err, context.Canceled) {
		return "tcping canceled"
	}
	var networkError net.Error
	if errors.Is(err, context.DeadlineExceeded) || errors.As(err, &networkError) && networkError.Timeout() {
		return "tcping timeout"
	}
	if errors.Is(err, syscall.ECONNREFUSED) {
		return "tcping connection refused"
	}
	category, status := vpn.LoginFailureDetails(err)
	if category != "unknown" {
		return vpn.LoginFailure(category, status).Error()
	}
	switch {
	case errors.Is(err, vpn.ErrResourceDenied):
		return "VPN resource denied tcping to this target"
	case errors.Is(err, vpn.ErrAddressFamily):
		return "VPN backend does not support tcping for this target address family"
	default:
		return "VPN tcping failed; check VPN authentication, target access and service logs"
	}
}

// TCPing opens and closes one TCP connection through a configured VPN profile.
func TCPing(ctx context.Context, c config.Config, via, host, port string, timeout time.Duration) (TCPingResult, error) {
	request := Request{Action: "tcping", Timeout: timeout, Request: routing.Request{Via: via, Host: host, Port: port}}
	profiles := make(map[string]string, len(c.Profiles))
	for name := range c.Profiles {
		profiles[name] = ""
	}
	if err := validateTCPingRequest(request, profiles); err != nil {
		return TCPingResult{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	conn, reply, err := RequestControl(ctx, c, request)
	if conn != nil {
		conn.Close()
	}
	if ctx.Err() != nil {
		return TCPingResult{}, ctx.Err()
	}
	if err != nil {
		var syntaxError *json.SyntaxError
		var typeError *json.UnmarshalTypeError
		if reply.Error == "unknown action" || errors.As(err, &syntaxError) || errors.As(err, &typeError) || errors.Is(err, bufio.ErrBufferFull) || errors.Is(err, io.EOF) {
			return TCPingResult{}, errTCPingProtocol
		}
		return TCPingResult{}, err
	}
	if reply.TCPingVersion != tcpingProtocolVersion || reply.TCPing == nil {
		return TCPingResult{}, errTCPingProtocol
	}
	result := *reply.TCPing
	if result.Address != net.JoinHostPort(host, port) || result.Duration < 0 || result.Duration > timeout {
		return TCPingResult{}, errors.New("daemon returned an invalid tcping result; restart it with the current OCH build")
	}
	return result, nil
}
