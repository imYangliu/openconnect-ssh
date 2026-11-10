package vpn

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"net/url"
	"os"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/authstate"
	"github.com/imyangliu/openconnect-ssh/internal/config"
	zclient "github.com/mythologyli/zju-connect/client"
	"github.com/mythologyli/zju-connect/client/atrust"
	"github.com/mythologyli/zju-connect/client/atrust/auth"
	"github.com/mythologyli/zju-connect/client/authchallenge"
	"github.com/mythologyli/zju-connect/resolve"
	oc "github.com/sagernet/sing-openconnect"
	"golang.zx2c4.com/wireguard/tun"
	"golang.zx2c4.com/wireguard/tun/netstack"
)

func NewNativeSession(ctx context.Context, p config.Profile) (Session, error) {
	var state *nativeState
	if p.AuthStateFile != "" {
		binding, err := stateBinding(p)
		if err != nil {
			return nil, err
		}
		store, err := authstate.Open(p.AuthStateFile, binding)
		if err != nil {
			return nil, err
		}
		data, err := store.Load()
		if err != nil {
			store.Close()
			return nil, err
		}
		state = &nativeState{store: store, data: data}
		ctx = context.WithValue(ctx, nativeStateKey{}, state)
	}
	password := os.Getenv(p.PasswordEnv)
	if options, ok := ctx.Value(bootstrapKey{}).(*BootstrapOptions); ok && options.Password != "" {
		password = options.Password
	}
	options, _ := ctx.Value(bootstrapKey{}).(*BootstrapOptions)
	prompt := func() error {
		if options == nil || options.PasswordPrompt == nil {
			return ErrLoginRequired
		}
		promptCtx := ctx
		if attempt, ok := ctx.Value(casLoginKey{}).(*casLoginAttempt); ok {
			promptCtx = attempt.ctx
		}
		value, e := options.PasswordPrompt(promptCtx)
		if e != nil {
			return e
		}
		if value == "" {
			return ErrLoginRequired
		}
		password = value
		return nil
	}
	if password == "" && (state == nil || len(state.data) == 0) && !(p.Protocol == "atrust" && p.AuthType == "auth/cas") && os.Getenv(p.CookieEnv) == "" {
		if e := prompt(); e != nil {
			if state != nil {
				state.store.Close()
			}
			return nil, e
		}
	}
	var session Session
	var err error
	create := func() (Session, error) {
		switch p.Protocol {
		case "anyconnect":
			return newAnyConnectSession(ctx, p, password)
		case "atrust":
			return newATrustSession(ctx, p, password)
		default:
			return nil, errors.New("unsupported native VPN protocol")
		}
	}
	session, err = create()
	if errors.Is(err, ErrLoginRequired) {
		if state != nil {
			if e := state.store.Clear(); e != nil {
				state.store.Close()
				return nil, e
			}
			state.data = nil
		}
		if password == "" && options != nil && options.PasswordPrompt != nil && !(p.Protocol == "atrust" && p.AuthType == "auth/cas") {
			if e := prompt(); e != nil {
				err = e
			} else {
				session, err = create()
			}
		}
	}

	if err != nil {
		if state != nil {
			state.store.Close()
		}
		return nil, err
	}
	if state != nil {
		return &storedSession{Session: session, store: state.store}, nil
	}
	return session, nil
}

type packetSession struct {
	device         tun.Device
	stack          *netstack.Net
	cancel         context.CancelFunc
	closeTransport func() error
	once           sync.Once
	errMu          sync.Mutex
	terminal       error
	addresses      []netip.Addr
	dnsServers     []netip.Addr
	writePacket    func([]byte) error
	life           context.Context
	writes         chan packetWrite
	echoMu         sync.Mutex
	echoes         map[uint16]*echoConn
}

