package proxy

import (
	"bytes"
	"context"
	"encoding/binary"
	"errors"
	"io"
	"net"
	"strconv"
	"sync"
	"testing"
	"time"
)

type udpTestDialer struct {
	done    chan struct{}
	dial    func(context.Context, string, string, string) (net.Conn, error)
	session func(context.Context, string) (<-chan struct{}, error)
}

func (d *udpTestDialer) Dial(context.Context, string, string) (net.Conn, error) {
	return nil, errors.New("unexpected TCP dial")
}

func (d *udpTestDialer) DialNetwork(ctx context.Context, via, network, address string) (net.Conn, error) {
	return d.dial(ctx, via, network, address)
}

func (d *udpTestDialer) SessionDone(ctx context.Context, via string) (<-chan struct{}, error) {
	if d.session != nil {
		return d.session(ctx, via)
	}
	return d.done, nil
}

func udpEcho(t *testing.T) *net.UDPAddr {
	return udpEchoNetwork(t, "udp4", net.IPv4(127, 0, 0, 1))
}

func udpEchoNetwork(t *testing.T, network string, ip net.IP) *net.UDPAddr {
	t.Helper()
	conn, err := net.ListenUDP(network, &net.UDPAddr{IP: ip})
	if err != nil {
		if network == "udp6" {
			t.Skipf("IPv6 loopback unavailable: %v", err)
		}
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	go func() {
		buffer := make([]byte, 65536)
		for {
			n, peer, err := conn.ReadFromUDP(buffer)
			if err != nil {
				return
			}
			if _, err := conn.WriteToUDP(buffer[:n], peer); err != nil {
				return
			}
		}
	}()
	return conn.LocalAddr().(*net.UDPAddr)
}

func TestSOCKSUDPIPv6Target(t *testing.T) {
	target := udpEchoNetwork(t, "udp6", net.IPv6loopback)
	address, _ := startMixedUDP(t, NewHandler(realUDPDialer(t)), context.Background())
	control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
	defer control.Close()
	if code != 0 {
		t.Fatal(code)
	}
	client := udpClient(t)
	client.WriteToUDP(udpFrame(t, target, []byte("IPv6 destination")), relay)
	readUDPFrame(t, client, target.String(), []byte("IPv6 destination"))
}

func udpClient(t *testing.T) *net.UDPConn {
	t.Helper()
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1)})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	return conn
}

func startMixedUDP(t *testing.T, handler *Handler, ctx context.Context) (string, <-chan struct{}) {
	t.Helper()
	listener, err := net.Listen("tcp4", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan struct{})
	go func() {
		defer close(done)
		conn, err := listener.Accept()
		if err == nil {
			handler.HandleMixed(ctx, conn, "campus")
		}
	}()
	t.Cleanup(func() {
		listener.Close()
		select {
		case <-done:
		case <-time.After(2 * time.Second):
			t.Error("UDP association handler did not stop")
		}
	})
	return listener.Addr().String(), done
}

func requestUDP(t *testing.T, address string, source []byte) (net.Conn, *net.UDPAddr, byte) {
	t.Helper()
	conn, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { conn.Close() })
	conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write(append([]byte{5, 1, 0, 5, 3, 0}, source...)); err != nil {
		t.Fatal(err)
	}
	var greeting [2]byte
	if _, err := io.ReadFull(conn, greeting[:]); err != nil || greeting != [2]byte{5, 0} {
		t.Fatalf("SOCKS greeting %v, %v", greeting, err)
	}
	var reply [4]byte
	if _, err := io.ReadFull(conn, reply[:]); err != nil {
		t.Fatal(err)
	}
	host, port, err := readSOCKSAddress(conn, reply[3])
	if err != nil {
		t.Fatal(err)
	}
	portNumber, _ := strconv.Atoi(port)
	conn.SetDeadline(time.Time{})
	return conn, &net.UDPAddr{IP: net.ParseIP(host), Port: portNumber}, reply[1]
}

func udpFrame(t *testing.T, target *net.UDPAddr, payload []byte) []byte {
	t.Helper()
	address, err := numericSOCKSAddress(target)
	if err != nil {
		t.Fatal(err)
	}
	return append(append([]byte{0, 0, 0}, address...), payload...)
}

