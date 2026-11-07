package vpn

import (
	"context"
	"errors"
	"net/url"
	"strings"
	"time"
)

// CASCallback runs the interactive browser step outside the VPN service.
type CASCallback func(context.Context, string) (string, error)
type casLoginKey struct{}
type casLoginAttempt struct {
	ctx      context.Context
	callback CASCallback
}

// Login installs the authenticated session in the same pool used by proxy clients.
func (m *Manager) Login(ctx context.Context, via string, options BootstrapOptions) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	profile, ok := m.profiles[via]
	if !ok {
		return errors.New("unknown VPN profile")
	}
	if profile.Protocol != "atrust" && profile.Protocol != "anyconnect" {
		return errors.New("unsupported login protocol")
	}
	if profile.Protocol == "atrust" && profile.AuthType == "auth/cas" && options.Browser == nil {
		return errors.New("CAS browser handler is required")
	}
	attemptCtx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	defer cancel()
	stopManager := context.AfterFunc(m.ctx, cancel)
	defer stopManager()
	m.mu.Lock()
	if m.closed {
		m.mu.Unlock()
		return errors.New("session service is stopping")
	}
	previous := m.entries[via]
	if previous != nil {
		select {
		case <-previous.ready:
			if previous.err == nil {
				health, hasHealth := previous.session.(interface{ Err() error })
				if !hasHealth || health.Err() == nil {
					m.mu.Unlock()
					return nil
				}
			}
		default:
			m.mu.Unlock()
			return errors.New("VPN is already connecting; retry login after that attempt completes")
		}
	}
	entry := &sessionEntry{ready: make(chan struct{})}
	m.entries[via] = entry
	factoryCtx := context.WithValue(m.ctx, bootstrapKey{}, &options)
	factoryCtx = context.WithValue(factoryCtx, casLoginKey{}, &casLoginAttempt{ctx: attemptCtx, callback: options.Browser})
	m.mu.Unlock()
	if previous != nil && previous.session != nil {
		previous.session.Close()
	}
	go m.initialize(factoryCtx, profile, entry)
	select {
	case <-attemptCtx.Done():
		return attemptCtx.Err()
	case <-entry.ready:
		if err := attemptCtx.Err(); err != nil {
			return err
		}
		if entry.err == nil {
			if health, ok := entry.session.(interface{ Err() error }); ok && health.Err() != nil {
				m.invalidate(via, entry, errors.New("authenticated VPN session became unavailable"))
				return errors.New("authenticated VPN session became unavailable")
			}
		}
		return entry.err
	}
}

func gatewayOrigin(u *url.URL) string {
	port := u.Port()
	if port == "" {
		port = "443"
	}
	return strings.ToLower(u.Hostname()) + ":" + port
}
func validCASURL(u, gateway *url.URL) bool {
	return u.Scheme == "https" && u.User == nil && u.Fragment == "" && gatewayOrigin(u) == gatewayOrigin(gateway)
}
func resolveCASLoginURL(gateway *url.URL, loginURL, domain string) (string, error) {
	u, err := url.Parse(loginURL)
	if err != nil {
		return "", errors.New("invalid CAS login URL")
	}
	u = gateway.ResolveReference(u)
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || !validCASURL(u, gateway) || u.Path != "/passport/v1/public/casLogin" || u.RawPath != "" || len(q["sfDomain"]) != 1 || q.Get("sfDomain") != domain || domain == "" {
		return "", errors.New("CAS login URL does not match the configured gateway and domain")
	}
	return u.String(), nil
}
func validateCASCallback(gateway *url.URL, callback, domain string) error {
	u, err := url.Parse(callback)
	if err != nil {
		return errors.New("invalid CAS callback")
	}
	q, err := url.ParseQuery(u.RawQuery)
	if err != nil || !validCASURL(u, gateway) || u.Path != "/passport/v1/auth/cas" || u.RawPath != "" || len(q["sfDomain"]) != 1 || q.Get("sfDomain") != domain || domain == "" || len(q["ticket"]) != 1 || strings.TrimSpace(q.Get("ticket")) == "" {
		return errors.New("CAS callback does not match the configured gateway and domain")
	}
	return nil
}
