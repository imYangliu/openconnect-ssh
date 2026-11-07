package vpn

import (
	"bytes"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"io"
	"net"
	"os"
	"strings"
)

func tunnelServerName(configured string, certificates []*x509.Certificate) (string, error) {
	if configured != "" || len(certificates) == 0 {
		return configured, nil
	}
	if inferred := commonTunnelDNSName(certificates); inferred != "" {
		return inferred, nil
	}
	return "", errors.New("approved tunnel certificates do not share a unique DNS name; configure tunnel_server_name")
}

func commonTunnelDNSName(certificates []*x509.Certificate) string {
	counts := make(map[string]int)
	for i, certificate := range certificates {
		for _, dns := range certificate.DNSNames {
			name := strings.ToLower(dns)
			if name == "" || strings.ContainsAny(name, " /:@*\t\r\n\x00") || net.ParseIP(name) != nil {
				continue
			}
			if counts[name] == i {
				counts[name]++
			}
		}
	}
	var common string
	for name, count := range counts {
		if count == len(certificates) {
			if common != "" {
				return ""
			}
			common = name
		}
	}
	return common
}

func loadTunnelCertificates(path string) ([]*x509.Certificate, error) {
	if path == "" {
		return nil, nil
	}
	file, err := os.Open(path)
	if err != nil {
		return nil, errors.New("could not open tunnel_certificate_file")
	}
	defer file.Close()
	data, err := io.ReadAll(io.LimitReader(file, 1024*1024+1))
	if err != nil || len(data) > 1024*1024 {
		return nil, errors.New("could not read tunnel_certificate_file or file exceeds 1 MiB")
	}
	remaining := bytes.TrimSpace(data)
	var certificates []*x509.Certificate
	seen := make(map[string]bool)
	for len(remaining) > 0 {
		if !bytes.HasPrefix(remaining, []byte("-----BEGIN CERTIFICATE-----")) {
			return nil, errors.New("tunnel_certificate_file must contain only PEM certificates")
		}
		endMarker := []byte("-----END CERTIFICATE-----")
		end := bytes.Index(remaining, endMarker)
		if end < 0 {
			return nil, errors.New("tunnel_certificate_file contains an incomplete PEM certificate")
		}
		end += len(endMarker)
		if bytes.Count(remaining[:end], []byte("-----BEGIN ")) != 1 {
			return nil, errors.New("tunnel_certificate_file contains an invalid PEM certificate")
		}
		block, rest := pem.Decode(remaining[:end])
		if block == nil || block.Type != "CERTIFICATE" || len(block.Headers) != 0 || len(bytes.TrimSpace(rest)) != 0 {
			return nil, errors.New("tunnel_certificate_file must contain only PEM certificates")
		}
		certificate, err := x509.ParseCertificate(block.Bytes)
		if err != nil {
			return nil, errors.New("tunnel_certificate_file contains an invalid certificate")
		}
		if !seen[string(certificate.Raw)] {
			certificates = append(certificates, certificate)
			seen[string(certificate.Raw)] = true
		}
		remaining = bytes.TrimSpace(remaining[end:])
	}
	if len(certificates) == 0 {
		return nil, errors.New("tunnel_certificate_file must contain at least one PEM certificate")
	}
	return certificates, nil
}
