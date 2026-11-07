package vpn

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/url"
	"strings"
	"sync"

	"github.com/imyangliu/openconnect-ssh/internal/authstate"
	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
)

var ErrLoginRequired = errors.New("VPN authentication expired or missing; run och login or och setup")

type nativeStateKey struct{}
type nativeState struct {
	store *authstate.Store
	data  []byte
}

func stateBinding(p config.Profile) (authstate.Binding, error) {
	server := p.Server
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	u, e := url.Parse(server)
	if e != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return authstate.Binding{}, errors.New("authentication state requires an HTTPS gateway")
	}
	host := strings.ToLower(u.Hostname())
	port := u.Port()
	if port != "" && port != "443" {
		host = net.JoinHostPort(host, port)
	} else if strings.Contains(host, ":") {
		host = "[" + host + "]"
	}
	gateway := "https://" + host + strings.TrimRight(u.EscapedPath(), "/")
	return authstate.Binding{Gateway: gateway, Protocol: p.Protocol, AuthType: p.AuthType, LoginDomain: p.LoginDomain, Username: p.Username, AuthGroup: p.AuthGroup}, nil
}
func currentState(ctx context.Context) *nativeState {
	s, _ := ctx.Value(nativeStateKey{}).(*nativeState)
	return s
}
func validateATrustState(data []byte, p config.Profile) error {
	if len(data) == 0 {
		return nil
	}
	var saved auth.ClientAuthData
	if json.Unmarshal(data, &saved) != nil || saved.DeviceID == "" {
		return errors.New("invalid saved aTrust authentication state; clear it with logout")
	}
	binding, e := stateBinding(p)
	if e != nil {
		return e
	}
	gateway, _ := url.Parse(binding.Gateway)
	for _, c := range saved.Cookies {
		u, e := url.Parse(c.Scheme + "://" + c.Host)
		if e != nil || c.Scheme != "https" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" || gatewayOrigin(u) != gatewayOrigin(gateway) {
			return errors.New("saved authentication cookie does not belong to configured gateway")
		}
	}
	return nil
}

type storedSession struct {
	Session
	store *authstate.Store
	once  sync.Once
}

func (s *storedSession) Close() error {
	var e error
	s.once.Do(func() {
		e = s.Session.Close()
		if x := s.store.Close(); e == nil {
			e = x
		}
	})
	return e
}
func (s *storedSession) Err() error {
	if h, ok := s.Session.(interface{ Err() error }); ok {
		return h.Err()
	}
	return nil
}
func ClearAuthentication(p config.Profile) error {
	if p.AuthStateFile == "" {
		return nil
	}
	b, e := stateBinding(p)
	if e != nil {
		return e
	}
	return authstate.Clear(p.AuthStateFile, b)
}