func newPacketSession(ctx context.Context, addresses, dns []netip.Addr, mtu int, read func(context.Context) ([]byte, error), write func([]byte) error, closeTransport func() error) (*packetSession, error) {
	if len(addresses) == 0 {
		return nil, errors.New("VPN assigned no tunnel address")
	}
	device, stack, err := netstack.CreateNetTUN(addresses, dns, mtu)
	if err != nil {
		return nil, err
	}
	life, cancel := context.WithCancel(ctx)
	s := &packetSession{device: device, stack: stack, cancel: cancel, closeTransport: closeTransport,
		addresses: append([]netip.Addr(nil), addresses...), dnsServers: append([]netip.Addr(nil), dns...), writePacket: write, life: life,
		writes: make(chan packetWrite, 64), echoes: make(map[uint16]*echoConn)}
	fail := s.invalidate
	go s.writeLoop()
	go func() {
		for {
			b, err := read(life)
			if err != nil {
				fail(err)
				return
			}
			if s.dispatchEcho(b) {
				continue
			}
			if _, err = device.Write([][]byte{b}, 0); err != nil {
				fail(err)
				return
			}
		}
	}()
	go func() {
		b := make([]byte, 65535)
		sizes := make([]int, 1)
		for {
			_, err := device.Read([][]byte{b}, sizes, 0)
			if err != nil {
				fail(err)
				return
			}
			// AnyConnect can reconnect while TCP retransmits a dropped packet.
			if err = s.sendPacket(life, b[:sizes[0]]); err != nil && !errors.Is(err, oc.ErrDataChannelNotReady) {
				fail(err)
				return
			}
		}
	}()
	go func() { <-life.Done(); s.Close() }()
	return s, nil
}

func (s *packetSession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := s.Err(); err != nil {
		return nil, fmt.Errorf("VPN packet transport stopped: %w", err)
	}
	if network == "tcp" {
		return s.dialTCP(ctx, address)
	}
	return s.dialPacket(ctx, network, address, nil, nil)
}
func (s *packetSession) Err() error {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	return s.terminal
}
func (s *packetSession) Done() <-chan struct{} { return s.life.Done() }
func (s *packetSession) setError(err error) {
	s.errMu.Lock()
	defer s.errMu.Unlock()
	if s.terminal == nil {
		s.terminal = err
	}
}
func (s *packetSession) invalidate(err error) {
	if err == nil {
		err = net.ErrClosed
	}
	s.setError(err)
	// Protocol callbacks can hold lifecycle locks needed by transport Close.
	go s.Close()
}
func (s *packetSession) Close() error {
	s.setError(net.ErrClosed)
	s.once.Do(func() {
		s.cancel()
		s.echoMu.Lock()
		var echoes []*echoConn
		for _, c := range s.echoes {
			echoes = append(echoes, c)
		}
		s.echoMu.Unlock()
		for _, c := range echoes {
			c.Close()
		}
		_ = s.closeTransport()
		_ = s.device.Close()
	})
	return nil
}

type packetConfigurationGuard struct {
	mu      sync.Mutex
	session *packetSession
	key     string
}

func packetConfigurationKey(addresses []netip.Prefix, dns []netip.Addr, mtu uint32) string {
	var addrKeys, dnsKeys []string
	for _, a := range addresses {
		addrKeys = append(addrKeys, a.String())
	}
	for _, d := range dns {
		dnsKeys = append(dnsKeys, d.String())
	}
	slices.Sort(addrKeys)
	return fmt.Sprintf("%v|%v|%d", addrKeys, dnsKeys, mtu)
}
func (g *packetConfigurationGuard) bind(s *packetSession, key string) {
	g.mu.Lock()
	g.session, g.key = s, key
	g.mu.Unlock()
}
func (g *packetConfigurationGuard) observe(key string) {
	g.mu.Lock()
	s, expected := g.session, g.key
	g.mu.Unlock()
	if s != nil && key != expected {
		s.invalidate(errors.New("VPN tunnel address, DNS or MTU changed; recreate session"))
	}
}

