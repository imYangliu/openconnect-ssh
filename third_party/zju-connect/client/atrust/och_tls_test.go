package atrust

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"errors"
	"io"
	"log"
	"math/big"
	"net"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

type ochTestUnderlay struct{ net.Dialer }

func (*ochTestUnderlay) ExcludeIP(net.IP) {}

func TestOCHExplicitTunnelServerNameKeepsCertificateVerification(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"vpn.example.edu"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour), KeyUsage: x509.KeyUsageDigitalSignature | x509.KeyUsageCertSign, ExtKeyUsage: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}, IsCA: true, BasicConstraintsValid: true}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	certificate, err := x509.ParseCertificate(der)
	if err != nil {
		t.Fatal(err)
	}
	roots := x509.NewCertPool()
	roots.AddCert(certificate)
	server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	server.Config.ErrorLog = log.New(io.Discard, "", 0)
	server.TLS = &tls.Config{Certificates: []tls.Certificate{{Certificate: [][]byte{der}, PrivateKey: key}}}
	server.StartTLS()
	defer server.Close()
	underlay := &ochTestUnderlay{net.Dialer{Timeout: time.Second}}
	for _, tc := range []struct {
		name  string
		roots *x509.CertPool
		want  string
	}{
		{"vpn.example.edu", roots, ""},
		{"wrong.example.edu", roots, "hostname"},
		{"", roots, "hostname"},
		{"vpn.example.edu", x509.NewCertPool(), "authority"},
	} {
		authTLS := &tls.Config{RootCAs: tc.roots, MinVersion: tls.VersionTLS12}
		c := NewClient(ClientOptions{TLSConfig: authTLS, TunnelServerName: tc.name, UnderlayDialer: underlay})
		tunnelConfig := c.tunnelTLSConfig()
		if tunnelConfig.InsecureSkipVerify || authTLS.ServerName != "" || c.tlsConfig.ServerName != "" {
			t.Fatal("tunnel option weakened TLS or changed auth identity")
		}
		conn, err := dialTLSContext(context.Background(), underlay, "tcp", server.Listener.Addr().String(), tunnelConfig)
		if conn != nil {
			conn.Close()
		}
		c.Close()
		if tc.want == "" && err != nil {
			t.Fatal(err)
		}
		if tc.want == "hostname" {
			var mismatch x509.HostnameError
			if !errors.As(err, &mismatch) {
				t.Fatalf("expected hostname rejection: %v", err)
			}
		}
		if tc.want == "authority" {
			var untrusted x509.UnknownAuthorityError
			if !errors.As(err, &untrusted) {
				t.Fatalf("expected authority rejection: %v", err)
			}
		}
	}
}

func TestOCHExactTunnelCertificatePreservesStandardChecks(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	makeCertificate := func(key *ecdsa.PrivateKey, serial int64, before, after time.Time, usage x509.ExtKeyUsage) (*x509.Certificate, tls.Certificate) {
		template := &x509.Certificate{SerialNumber: big.NewInt(serial), Subject: pkix.Name{CommonName: "sdp.ssl.com"}, DNSNames: []string{"sdp.ssl.com"}, NotBefore: before, NotAfter: after, ExtKeyUsage: []x509.ExtKeyUsage{usage}, BasicConstraintsValid: true}
		der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
		if err != nil {
			t.Fatal(err)
		}
		cert, err := x509.ParseCertificate(der)
		if err != nil {
			t.Fatal(err)
		}
		return cert, tls.Certificate{Certificate: [][]byte{der}, PrivateKey: key}
	}
	secondKey, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	now := time.Now()
	good, goodPair := makeCertificate(key, 10, now.Add(-time.Hour), now.Add(time.Hour), x509.ExtKeyUsageServerAuth)
	different, differentPair := makeCertificate(secondKey, 11, now.Add(-time.Hour), now.Add(time.Hour), x509.ExtKeyUsageServerAuth)
	expired, expiredPair := makeCertificate(key, 12, now.Add(-2*time.Hour), now.Add(-time.Hour), x509.ExtKeyUsageServerAuth)
	future, futurePair := makeCertificate(key, 13, now.Add(time.Hour), now.Add(2*time.Hour), x509.ExtKeyUsageServerAuth)
	wrongUsage, wrongUsagePair := makeCertificate(key, 14, now.Add(-time.Hour), now.Add(time.Hour), x509.ExtKeyUsageClientAuth)
	unlisted, unlistedPair := makeCertificate(key, 15, now.Add(-time.Hour), now.Add(time.Hour), x509.ExtKeyUsageServerAuth)
	for _, tc := range []struct {
		name, serverName string
		pins             []*x509.Certificate
		pair             tls.Certificate
		valid            bool
	}{
		{"exact", "sdp.ssl.com", []*x509.Certificate{good}, goodPair, true},
		{"first approved member", "sdp.ssl.com", []*x509.Certificate{good, different}, goodPair, true},
		{"second approved member", "sdp.ssl.com", []*x509.Certificate{good, different}, differentPair, true},
		{"unlisted distinct key", "sdp.ssl.com", []*x509.Certificate{good}, differentPair, false},
		{"unlisted leaf same key", "sdp.ssl.com", []*x509.Certificate{good, different}, unlistedPair, false},
		{"wrong hostname", "vpn.example.edu", []*x509.Certificate{good, different}, goodPair, false},
		{"expired", "sdp.ssl.com", []*x509.Certificate{good, expired}, expiredPair, false},
		{"not yet valid", "sdp.ssl.com", []*x509.Certificate{good, future}, futurePair, false},
		{"wrong usage", "sdp.ssl.com", []*x509.Certificate{good, wrongUsage}, wrongUsagePair, false},
		{"no configured trust", "sdp.ssl.com", nil, goodPair, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewUnstartedServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
			server.Config.ErrorLog = log.New(io.Discard, "", 0)
			server.TLS = &tls.Config{Certificates: []tls.Certificate{tc.pair}}
			server.StartTLS()
			defer server.Close()
			authTLS := &tls.Config{MinVersion: tls.VersionTLS12}
			c := NewClient(ClientOptions{TLSConfig: authTLS, TunnelServerName: tc.serverName, TunnelCertificates: tc.pins})
			defer c.Close()
			tunnel := c.tunnelTLSConfig()
			if tunnel.InsecureSkipVerify || authTLS.RootCAs != nil || authTLS.ServerName != "" || authTLS.VerifyConnection != nil {
				t.Fatal("pin weakened TLS or altered authentication TLS")
			}
			conn, err := dialTLSContext(context.Background(), &ochTestUnderlay{}, "tcp", server.Listener.Addr().String(), tunnel)
			if conn != nil {
				if len(conn.ConnectionState().VerifiedChains) == 0 {
					t.Error("standard certificate verification did not run")
				}
				conn.Close()
			}
			if (err == nil) != tc.valid {
				t.Fatalf("valid=%t err=%v", tc.valid, err)
			}
			if len(tc.pins) > 0 && tunnel.VerifyConnection(tls.ConnectionState{PeerCertificates: []*x509.Certificate{unlisted}}) == nil {
				t.Fatal("different DER with same public key accepted")
			}
		})
	}
}