func readUDPFrame(t *testing.T, client *net.UDPConn, wantTarget string, wantPayload []byte) {
	t.Helper()
	client.SetReadDeadline(time.Now().Add(2 * time.Second))
	buffer := make([]byte, 65536)
	n, _, err := client.ReadFromUDP(buffer)
	if err != nil {
		t.Fatal(err)
	}
	target, payload, err := decodeSOCKSUDP(buffer[:n])
	if err != nil || target != wantTarget || !bytes.Equal(payload, wantPayload) {
		t.Fatalf("UDP reply target=%q payload=%q error=%v; want target=%q payload=%q", target, payload, err, wantTarget, wantPayload)
	}
}

func expectNoUDP(t *testing.T, conn *net.UDPConn) {
	t.Helper()
	conn.SetReadDeadline(time.Now().Add(80 * time.Millisecond))
	var buffer [65536]byte
	_, _, err := conn.ReadFromUDP(buffer[:])
	var timeout net.Error
	if !errors.As(err, &timeout) || !timeout.Timeout() {
		t.Fatalf("expected discarded UDP packet, got %v", err)
	}
}

func realUDPDialer(t *testing.T) *udpTestDialer {
	t.Helper()
	return &udpTestDialer{done: make(chan struct{}), dial: func(ctx context.Context, via, network, target string) (net.Conn, error) {
		if via != "campus" || network != "udp" {
			t.Errorf("dial escaped fixed UDP VPN: via=%q network=%q", via, network)
		}
		return (&net.Dialer{}).DialContext(ctx, network, target)
	}}
}

func TestSOCKSUDPBidirectionalTargetsDomainAndEmptyDatagram(t *testing.T) {
	first, second := udpEcho(t), udpEcho(t)
	dialer := realUDPDialer(t)
	realDial := dialer.dial
	domains := make(chan string, 1)
	dialer.dial = func(ctx context.Context, via, network, target string) (net.Conn, error) {
		if host, _, _ := net.SplitHostPort(target); host == "internal.invalid" {
			domains <- target
			target = second.String()
		}
		return realDial(ctx, via, network, target)
	}
	handler := NewHandler(dialer)
	address, _ := startMixedUDP(t, handler, context.Background())
	control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
	defer control.Close()
	if code != 0 || !relay.IP.IsLoopback() || relay.Port == 0 {
		t.Fatalf("invalid UDP relay response: %v code=%d", relay, code)
	}
	client := udpClient(t)
	for _, payload := range [][]byte{[]byte("first"), {}, []byte("after empty")} {
		if _, err := client.WriteToUDP(udpFrame(t, first, payload), relay); err != nil {
			t.Fatal(err)
		}
		readUDPFrame(t, client, first.String(), payload)
	}
	frame := append([]byte{0, 0, 0, 3, byte(len("internal.invalid"))}, "internal.invalid"...)
	frame = binary.BigEndian.AppendUint16(frame, uint16(second.Port))
	frame = append(frame, "domain reply"...)
	client.WriteToUDP(frame, relay)
	readUDPFrame(t, client, second.String(), []byte("domain reply"))
	select {
	case target := <-domains:
		if target != net.JoinHostPort("internal.invalid", strconv.Itoa(second.Port)) {
			t.Fatal(target)
		}
	case <-time.After(time.Second):
		t.Fatal("domain was not passed unresolved to VPN dialer")
	}
	client.WriteToUDP(udpFrame(t, first, []byte("first again")), relay)
	readUDPFrame(t, client, first.String(), []byte("first again"))
}

func TestSOCKSUDPInvalidFramesDoNotPinAndPinnedPortRejectsHijack(t *testing.T) {
	target := udpEcho(t)
	address, _ := startMixedUDP(t, NewHandler(realUDPDialer(t)), context.Background())
	control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
	defer control.Close()
	if code != 0 {
		t.Fatal(code)
	}
	attacker, client := udpClient(t), udpClient(t)
	valid := udpFrame(t, target, []byte("valid"))
	fragment := bytes.Clone(valid)
	fragment[2] = 1
	reserved := bytes.Clone(valid)
	reserved[0] = 1
	for _, frame := range [][]byte{fragment, reserved, {0, 0, 0}, {0, 0, 0, 9}, {0, 0, 0, 1, 127}} {
		attacker.WriteToUDP(frame, relay)
	}
	client.WriteToUDP(valid, relay)
	readUDPFrame(t, client, target.String(), []byte("valid"))
	attacker.WriteToUDP(valid, relay)
	expectNoUDP(t, attacker)
	client.WriteToUDP(udpFrame(t, target, []byte("still owned")), relay)
	readUDPFrame(t, client, target.String(), []byte("still owned"))
}