func newAnyConnectSession(ctx context.Context, p config.Profile, password string) (Session, error) {
	life, cancel := context.WithCancel(ctx)
	cookie := os.Getenv(p.CookieEnv)
	state := currentState(ctx)
	if cookie == "" && state != nil && len(state.data) > 0 {
		var saved struct {
			Cookie string `json:"cookie"`
		}
		if json.Unmarshal(state.data, &saved) != nil || saved.Cookie == "" || strings.ContainsAny(saved.Cookie, "\r\n") {
			cancel()
			return nil, errors.New("invalid saved AnyConnect authentication state; clear it with logout")
		}
		cookie = saved.Cookie
	}
	if cookie == "" && password == "" {
		cancel()
		return nil, ErrLoginRequired
	}
	saveCookie := func(endpoint, cookie string) error {
		if state == nil {
			return nil
		}
		original, e := stateBinding(p)
		if e != nil {
			return e
		}
		expected, _ := url.Parse(original.Gateway)
		actual, e := url.Parse(endpoint)
		if e != nil || actual.Scheme != "https" || actual.User != nil || actual.Fragment != "" || gatewayOrigin(actual) != gatewayOrigin(expected) {
			return errors.New("authenticated gateway changed; refusing to persist cookie")
		}

		data, _ := json.Marshal(struct {
			Cookie string `json:"cookie"`
		}{cookie})
		return state.store.Save(data)
	}

	if p.CookieEnv != "" && cookie == "" {
		cancel()
		return nil, fmt.Errorf("cookie environment variable %s is empty", p.CookieEnv)
	}
	var guard packetConfigurationGuard
	var c *oc.Client
	c, err := oc.NewClient(oc.ClientOptions{Context: life, Server: p.Server, Flavor: oc.FlavorAnyConnect, Username: p.Username, Password: password, AuthGroup: p.AuthGroup, Cookie: cookie, PasswordAuthenticationDisabled: password == "", OnAuthenticatedCookie: saveCookie, TLSConfig: oc.ClientTLSOptions{Config: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: p.Insecure}}, OnTunnelConfiguration: func(oc.TunnelConfigurationEvent) error {
		conf := c.TunnelConfiguration()
		guard.observe(packetConfigurationKey(conf.Addresses, conf.DNS, conf.MTU))
		return nil
	}})
	if err != nil {
		cancel()
		return nil, err
	}
	closeClient := func() error { cancel(); return c.Close() }
	if err = c.Start(); err != nil {
		closeClient()
		return nil, err
	}
	readyBase := ctx
	if attempt, ok := ctx.Value(casLoginKey{}).(*casLoginAttempt); ok {
		readyBase = attempt.ctx
	}
	ready, stop := context.WithTimeout(readyBase, 60*time.Second)
	defer stop()
	if err = c.WaitReady(ready); err != nil {
		closeClient()
		if errors.Is(err, oc.ErrAuthenticationFailed) || errors.Is(err, oc.ErrSessionRejected) {
			return nil, ErrLoginRequired
		}
		return nil, fmt.Errorf("AnyConnect authentication/tunnel setup: %w", err)
	}
	conf := c.TunnelConfiguration()
	addresses := make([]netip.Addr, 0, len(conf.Addresses))
	for _, p := range conf.Addresses {
		addresses = append(addresses, p.Addr())
	}
	s, err := newPacketSession(life, addresses, conf.DNS, int(conf.MTU), c.ReadDataPacket, c.WriteDataPacket, closeClient)
	if err != nil {
		closeClient()
		return nil, err
	}
	guard.bind(s, packetConfigurationKey(conf.Addresses, conf.DNS, conf.MTU))
	current := c.TunnelConfiguration()
	guard.observe(packetConfigurationKey(current.Addresses, current.DNS, current.MTU))
	return s, nil
}

type nativeUnderlay struct{ net.Dialer }

func (*nativeUnderlay) ExcludeIP(net.IP) {}

type aTrustSession struct {
	client  *atrust.Client
	packets *packetSession
	domains zclient.DomainResources
	dns     map[string][]net.IP
}

