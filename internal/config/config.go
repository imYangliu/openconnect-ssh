package config

import (
	"crypto/sha256"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"go.yaml.in/yaml/v3"
)

const ManagedPath = "auto"

type Profile struct {
	AuthStateFile         string `yaml:"auth_state_file,omitempty"`
	Protocol              string `yaml:"protocol"`
	Server                string `yaml:"server"`
	Username              string `yaml:"username,omitempty"`
	PasswordEnv           string `yaml:"password_env,omitempty"`
	CookieEnv             string `yaml:"cookie_env,omitempty"`
	AuthGroup             string `yaml:"auth_group,omitempty"`
	LoginDomain           string `yaml:"login_domain,omitempty"`
	AuthType              string `yaml:"auth_type,omitempty"`
	Insecure              bool   `yaml:"insecure,omitempty"`
	TunnelServerName      string `yaml:"tunnel_server_name,omitempty"`
	TunnelCertificateFile string `yaml:"tunnel_certificate_file,omitempty"`
}
type Rule struct {
	Domain       string `yaml:"domain"`
	DomainSuffix string `yaml:"domain_suffix"`
	CIDR         string `yaml:"cidr"`
	Via          string `yaml:"via"`
}
type Policy struct {
	Rules []Rule `yaml:"rules"`
}
type Listener struct {
	Listen string `yaml:"listen"`
	Via    string `yaml:"via"`
}
type Config struct {
	Socket    string                `yaml:"socket"`
	Profiles  map[string]Profile    `yaml:"profiles"`
	Policies  map[string]Policy     `yaml:"policies"`
	Listeners map[string]Listener   `yaml:"listeners"`
	SSH       map[string]SSHProfile `yaml:"ssh,omitempty"`
	Forwards  map[string]Forward    `yaml:"forwards,omitempty"`
}

func DefaultPath() string {
	return filepath.Join(os.Getenv("HOME"), ".config", "och", "config.yaml")
}

func ManagedAuthStatePath(home, name string, p Profile) string {
	identity := strings.Join([]string{name, p.Protocol, p.Server, p.LoginDomain, p.AuthType, p.AuthGroup, p.Username}, "\x00")
	key := sha256.Sum256([]byte(identity))
	return filepath.Join(home, ".config", "och", "auth", fmt.Sprintf("%x.json", key[:16]))
}

func Load(path string) (Config, error) {
	var c Config
	file, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer file.Close()
	decoder := yaml.NewDecoder(file)
	decoder.KnownFields(true)
	var parsed *Config
	if err := decoder.Decode(&parsed); err != nil {
		return c, errors.New("invalid YAML configuration: check syntax, field names and value types")
	}
	if parsed == nil {
		return c, errors.New("YAML configuration must contain a mapping")
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return c, errors.New("configuration must contain exactly one YAML document")
	}
	c = *parsed
	if c.Socket == "" {
		c.Socket = filepath.Join(filepath.Dir(path), "run", "och.sock")
	}
	if !filepath.IsAbs(c.Socket) {
		return c, errors.New("socket must be an absolute path")
	}
	if len(c.Socket) > 100 {
		return c, errors.New("socket path is too long; configure a shorter absolute path")
	}
	for name, p := range c.Profiles {
		if p.AuthStateFile == ManagedPath || p.TunnelCertificateFile == ManagedPath {
			home, err := os.UserHomeDir()
			if err != nil || !filepath.IsAbs(home) {
				return c, fmt.Errorf("profile %s: managed paths require an absolute home directory", name)
			}
			managed := ManagedAuthStatePath(home, name, p)
			if p.AuthStateFile == ManagedPath {
				p.AuthStateFile = managed
			}
			if p.TunnelCertificateFile == ManagedPath {
				p.TunnelCertificateFile = managed + ".certificates.pem"
			}
		}
		if p.AuthStateFile != "" && !filepath.IsAbs(p.AuthStateFile) {
			return c, fmt.Errorf("profile %s: auth_state_file must be auto or an absolute path", name)
		}
		if name == "" || name == "direct" {
			return c, fmt.Errorf("invalid profile name %q", name)
		}
		if p.Protocol != "anyconnect" && p.Protocol != "atrust" {
			return c, fmt.Errorf("profile %s: protocol must be anyconnect or atrust", name)
		}
		if p.Server == "" {
			return c, fmt.Errorf("profile %s: server is required", name)
		}
		if p.TunnelServerName != "" && (p.Protocol != "atrust" || strings.ContainsAny(p.TunnelServerName, " /:@*\t\r\n\x00") || strings.TrimSpace(p.TunnelServerName) != p.TunnelServerName) {
			return c, fmt.Errorf("profile %s: tunnel_server_name requires an aTrust DNS hostname", name)
		}
		if p.TunnelCertificateFile != "" && (p.Protocol != "atrust" || p.Insecure || !filepath.IsAbs(p.TunnelCertificateFile)) {
			return c, fmt.Errorf("profile %s: tunnel_certificate_file requires aTrust, auto or an absolute path and insecure=false", name)
		}
		c.Profiles[name] = p
	}
	for name, p := range c.Policies {
		for i, r := range p.Rules {
			count := 0
			for _, s := range []string{r.Domain, r.DomainSuffix, r.CIDR} {
				if s != "" {
					count++
				}
			}
			if count != 1 {
				return c, fmt.Errorf("policy %s rule %d: set exactly one of domain, domain_suffix, cidr", name, i+1)
			}
			if err := c.ValidateVia(r.Via); err != nil {
				return c, fmt.Errorf("policy %s: %w", name, err)
			}
			if r.CIDR != "" {
				if _, err := netip.ParsePrefix(r.CIDR); err != nil {
					return c, fmt.Errorf("policy %s: %w", name, err)
				}
			}
			if strings.ContainsAny(r.Domain+r.DomainSuffix, " /:\t\r\n") {
				return c, fmt.Errorf("policy %s: invalid domain", name)
			}
		}
	}
	for name, l := range c.Listeners {
		if l.Via == "direct" {
			return c, fmt.Errorf("listener %s must bind a VPN profile", name)
		}
		if err := c.ValidateVia(l.Via); err != nil {
			return c, fmt.Errorf("listener %s: %w", name, err)
		}
		if err := validateEndpoint(l.Listen, true); err != nil {
			return c, fmt.Errorf("listener %s must use a loopback IP and numeric port 0-65535", name)
		}
		host, _, _ := net.SplitHostPort(l.Listen)
		ip := net.ParseIP(host)
		if ip == nil || !ip.IsLoopback() {
			return c, fmt.Errorf("listener %s must use a loopback IP address", name)
		}
	}
	if err := c.normalizeForwards(); err != nil {
		return c, err
	}
	return c, nil
}
func (c Config) ValidateVia(via string) error {
	if via == "direct" {
		return nil
	}
	if _, ok := c.Profiles[via]; !ok {
		return fmt.Errorf("unknown VPN profile %q", via)
	}
	return nil
}
