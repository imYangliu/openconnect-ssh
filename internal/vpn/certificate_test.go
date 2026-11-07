package vpn

import (
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/x509"
	"encoding/pem"
	"math/big"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestTunnelCertificateFileParsesExplicitSet(t *testing.T) {
	key, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		t.Fatal(err)
	}
	template := &x509.Certificate{SerialNumber: big.NewInt(1), DNSNames: []string{"vpn.example.edu"}, NotBefore: time.Now().Add(-time.Hour), NotAfter: time.Now().Add(time.Hour)}
	der, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	template.SerialNumber = big.NewInt(2)
	secondDER, err := x509.CreateCertificate(rand.Reader, template, template, &key.PublicKey, key)
	if err != nil {
		t.Fatal(err)
	}
	second := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: secondDER})
	certificate := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	path := filepath.Join(t.TempDir(), "cert.pem")
	for _, tc := range []struct {
		data  []byte
		valid bool
		count int
	}{
		{certificate, true, 1},
		{append(append(append([]byte{}, certificate...), second...), certificate...), true, 2},
		{nil, false, 0},
		{append([]byte("-----BEGIN CERTIFICATE-----\nnot-base64!\n-----END CERTIFICATE-----\n"), certificate...), false, 0},
		{append([]byte("-----BEGIN CERTIFICATE-----\n"), certificate...), false, 0},
		{[]byte("  \n"), false, 0},
		{[]byte(strings.Repeat("x", 1024*1024+1)), false, 0},
		{append(append([]byte{}, certificate...), certificate...), true, 1},
		{append([]byte("private-fixture\n"), certificate...), false, 0},
		{append(append([]byte{}, certificate...), []byte("private-fixture")...), false, 0},
		{pem.EncodeToMemory(&pem.Block{Type: "PRIVATE KEY", Bytes: der}), false, 0},
		{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Headers: map[string]string{"unexpected": "private-fixture"}, Bytes: der}), false, 0},
		{pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: []byte("private-fixture")}), false, 0},
	} {
		if err := os.WriteFile(path, tc.data, 0644); err != nil {
			t.Fatal(err)
		}
		cert, err := loadTunnelCertificates(path)
		if (err == nil) != tc.valid {
			t.Fatal(tc.valid, err)
		}
		if tc.valid && (len(cert) != tc.count || cert[0].DNSNames[0] != "vpn.example.edu") {
			t.Fatal("certificate changed")
		}
		if err != nil && strings.Contains(err.Error(), "private-fixture") {
			t.Fatal("certificate contents leaked")
		}
	}
	if cert, err := loadTunnelCertificates(""); cert != nil || err != nil {
		t.Fatal("default PKI behavior changed")
	}
}

func TestTunnelServerNameFromApprovedCertificates(t *testing.T) {
	for _, tc := range []struct {
		name       string
		configured string
		dnsNames   [][]string
		want       string
		wantError  bool
	}{
		{name: "unique common SAN", dnsNames: [][]string{{"SDP.example", "other.example"}, {"sdp.example", "sdp.example"}}, want: "sdp.example"},
		{name: "ambiguous SANs", dnsNames: [][]string{{"sdp.example", "other.example"}, {"other.example", "sdp.example"}}, wantError: true},
		{name: "no common SAN", dnsNames: [][]string{{"sdp.example"}, {"other.example"}}, wantError: true},
		{name: "missing SAN", dnsNames: [][]string{nil}, wantError: true},
		{name: "wildcard and IP", dnsNames: [][]string{{"*.example", "192.0.2.1"}}, wantError: true},
		{name: "explicit name", configured: "other.example", dnsNames: [][]string{{"sdp.example"}}, want: "other.example"},
		{name: "system trust"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var certificates []*x509.Certificate
			for _, names := range tc.dnsNames {
				certificate := &x509.Certificate{DNSNames: names}
				certificate.Subject.CommonName = "not-a-san.example"
				certificates = append(certificates, certificate)
			}
			got, err := tunnelServerName(tc.configured, certificates)
			if got != tc.want || (err != nil) != tc.wantError {
				t.Fatalf("tunnel name = %q, error = %v; want %q, error = %v", got, err, tc.want, tc.wantError)
			}
		})
	}
}