func newATrustSession(ctx context.Context, p config.Profile, password string) (Session, error) {
	server := p.Server
	if !strings.Contains(server, "://") {
		server = "https://" + server
	}
	u, err := url.Parse(server)
	if err != nil || u.Scheme != "https" || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" || (u.Path != "" && u.Path != "/") {
		return nil, errors.New("aTrust server must be an HTTPS origin")
	}
	port := 443
	if u.Port() != "" {
		port, err = strconv.Atoi(u.Port())
		if err != nil || port < 1 || port > 65535 {
			return nil, errors.New("invalid aTrust server port")
		}
	}
	state := currentState(ctx)
	var cached []byte
	if state != nil {
		cached = state.data
		if err := validateATrustState(cached, p); err != nil {
			return nil, err
		}
	}
	if len(cached) == 0 && p.AuthType != "auth/cas" && password == "" {
		return nil, ErrLoginRequired
	}
	if _, interactive := ctx.Value(casLoginKey{}).(*casLoginAttempt); p.AuthType == "auth/cas" && len(cached) == 0 && !interactive {
		return nil, ErrLoginRequired
	}
	method, err := auth.NewLoginMethod(auth.LoginMethodOptions{AuthType: p.AuthType, Username: p.Username, Password: password, Domain: p.LoginDomain})
	if err != nil {
		return nil, err
	}
	certificates, err := loadTunnelCertificates(p.TunnelCertificateFile)
	if err != nil {
		return nil, err
	}
	tlsName, err := tunnelServerName(p.TunnelServerName, certificates)
	if err != nil {
		return nil, err
	}
	c := atrust.NewClient(atrust.ClientOptions{TunnelCertificates: certificates, TunnelServerName: tlsName, UnderlayDialer: &nativeUnderlay{net.Dialer{Timeout: 15 * time.Second}}, TLSConfig: &tls.Config{MinVersion: tls.VersionTLS12, InsecureSkipVerify: p.Insecure}})
	setupBase := ctx
	setupTimeout := 60 * time.Second
	var handler authchallenge.Handler = authchallenge.UnsupportedHandler{}
	attempt, interactive := ctx.Value(casLoginKey{}).(*casLoginAttempt)
	if interactive {
		setupBase = attempt.ctx
		setupTimeout = 5 * time.Minute
		handler = authchallenge.HandlerFuncs{ExternalLogin: func(challenge authchallenge.ExternalLoginChallenge) (authchallenge.ExternalLoginResponse, error) {
			if challenge.Kind != authchallenge.ExternalLoginCAS {
				return authchallenge.ExternalLoginResponse{}, errors.New("only CAS browser login is supported")
			}
			loginURL, err := resolveCASLoginURL(u, challenge.LoginURL, p.LoginDomain)
			if err != nil {
				return authchallenge.ExternalLoginResponse{}, err
			}
			if attempt.callback == nil {
				return authchallenge.ExternalLoginResponse{}, ErrLoginRequired
			}
			callback, err := attempt.callback(attempt.ctx, loginURL)
			if err != nil {
				return authchallenge.ExternalLoginResponse{}, errors.New("CAS browser login canceled or failed")
			}
			if err = validateCASCallback(u, callback, p.LoginDomain); err != nil {
				return authchallenge.ExternalLoginResponse{}, err
			}
			return authchallenge.ExternalLoginResponse{CallbackURL: callback}, nil
		}}
	}
	setup, cancel := context.WithTimeout(setupBase, setupTimeout)
	defer cancel()
	stopSetup := context.AfterFunc(ctx, cancel)
	defer stopSetup()
	_, err = c.SetupContext(setup, atrust.SetupOptions{ServerAddress: u.Hostname(), ServerPort: port, LoginMethod: method, ChallengeHandler: handler, ResumeOnly: len(cached) > 0 && password == "" && !(interactive && p.AuthType == "auth/cas"), ClientData: cached, SaveClientData: func(data []byte) error {
		if state == nil {
			return nil
		}
		if err := validateATrustState(data, p); err != nil {
			return err
		}
		return state.store.Save(data)
	}, BeforeTunnel: bootstrapTunnelHook(ctx, p, c), SessionRefreshInterval: 5 * time.Minute})
	if err != nil {
		c.Close()
		if errors.Is(err, auth.ErrLoginRequired) || errors.Is(err, auth.ErrSessionInvalid) {
			return nil, ErrLoginRequired
		}
		if interactive {
			return nil, classifyCASFailure(err)
		}
		if p.AuthType == "auth/cas" {
			return nil, classifyCASFailure(err)
		}
		return nil, fmt.Errorf("aTrust authentication/tunnel setup: %w", err)
	}
	ip, err := c.IP()
	if err != nil {
		c.Close()
		return nil, err
	}
	addr, ok := netip.AddrFromSlice(ip)
	if !ok {
		c.Close()
		return nil, errors.New("invalid aTrust tunnel address")
	}
	conn, err := c.NewL3Conn()
	if err != nil {
		c.Close()
		return nil, err
	}
	servers, _ := c.DNSServers()
	var dns []netip.Addr
	for _, v := range servers {
		if a, e := netip.ParseAddr(v); e == nil {
			dns = append(dns, a)
		}
	}
	read := func(context.Context) ([]byte, error) { b := make([]byte, 65535); n, e := conn.Read(b); return b[:n], e }
	write := func(b []byte) error {
		n, e := conn.Write(b)
		if errors.Is(e, zclient.ErrResourceNotFound) {
			return nil
		}
		if e == nil && n != len(b) {
			return io.ErrShortWrite
		}
		return e
	}
	packets, err := newPacketSession(ctx, []netip.Addr{addr.Unmap()}, dns, 1400, read, write, func() error { e := conn.Close(); c.Close(); return e })
	if err != nil {
		conn.Close()
		c.Close()
		return nil, err
	}
	expectedIP := addr.Unmap()
	c.SetIPUpdateHandler(func(ip net.IP) error {
		next, ok := netip.AddrFromSlice(ip)
		if !ok || next.Unmap() != expectedIP {
			err := errors.New("aTrust tunnel address changed; recreate session")
			packets.invalidate(err)
			return err
		}
		return nil
	})
	currentIP, ipErr := c.IP()
	currentAddr, valid := netip.AddrFromSlice(currentIP)
	if ipErr != nil || !valid || currentAddr.Unmap() != expectedIP {
		packets.invalidate(errors.New("aTrust tunnel address changed during setup; recreate session"))
	}
	domains, _ := c.DomainResources()
	resources, _ := c.DNSResource()
	return &aTrustSession{client: c, packets: packets, domains: domains, dns: resources}, nil
}
func (s *aTrustSession) Close() error          { return s.packets.Close() }
func (s *aTrustSession) Done() <-chan struct{} { return s.packets.Done() }
func (s *aTrustSession) Err() error {
	if err := s.packets.Err(); err != nil {
		return err
	}
	if err := s.client.SessionError(); err != nil {
		s.packets.invalidate(err)
		return err
	}
	return nil
}
func (s *aTrustSession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if err := s.Err(); err != nil {
		return nil, err
	}
	if network != "tcp" {
		return s.packets.dialPacket(ctx, network, address, s.dns, func(ip netip.Addr, protocol string, port int) error {
			if !ip.Is4() {
				return ErrAddressFamily
			}
			if err := s.client.CheckL3Target(net.IP(ip.AsSlice()), protocol, port); err != nil {
				return fmt.Errorf("%w: aTrust target is outside the permitted %s resources", ErrResourceDenied, protocol)
			}
			return nil
		})
	}
	host, portText, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	port, err := strconv.Atoi(portText)
	if err != nil || port < 1 || port > 65535 {
		return nil, errors.New("invalid target port")
	}
	ip := net.ParseIP(host)
	if ip == nil {
		canonical := strings.ToLower(strings.TrimSuffix(host, "."))
		var chosen *zclient.DomainResource
		longest := -1
		for name, entries := range s.domains {
			name = strings.ToLower(strings.TrimSuffix(name, "."))
			base := strings.TrimPrefix(strings.TrimPrefix(name, "*."), ".")
			match := base != "" && (canonical == base || strings.HasSuffix(canonical, "."+base))
			if !match || len(name) < longest {
				continue
			}
			if r, ok := zclient.MatchDomainResource(entries, "tcp", port); ok {
				copy := r
				chosen = &copy
				longest = len(name)
			}
		}
		if chosen != nil && !chosen.EnableTCPPrefL3 && chosen.AddrPretend {
			ctx = context.WithValue(ctx, resolve.ContextKeyDomainResource, *chosen)
			ctx = context.WithValue(ctx, resolve.ContextKeyResolveHost, canonical)
			return s.client.DialTCP(ctx, &net.TCPAddr{IP: net.IPv4zero, Port: port})
		}
		if ips := s.dns[canonical]; len(ips) > 0 {
			ip = ips[0]
		} else {
			ips, e := s.packets.lookupPacketHost(ctx, canonical, nil)
			if e != nil {
				return nil, e
			}
			if len(ips) == 0 {
				return nil, errors.New("VPN DNS returned no addresses")
			}
			ip = net.IP(ips[0].AsSlice())
		}
		if chosen != nil {
			ctx = context.WithValue(ctx, resolve.ContextKeyDomainResource, *chosen)
			ctx = context.WithValue(ctx, resolve.ContextKeyResolveHost, canonical)
		}
	}
	conn, err := s.client.DialTCP(ctx, &net.TCPAddr{IP: ip, Port: port})
	if !errors.Is(err, zclient.ErrResourceNotFound) {
		return conn, err
	}
	return s.packets.DialContext(ctx, "tcp", net.JoinHostPort(ip.String(), portText))
}
