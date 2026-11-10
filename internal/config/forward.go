package config

import (
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type SSHProfile struct {
	Address          string `yaml:"address"`
	Username         string `yaml:"username"`
	Via              string `yaml:"via,omitempty"`
	IdentityFile     string `yaml:"identity_file,omitempty"`
	KnownHostsFile   string `yaml:"known_hosts_file,omitempty"`
	PasswordEnv      string `yaml:"password_env,omitempty"`
	KeyPassphraseEnv string `yaml:"key_passphrase_env,omitempty"`
}

type Forward struct {
	Direction string `yaml:"direction,omitempty"`
	Listen    string `yaml:"listen"`
	Target    string `yaml:"target"`
	SSH       string `yaml:"ssh,omitempty"`
	Via       string `yaml:"via,omitempty"`
}

func (f Forward) WithDefaults() Forward {
	if f.Direction == "" {
		f.Direction = "local"
	}
	for _, address := range []*string{&f.Listen, &f.Target} {
		if *address != "" && strings.Trim(*address, "0123456789") == "" {
			*address = net.JoinHostPort("127.0.0.1", *address)
		}
	}
	return f
}

func (c *Config) normalizeForwards() error {
	for name, p := range c.SSH {
		if name == "" {
			return errors.New("SSH profile name is required")
		}
		if err := validateEndpoint(p.Address, false); err != nil {
			return fmt.Errorf("SSH %s: address must be HOST:PORT with port 1-65535", name)
		}
		if strings.TrimSpace(p.Username) == "" || strings.ContainsAny(p.Username, "\x00\r\n") {
			return fmt.Errorf("SSH %s: username is required and must be single-line", name)
		}
		if p.Via == "" {
			p.Via = "direct"
		}
		if err := c.ValidateVia(p.Via); err != nil {
			return fmt.Errorf("SSH %s: %w", name, err)
		}
		if p.KnownHostsFile == "" {
			p.KnownHostsFile = "~/.ssh/known_hosts"
		}
		for _, path := range []*string{&p.IdentityFile, &p.KnownHostsFile} {
			if *path == "" {
				continue
			}
			if strings.HasPrefix(*path, "~/") {
				home, err := os.UserHomeDir()
				if err != nil || !filepath.IsAbs(home) {
					return fmt.Errorf("SSH %s: ~/ paths require an absolute home directory", name)
				}
				*path = filepath.Join(home, (*path)[2:])
			}
			if !filepath.IsAbs(*path) || strings.ContainsAny(*path, "\x00\r\n") {
				return fmt.Errorf("SSH %s: key and known-hosts paths must be absolute or start with ~/", name)
			}
		}
		if p.KeyPassphraseEnv != "" && p.IdentityFile == "" {
			return fmt.Errorf("SSH %s: key_passphrase_env requires identity_file", name)
		}
		c.SSH[name] = p
	}
	for name, f := range c.Forwards {
		if name == "" {
			return errors.New("forward name is required")
		}
		f = f.WithDefaults()
		if err := c.ValidateForward(f); err != nil {
			return fmt.Errorf("forward %s: %w", name, err)
		}
		c.Forwards[name] = f
	}
	return nil
}

func (c Config) ValidateForward(f Forward) error {
	f = f.WithDefaults()
	if f.Direction != "local" && f.Direction != "remote" {
		return errors.New("direction must be local or remote")
	}
	if (f.SSH == "") == (f.Via == "") {
		return errors.New("specify exactly one of ssh or via")
	}
	if f.SSH != "" {
		if _, ok := c.SSH[f.SSH]; !ok {
			return fmt.Errorf("unknown SSH profile %q", f.SSH)
		}
	} else if err := c.ValidateVia(f.Via); err != nil {
		return err
	}
	if f.Direction == "remote" && f.SSH == "" {
		return errors.New("remote forwarding requires an SSH profile")
	}
	if err := validateEndpoint(f.Listen, true); err != nil {
		return errors.New("listen must be PORT or loopback IP:PORT with port 0-65535")
	}
	host, _, _ := net.SplitHostPort(f.Listen)
	if ip := net.ParseIP(host); ip == nil || !ip.IsLoopback() {
		return errors.New("listen must use a loopback IP address")
	}
	if err := validateEndpoint(f.Target, false); err != nil {
		return errors.New("target must be PORT or HOST:PORT with port 1-65535")
	}
	return nil
}

func validateEndpoint(address string, allowZero bool) error {
	host, port, err := net.SplitHostPort(address)
	if err != nil || host == "" || strings.ContainsAny(host, " \t\r\n\x00/@") {
		return errors.New("invalid endpoint")
	}
	n, err := strconv.Atoi(port)
	if err != nil || n > 65535 || n < 0 || n == 0 && !allowZero {
		return errors.New("invalid port")
	}
	return nil
}
