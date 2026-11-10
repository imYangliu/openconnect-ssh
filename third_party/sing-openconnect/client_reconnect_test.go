package openconnect

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"math/big"
	"net"
	"strings"
	"sync"
	"testing"
	"time"

	M "github.com/sagernet/sing/common/metadata"
)

type reconnectTestDial struct {
	block    bool
	reject   bool
	rekey    bool
	started  chan struct{}
	canceled chan struct{}
	drop     chan struct{}
}

func newReconnectTestDial(block bool) *reconnectTestDial {
	return &reconnectTestDial{
		block: block, started: make(chan struct{}), canceled: make(chan struct{}), drop: make(chan struct{}),
	}
}

type reconnectTestDialer struct {
	access      sync.Mutex
	calls       int
	certificate tls.Certificate
	dials       []*reconnectTestDial
	workers     sync.WaitGroup
}

func (d *reconnectTestDialer) DialContext(ctx context.Context, _ string, _ M.Socksaddr) (net.Conn, error) {
	d.access.Lock()
	index := d.calls
	d.calls++
	d.access.Unlock()
	if index >= len(d.dials) {
		return nil, errors.New("unexpected fixture dial")
	}
	plan := d.dials[index]
	close(plan.started)
	if plan.block {
		<-ctx.Done()
		close(plan.canceled)
		return nil, ctx.Err()
	}
	client, server := net.Pipe()
	done := make(chan struct{})
	d.workers.Add(2)
	go func() {
		defer d.workers.Done()
		select {
		case <-plan.drop:
			_ = server.Close()
		case <-done:
		}
	}()
	go func() {
		defer d.workers.Done()
		defer close(done)
		defer server.Close()
		connection := tls.Server(server, &tls.Config{Certificates: []tls.Certificate{d.certificate}})
		reader := bufio.NewReader(connection)
		for {
			line, err := reader.ReadString('\n')
			if err != nil {
				return
			}
			if strings.TrimSpace(line) == "" {
				break
			}
		}
		response := "HTTP/1.1 200 OK\r\nX-CSTP-MTU: 1400\r\nX-CSTP-Address: 10.0.0.2\r\n\r\n"
		if plan.rekey {
			response = strings.Replace(response, "\r\n\r\n", "\r\nX-CSTP-Rekey-Time: 1\r\nX-CSTP-Rekey-Method: new-tunnel\r\n\r\n", 1)
		}
		if plan.reject {
			response = "HTTP/1.1 401 Unauthorized\r\n\r\n"
		}
		if _, err := io.WriteString(connection, response); err != nil {
			return
		}
		_, _ = io.Copy(io.Discard, connection)
	}()
	return client, nil
}

func (d *reconnectTestDialer) ListenPacket(context.Context, M.Socksaddr) (net.PacketConn, error) {
	return nil, errors.New("unused fixture UDP")
}

func newReconnectTestClient(t *testing.T, timeout time.Duration, dials ...*reconnectTestDial) *Client {
	t.Helper()
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{
		SerialNumber: big.NewInt(1), NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour),
		DNSNames: []string{"vpn.test"}, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	dialer := &reconnectTestDialer{
		certificate: tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}, dials: dials,
	}
	client, err := NewClient(ClientOptions{
		Server: "https://vpn.test", Cookie: "webvpn=fixture", NoUDP: true, ReconnectTimeout: timeout, Dialer: dialer,
		TLSConfig: ClientTLSOptions{Config: &tls.Config{InsecureSkipVerify: true}},
	})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		closed := make(chan struct{})
		go func() {
			_ = client.Close()
			dialer.workers.Wait()
			close(closed)
		}()
		waitReconnectTestSignal(t, closed, "client and fixture shutdown")
	})
	if err = client.Start(); err != nil {
		t.Fatal(err)
	}
	return client
}

func waitReconnectTestSignal(t *testing.T, signal <-chan struct{}, operation string) {
	t.Helper()
	select {
	case <-signal:
	case <-time.After(2 * time.Second):
		t.Fatalf("timed out waiting for %s", operation)
	}
}

func waitReconnectTestReady(t *testing.T, client *Client, timeout time.Duration, expected error) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()
	if err := client.WaitReady(ctx); !errors.Is(err, expected) {
		t.Fatalf("WaitReady returned %v, expected %v", err, expected)
	}
}

