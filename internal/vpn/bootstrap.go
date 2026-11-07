package vpn

import (
	"context"
	"crypto/sha256"
	"crypto/tls"
	"crypto/x509"
	"encoding/pem"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/mythologyli/zju-connect/client/atrust"
)

type CertificateInfo struct {
	SHA256   string
	Subject  string
	DNSNames []string
	NotAfter time.Time
}
type BootstrapOptions struct {
	Password          string
	PasswordPrompt    func(context.Context) (string, error)
	Browser           CASCallback
	TrustCertificates func(context.Context, []CertificateInfo, string) (string, error)
	result            *config.Profile
}
type bootstrapKey struct{}

// Bootstrap creates authenticated state before a daemon opens its own session.
func Bootstrap(ctx context.Context, p config.Profile, options BootstrapOptions) (config.Profile, error) {
	if p.AuthStateFile == "" {
		return p, errors.New("bootstrap requires auth_state_file")
	}
	options.result = &p
	ctx = context.WithValue(ctx, bootstrapKey{}, &options)
	if options.Browser != nil {
		ctx = context.WithValue(ctx, casLoginKey{}, &casLoginAttempt{ctx: ctx, callback: options.Browser})
	}
	session, err := NewNativeSession(ctx, p)
	if session != nil {
		if closeErr := session.Close(); err == nil {
			err = closeErr
		}
	}
	return p, err
}

func bootstrapTunnelHook(ctx context.Context, p config.Profile, c *atrust.Client) func(context.Context) error {
	options, _ := ctx.Value(bootstrapKey{}).(*BootstrapOptions)
	if options == nil || options.TrustCertificates == nil || p.TunnelCertificateFile != "" {
		return nil
	}
	return func(probeCtx context.Context) error {
		addresses := map[string]bool{}
		for _, g := range c.NodeGroups {
			for _, a := range append(append([]string(nil), g.WAN...), g.LAN...) {
				addresses[a] = true
			}
		}
		type result struct {
			certificate *x509.Certificate
			verified    bool
		}
		if len(addresses) > 64 {
			return errors.New("gateway published too many tunnel endpoints for automatic setup")
		}
		slots := make(chan struct{}, 8)
		results := make(chan result, len(addresses))
		for a := range addresses {
			go func(address string) {
				select {
				case slots <- struct{}{}:
					defer func() { <-slots }()
				case <-probeCtx.Done():
					results <- result{}
					return
				}
				bounded, cancel := context.WithTimeout(probeCtx, 4*time.Second)
				defer cancel()
				d := tls.Dialer{NetDialer: &net.Dialer{Timeout: 4 * time.Second}, Config: &tls.Config{MinVersion: tls.VersionTLS12, ServerName: p.TunnelServerName}}
				conn, err := d.DialContext(bounded, "tcp", address)
				if err == nil {
					conn.Close()
					results <- result{verified: true}
					return
				}
				var verification *tls.CertificateVerificationError
				if errors.As(err, &verification) && len(verification.UnverifiedCertificates) > 0 {
					results <- result{certificate: verification.UnverifiedCertificates[0]}
					return
				}
				results <- result{}
			}(a)
		}
		found := map[string]*x509.Certificate{}
		verified := false
		for range addresses {
			select {
			case <-probeCtx.Done():
				return probeCtx.Err()
			case r := <-results:
				verified = verified || r.verified
				if r.certificate != nil {
					hash := fmt.Sprintf("%x", sha256.Sum256(r.certificate.Raw))
					found[hash] = r.certificate
				}
			}
		}
		if verified {
			return nil
		}
		if len(found) == 0 {
			return errors.New("no reachable TLS tunnel node; authentication state was retained")
		}
		hashes := make([]string, 0, len(found))
		for h := range found {
			hashes = append(hashes, h)
		}
		sort.Strings(hashes)
		infos := make([]CertificateInfo, 0, len(hashes))
		certificates := make([]*x509.Certificate, 0, len(hashes))
		for _, h := range hashes {
			cert := found[h]
			infos = append(infos, CertificateInfo{SHA256: h, Subject: cert.Subject.String(), DNSNames: append([]string(nil), cert.DNSNames...), NotAfter: cert.NotAfter})
			certificates = append(certificates, cert)
		}
		inferred := commonTunnelDNSName(certificates)
		suggested := p.TunnelServerName
		if suggested == "" {
			suggested = inferred
		}
		name, err := options.TrustCertificates(probeCtx, infos, suggested)
		if err != nil {
			return err
		}
		if name == "" || strings.ContainsAny(name, " /:@*\t\r\n\x00") {
			return errors.New("tunnel certificates were not approved with a valid DNS identity")
		}
		for _, cert := range certificates {
			roots := x509.NewCertPool()
			roots.AddCert(cert)
			if _, err := cert.Verify(x509.VerifyOptions{Roots: roots, DNSName: name, KeyUsages: []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth}}); err != nil {
				return errors.New("approved tunnel certificate does not validate for the selected name, time or use")
			}
		}
		path := p.AuthStateFile + ".certificates.pem"
		if err := writeBootstrapCertificates(path, certificates); err != nil {
			return err
		}
		c.ConfigureTunnelTrust(name, certificates)
		if options.result != nil {
			if p.TunnelServerName != "" || name != inferred {
				options.result.TunnelServerName = name
			}
			options.result.TunnelCertificateFile = path
		}
		return nil
	}
}
func writeBootstrapCertificates(path string, certificates []*x509.Certificate) error {
	if info, err := os.Lstat(path); err == nil && !info.Mode().IsRegular() {
		return errors.New("certificate destination is not a regular file")
	}
	f, err := os.CreateTemp(filepath.Dir(path), ".och-certificates-")
	if err != nil {
		return errors.New("cannot create certificate bundle")
	}
	defer os.Remove(f.Name())
	for _, cert := range certificates {
		if err = pem.Encode(f, &pem.Block{Type: "CERTIFICATE", Bytes: cert.Raw}); err != nil {
			f.Close()
			return errors.New("cannot write certificate bundle")
		}
	}
	if err = f.Sync(); err != nil {
		f.Close()
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Rename(f.Name(), path)
}