func TestSOCKSUDPExplicitSourcePort(t *testing.T) {
	target, client, attacker := udpEcho(t), udpClient(t), udpClient(t)
	source, err := numericSOCKSAddress(client.LocalAddr())
	if err != nil {
		t.Fatal(err)
	}
	address, _ := startMixedUDP(t, NewHandler(realUDPDialer(t)), context.Background())
	control, relay, code := requestUDP(t, address, source)
	defer control.Close()
	if code != 0 {
		t.Fatal(code)
	}
	frame := udpFrame(t, target, []byte("explicit"))
	attacker.WriteToUDP(frame, relay)
	expectNoUDP(t, attacker)
	client.WriteToUDP(frame, relay)
	readUDPFrame(t, client, target.String(), []byte("explicit"))
}

func TestSOCKSUDPForeignSourceIPCannotPinEndpoint(t *testing.T) {
	attacker, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4(127, 0, 0, 2)})
	if err != nil {
		t.Skipf("second loopback address unavailable: %v", err)
	}
	defer attacker.Close()
	target := udpEcho(t)
	address, _ := startMixedUDP(t, NewHandler(realUDPDialer(t)), context.Background())
	control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
	defer control.Close()
	if code != 0 {
		t.Fatal(code)
	}
	frame := udpFrame(t, target, []byte("IP pin"))
	attacker.WriteToUDP(frame, relay)
	expectNoUDP(t, attacker)
	client := udpClient(t)
	client.WriteToUDP(frame, relay)
	readUDPFrame(t, client, target.String(), []byte("IP pin"))
}

func TestSOCKSUDPRejectsForeignAndDomainSources(t *testing.T) {
	for _, source := range [][]byte{{1, 127, 0, 0, 2, 0, 0}, append(append([]byte{3, byte(len("localhost"))}, "localhost"...), 0, 0)} {
		address, _ := startMixedUDP(t, NewHandler(realUDPDialer(t)), context.Background())
		control, _, code := requestUDP(t, address, source)
		control.Close()
		if code == 0 {
			t.Fatalf("accepted invalid source %v", source)
		}
	}
}

type trackedUDPConn struct {
	net.Conn
	once   sync.Once
	closed chan struct{}
}

type addressedUDPConn struct {
	net.Conn
	remote net.Addr
}

func (c addressedUDPConn) RemoteAddr() net.Addr { return c.remote }

func (c *trackedUDPConn) Close() error {
	c.once.Do(func() { close(c.closed) })
	return c.Conn.Close()
}

func TestSOCKSUDPLifetimeClosesRelayAndFlows(t *testing.T) {
	for _, lifetime := range []string{"control EOF", "session logout", "daemon stop", "logout before flow"} {
		t.Run(lifetime, func(t *testing.T) {
			target := udpEcho(t)
			dialer := realUDPDialer(t)
			dial := dialer.dial
			closed := make(chan struct{})
			dialer.dial = func(ctx context.Context, via, network, address string) (net.Conn, error) {
				conn, err := dial(ctx, via, network, address)
				if err != nil {
					return nil, err
				}
				return &trackedUDPConn{Conn: conn, closed: closed}, nil
			}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			handler := NewHandler(dialer)
			address, done := startMixedUDP(t, handler, ctx)
			control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
			defer control.Close()
			if code != 0 {
				t.Fatal(code)
			}
			client := udpClient(t)
			if lifetime != "logout before flow" {
				client.WriteToUDP(udpFrame(t, target, []byte("live")), relay)
				readUDPFrame(t, client, target.String(), []byte("live"))
			}
			switch lifetime {
			case "control EOF":
				control.(*net.TCPConn).CloseWrite()
			case "session logout", "logout before flow":
				close(dialer.done)
			case "daemon stop":
				cancel()
			}
			select {
			case <-done:
			case <-time.After(2 * time.Second):
				t.Fatal("association outlived its control, session, or daemon")
			}
			if lifetime != "logout before flow" {
				select {
				case <-closed:
				default:
					t.Fatal("UDP flow left open")
				}
			}
			if len(handler.udpSlots) != 0 {
				t.Fatal("association slot leaked")
			}
			client.WriteToUDP(udpFrame(t, target, []byte("closed")), relay)
			expectNoUDP(t, client)
		})
	}
}