func TestReconnectTimeoutInterruptsBlockedDial(t *testing.T) {
	initial, blocked := newReconnectTestDial(false), newReconnectTestDial(true)
	client := newReconnectTestClient(t, 50*time.Millisecond, initial, blocked)
	waitReconnectTestReady(t, client, time.Second, nil)
	close(initial.drop)
	waitReconnectTestSignal(t, blocked.started, "blocked redial")
	waitReconnectTestReady(t, client, time.Second, ErrReconnectTimeout)
	waitReconnectTestSignal(t, blocked.canceled, "redial cancellation")
}

func TestReconnectRecoverySurvivesOldDeadlineAndGetsFreshTimeout(t *testing.T) {
	initial, recovered, blocked := newReconnectTestDial(false), newReconnectTestDial(false), newReconnectTestDial(true)
	const timeout = 150 * time.Millisecond
	client := newReconnectTestClient(t, timeout, initial, recovered, blocked)
	waitReconnectTestReady(t, client, time.Second, nil)
	close(initial.drop)
	waitReconnectTestSignal(t, recovered.started, "recovery dial")
	waitReconnectTestReady(t, client, time.Second, nil)
	time.Sleep(2 * timeout)
	if !client.Ready() {
		t.Fatal("recovered session ended after the old reconnect deadline")
	}
	close(recovered.drop)
	waitReconnectTestSignal(t, blocked.started, "second outage redial")
	waitReconnectTestReady(t, client, timeout/3, context.DeadlineExceeded)
	waitReconnectTestReady(t, client, time.Second, ErrReconnectTimeout)
	waitReconnectTestSignal(t, blocked.canceled, "second redial cancellation")
}

func TestReconnectCloseCancelsBlockedDial(t *testing.T) {
	initial, blocked := newReconnectTestDial(false), newReconnectTestDial(true)
	const timeout = 50 * time.Millisecond
	client := newReconnectTestClient(t, timeout, initial, blocked)
	waitReconnectTestReady(t, client, time.Second, nil)
	close(initial.drop)
	waitReconnectTestSignal(t, blocked.started, "blocked redial")
	closed := make(chan struct{})
	go func() {
		_ = client.Close()
		close(closed)
	}()
	waitReconnectTestSignal(t, closed, "Close during reconnect")
	waitReconnectTestSignal(t, blocked.canceled, "closed dial cancellation")
	time.Sleep(2 * timeout)
	waitReconnectTestReady(t, client, time.Second, ErrClientClosed)
}

func TestReconnectTimeoutDoesNotLimitInitialConnection(t *testing.T) {
	blocked := newReconnectTestDial(true)
	client := newReconnectTestClient(t, 50*time.Millisecond, blocked)
	waitReconnectTestSignal(t, blocked.started, "initial dial")
	waitReconnectTestReady(t, client, 150*time.Millisecond, context.DeadlineExceeded)
}

func TestReconnectTimeoutInterruptsAuthentication(t *testing.T) {
	initial, rejected, authentication := newReconnectTestDial(false), newReconnectTestDial(false), newReconnectTestDial(true)
	rejected.reject = true
	client := newReconnectTestClient(t, 1500*time.Millisecond, initial, rejected, authentication)
	waitReconnectTestReady(t, client, time.Second, nil)
	close(initial.drop)
	waitReconnectTestSignal(t, authentication.started, "authentication HTTP dial")
	waitReconnectTestReady(t, client, time.Second, ErrReconnectTimeout)
	if err := client.Close(); err != nil {
		t.Fatal(err)
	}
	waitReconnectTestSignal(t, authentication.canceled, "authentication transport shutdown")
}

func TestReconnectRekeyHasReconnectBudget(t *testing.T) {
	initial, blocked := newReconnectTestDial(false), newReconnectTestDial(true)
	initial.rekey = true
	client := newReconnectTestClient(t, 50*time.Millisecond, initial, blocked)
	waitReconnectTestReady(t, client, time.Second, nil)
	waitReconnectTestSignal(t, blocked.started, "rekey redial")
	waitReconnectTestReady(t, client, 200*time.Millisecond, ErrReconnectTimeout)
}
