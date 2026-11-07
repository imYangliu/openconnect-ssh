package atrust

import (
	"context"
	"crypto/subtle"
	"crypto/tls"
	"crypto/x509"
	"errors"
	"io"
	"net"

	"github.com/mythologyli/zju-connect/client"
)

func tlsConfig(config *tls.Config, keyLogWriter io.Writer) *tls.Config {
	if config == nil {
		config = &tls.Config{}
	} else {
		config = config.Clone()
	}
	if keyLogWriter != nil {
		config.KeyLogWriter = keyLogWriter
	}
	return config
}

// tunnelTLSConfig mirrors the official client's standard TLS transport.
func tunnelTLSConfig(keyLogWriter io.Writer) *tls.Config {
	return tlsConfig(&tls.Config{
		InsecureSkipVerify:     false,
		SessionTicketsDisabled: true,
	}, keyLogWriter)
}

func dialTLSContext(ctx context.Context, dialer client.UnderlayDialer, network, address string, config *tls.Config) (*tls.Conn, error) {
	if dialer == nil {
		return nil, errors.New("underlay dialer is required")
	}
	rawConn, err := dialer.DialContext(ctx, network, address)
	if err != nil {
		return nil, err
	}
	if config == nil {
		config = &tls.Config{}
	} else {
		config = config.Clone()
	}
	if config.ServerName == "" {
		host, _, splitErr := net.SplitHostPort(address)
		if splitErr == nil {
			config.ServerName = host
		}
	}
	tlsConn := tls.Client(rawConn, config)
	if err := tlsConn.HandshakeContext(ctx); err != nil {
		_ = rawConn.Close()
		return nil, err
	}
	return tlsConn, nil
}

func (c *Client) tunnelTLSConfig() *tls.Config {
	config := tlsConfig(c.tlsConfig, c.tlsKeyLogWriter)
	if c.tunnelServerName != "" {
		config.ServerName = c.tunnelServerName
	}
	if len(c.tunnelCertificates) > 0 {
		config.InsecureSkipVerify = false
		config.RootCAs = x509.NewCertPool()
		expected := make([][]byte, 0, len(c.tunnelCertificates))
		for _, certificate := range c.tunnelCertificates {
			config.RootCAs.AddCert(certificate)
			expected = append(expected, append([]byte(nil), certificate.Raw...))
		}
		previous := config.VerifyConnection
		config.VerifyConnection = func(state tls.ConnectionState) error {
			matched := 0
			if len(state.PeerCertificates) > 0 {
				for _, certificate := range expected {
					matched |= subtle.ConstantTimeCompare(state.PeerCertificates[0].Raw, certificate)
				}
			}
			if matched != 1 {
				return errors.New("aTrust tunnel certificate does not match the configured certificate set")
			}
			if previous != nil {
				return previous(state)
			}
			return nil
		}
	}
	config.SessionTicketsDisabled = true
	return config
}