func TestSOCKSUDPSlowFlowDoesNotBlockOthersAndFlowLimit(t *testing.T) {
	target := udpEcho(t)
	dialer := realUDPDialer(t)
	dial := dialer.dial
	slowStarted, slowCanceled := make(chan struct{}), make(chan struct{})
	dialer.dial = func(ctx context.Context, via, network, address string) (net.Conn, error) {
		if address == "slow.invalid:99" {
			close(slowStarted)
			<-ctx.Done()
			close(slowCanceled)
			return nil, ctx.Err()
		}
		return dial(ctx, via, network, address)
	}
	handler := NewHandler(dialer)
	handler.udpOptions.maxFlows = 2
	handler.udpOptions.queueSize = 1
	address, done := startMixedUDP(t, handler, context.Background())
	control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
	defer control.Close()
	if code != 0 {
		t.Fatal(code)
	}
	client := udpClient(t)
	slowFrame := append([]byte{0, 0, 0, 3, byte(len("slow.invalid"))}, "slow.invalid"...)
	slowFrame = append(slowFrame, 0, 99, 1)
	client.WriteToUDP(slowFrame, relay)
	select {
	case <-slowStarted:
	case <-time.After(time.Second):
		t.Fatal("slow flow did not begin")
	}
	for range 32 {
		client.WriteToUDP(slowFrame, relay)
	}
	client.WriteToUDP(udpFrame(t, target, []byte("fast")), relay)
	readUDPFrame(t, client, target.String(), []byte("fast"))
	other := udpEcho(t)
	client.WriteToUDP(udpFrame(t, other, []byte("exceeds limit")), relay)
	expectNoUDP(t, client)
	control.Close()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("shutdown did not cancel pending dial")
	}
	select {
	case <-slowCanceled:
	default:
		t.Fatal("slow dial context was not canceled")
	}
}

func TestSOCKSUDPIdleFlowExpires(t *testing.T) {
	target := udpEcho(t)
	dialer := realUDPDialer(t)
	dial := dialer.dial
	closed := make(chan struct{})
	dialer.dial = func(ctx context.Context, via, network, address string) (net.Conn, error) {
		conn, err := dial(ctx, via, network, address)
		if err != nil {
			return nil, err
		}
		return &trackedUDPConn{Conn: conn, closed: closed}, nil
	}
	handler := NewHandler(dialer)
	handler.udpOptions.idleTimeout = 20 * time.Millisecond
	address, _ := startMixedUDP(t, handler, context.Background())
	control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
	defer control.Close()
	if code != 0 {
		t.Fatal(code)
	}
	client := udpClient(t)
	client.WriteToUDP(udpFrame(t, target, []byte("expires")), relay)
	readUDPFrame(t, client, target.String(), []byte("expires"))
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("idle flow was not closed")
	}
}

func TestSOCKSUDPBlockedWriteHasDeadline(t *testing.T) {
	dialer := realUDPDialer(t)
	closed := make(chan struct{})
	dialer.dial = func(context.Context, string, string, string) (net.Conn, error) {
		local, remote := net.Pipe()
		t.Cleanup(func() { remote.Close() })
		return &trackedUDPConn{Conn: addressedUDPConn{Conn: local, remote: &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 99}}, closed: closed}, nil
	}
	handler := NewHandler(dialer)
	handler.udpOptions.writeTimeout = 20 * time.Millisecond
	address, _ := startMixedUDP(t, handler, context.Background())
	control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
	defer control.Close()
	if code != 0 {
		t.Fatal(code)
	}
	client := udpClient(t)
	client.WriteToUDP(udpFrame(t, &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 99}, []byte("blocked write")), relay)
	select {
	case <-closed:
	case <-time.After(time.Second):
		t.Fatal("blocked destination write was not interrupted by deadline")
	}
}

