package atrust

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"sync/atomic"
	"testing"
	"time"
)

func TestOCHNodeSelectionRejectsFastUntrustedNodeAndUsesLANFallback(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	certificate := func(serial int64) (*x509.Certificate, tls.Certificate) {
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "sdp.ssl.com"}, DNSNames: []string{"sdp.ssl.com"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, BasicConstraintsValid: true}
		der, e := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if e != nil {
			t.Fatal(e)
		}
		parsed, e := x509.ParseCertificate(der)
		if e != nil {
			t.Fatal(e)
		}
		return parsed, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	trusted, pair := certificate(1)
	_, otherPair := certificate(2)
	var applicationRequests atomic.Int32
	server := func(pair tls.Certificate) *httptest.Server {
		s := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { applicationRequests.Add(1) }))
		s.Config.ErrorLog = log.New(io.Discard, "", 0)
		s.TLS = &tls.Config{Certificates: []tls.Certificate{pair}}
		s.StartTLS()
		t.Cleanup(s.Close)
		return s
	}
	good, bad := server(pair), server(otherPair)
	goodAddress, badAddress := good.Listener.Addr().String(), bad.Listener.Addr().String()
	c := NewClient(ClientOptions{TunnelCertificates: []*x509.Certificate{trusted}, TunnelServerName: "sdp.ssl.com"})
	defer c.Close()
	var goodCalls, badCalls atomic.Int32
	dial := func(ctx context.Context, network, address string) (net.Conn, error) {
		if address == goodAddress {
			goodCalls.Add(1)
			timer := time.NewTimer(20 * time.Millisecond)
			select {
			case <-ctx.Done():
				timer.Stop()
				return nil, ctx.Err()
			case <-timer.C:
			}
		} else if address == badAddress {
			badCalls.Add(1)
		}
		return (&net.Dialer{}).DialContext(ctx, network, address)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	best := getBestNodes(ctx, map[string]NodeGroup{
		"mixed":    {WAN: []string{"malformed-node", badAddress, goodAddress}},
		"fallback": {WAN: []string{badAddress}, LAN: []string{goodAddress}},
	}, dial, nil, c.tunnelTLSConfig())
	if best["mixed"] != goodAddress || best["fallback"] != goodAddress {
		t.Fatal("selected untrusted or incorrectly indexed node", best)
	}
	if goodCalls.Load() == 0 || badCalls.Load() == 0 {
		t.Fatal("fixture did not exercise both certificate identities")
	}
	if applicationRequests.Load() != 0 {
		t.Fatal("node probes sent application data")
	}
}

func TestOCHVerifiedWANChoiceDoesNotProbeLAN(t *testing.T) {
	calls := 0
	got := selectBestNodes(map[string]NodeGroup{"group": {WAN: []string{"trusted-wan"}, LAN: []string{"trusted-lan"}}}, func(groups map[string][]string) map[string]string {
		calls++
		if groups["group"][0] != "trusted-wan" {
			t.Error("LAN probed despite verified WAN")
		}
		return map[string]string{"group": "trusted-wan"}
	})
	if calls != 1 || got["group"] != "trusted-wan" {
		t.Fatal(calls, got)
	}
}