func TestSOCKSUDPOversizedResponseIsDropped(t *testing.T) {
	target := &net.UDPAddr{IP: net.IPv4(127, 0, 0, 1), Port: 99}
	dialer := realUDPDialer(t)
	serverErr := make(chan error, 1)
	dialer.dial = func(context.Context, string, string, string) (net.Conn, error) {
		local, remote := net.Pipe()
		t.Cleanup(func() { remote.Close() })
		go func() {
			defer remote.Close()
			var request [64]byte
			_, err := remote.Read(request[:])
			if err == nil {
				_, err = remote.Write(make([]byte, maxSOCKSUDPDatagram))
			}
			if err == nil {
				_, err = remote.Write([]byte("fits"))
			}
			serverErr <- err
		}()
		return addressedUDPConn{Conn: local, remote: target}, nil
	}
	address, _ := startMixedUDP(t, NewHandler(dialer), context.Background())
	control, relay, code := requestUDP(t, address, []byte{1, 0, 0, 0, 0, 0, 0})
	defer control.Close()
	if code != 0 {
		t.Fatal(code)
	}
	client := udpClient(t)
	client.WriteToUDP(udpFrame(t, target, []byte("request")), relay)
	readUDPFrame(t, client, target.String(), []byte("fits"))
	if err := <-serverErr; err != nil {
		t.Fatal(err)
	}
	expectNoUDP(t, client)
}

func TestSOCKSUDPAssociationLimit(t *testing.T) {
	handler := NewHandler(realUDPDialer(t))
	handler.udpSlots = make(chan struct{}, 1)
	firstAddress, _ := startMixedUDP(t, handler, context.Background())
	first, _, code := requestUDP(t, firstAddress, []byte{1, 0, 0, 0, 0, 0, 0})
	defer first.Close()
	if code != 0 {
		t.Fatal(code)
	}
	secondAddress, _ := startMixedUDP(t, handler, context.Background())
	second, _, code := requestUDP(t, secondAddress, []byte{1, 0, 0, 0, 0, 0, 0})
	second.Close()
	if code == 0 {
		t.Fatal("association limit was not enforced")
	}
}

func TestSOCKSUDPControlEOFCancelsSessionInitialization(t *testing.T) {
	dialer := realUDPDialer(t)
	started, canceled := make(chan struct{}), make(chan struct{})
	dialer.session = func(ctx context.Context, via string) (<-chan struct{}, error) {
		if via != "campus" {
			t.Errorf("session initialized with via=%q", via)
		}
		close(started)
		<-ctx.Done()
		close(canceled)
		return nil, ctx.Err()
	}
	handler := NewHandler(dialer)
	address, done := startMixedUDP(t, handler, context.Background())
	control, err := net.Dial("tcp", address)
	if err != nil {
		t.Fatal(err)
	}
	defer control.Close()
	control.Write([]byte{5, 1, 0, 5, 3, 0, 1, 0, 0, 0, 0, 0, 0})
	select {
	case <-started:
	case <-time.After(time.Second):
		t.Fatal("session initialization did not begin")
	}
	control.(*net.TCPConn).CloseWrite()
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("control EOF did not stop session initialization")
	}
	select {
	case <-canceled:
	default:
		t.Fatal("session initialization context was not canceled")
	}
	if len(handler.udpSlots) != 0 {
		t.Fatal("initializing association slot leaked")
	}
}

func TestSOCKSUDPAddressValidation(t *testing.T) {
	for _, frame := range [][]byte{
		{0, 0, 1, 1, 127, 0, 0, 1, 0, 53},
		{0, 1, 0, 1, 127, 0, 0, 1, 0, 53},
		{0, 0, 0, 1, 127, 0, 0, 1, 0, 0},
		{0, 0, 0, 3, 0, 0, 53},
		{0, 0, 0, 3, 1, '\n', 0, 53},
		{0, 0, 0, 3, 4, 'a'},
		{0, 0, 0, 4, 0},
	} {
		if _, _, err := decodeSOCKSUDP(frame); err == nil {
			t.Fatalf("accepted malformed UDP frame %v", frame)
		}
	}
	v6 := &net.UDPAddr{IP: net.ParseIP("2001:db8::1"), Port: 53}
	frame := udpFrame(t, v6, nil)
	if target, payload, err := decodeSOCKSUDP(frame); err != nil || target != "[2001:db8::1]:53" || len(payload) != 0 {
		t.Fatalf("IPv6 frame target=%q payload=%v error=%v", target, payload, err)
	}
}
