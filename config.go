package main

import (
	"bytes"
	"errors"
	"fmt"
	"net"
	"os"
	"path/filepath"
	"strings"

	"github.com/BurntSushi/toml"
)

type config struct {
	VPN struct {
		Host   string `toml:"host"`
		User   string `toml:"user"`
		Group  string `toml:"auth_group"`
		Helper string `toml:"helper,omitempty"`
	} `toml:"vpn"`
	SSH struct {
		Host   string `toml:"host"`
		User   string `toml:"user"`
		Port   string `toml:"port"`
		Target string `toml:"target_host"`
	} `toml:"ssh"`
	Routes struct {
		Mode  string   `toml:"mode"`
		Extra []string `toml:"extra"`
	} `toml:"routes"`
	DNS struct {
		Mode string `toml:"mode"`
	} `toml:"dns"`
	App struct {
		Language string `toml:"language"`
	} `toml:"app"`
	Paths struct{} `toml:"paths"`
	Proxy *struct {
		LocalHost  string `toml:"local_host"`
		LocalPort  string `toml:"local_port"`
		RemotePort string `toml:"remote_port"`
	} `toml:"proxy,omitempty"`
}

func env(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}
func configDir() string  { return filepath.Join(os.Getenv("HOME"), ".config", "och") }
func configPath() string { return env("OCH_CONFIG_FILE", filepath.Join(configDir(), "config.toml")) }
func secretPath() string { return env("OCH_SECRETS_FILE", filepath.Join(configDir(), "secrets.env")) }
func stateDir() string   { return filepath.Join(configDir(), "run") }
func readConfig() (config, error) {
	var c config
	c.SSH.Port = "22"
	c.SSH.User = os.Getenv("USER")
	c.Routes.Mode = "openconnect"
	c.DNS.Mode = "openconnect"
	c.App.Language = "system"
	m, e := toml.DecodeFile(configPath(), &c)
	if errors.Is(e, os.ErrNotExist) {
		return c, nil
	}
	if e != nil {
		return c, e
	}
	if k := m.Undecoded(); len(k) > 0 {
		return c, fmt.Errorf("未知配置字段 %s", k[0])
	}
	if len(c.Routes.Extra) > 0 && !m.IsDefined("routes", "mode") {
		c.Routes.Mode = "extra"
	}
	if c.Routes.Mode != "openconnect" && c.Routes.Mode != "extra" {
		return c, fmt.Errorf("无效 routes.mode")
	}
	if c.DNS.Mode != "openconnect" && c.DNS.Mode != "ignore" {
		return c, fmt.Errorf("无效 dns.mode")
	}
	if c.VPN.Helper != "" && !filepath.IsAbs(c.VPN.Helper) {
		return c, errors.New("vpn.helper 必须使用绝对路径")
	}
	switch c.App.Language {
	case "system", "en", "zh-Hans", "zh-Hant":
	default:
		return c, fmt.Errorf("无效 app.language")
	}
	for _, r := range c.Routes.Extra {
		if _, _, e := net.ParseCIDR(r); e != nil {
			return c, e
		}
	}
	return c, nil
}
func readSecret() (string, error) {
	p := secretPath()
	info, e := os.Stat(p)
	if errors.Is(e, os.ErrNotExist) {
		return "", nil
	}
	if e != nil {
		return "", e
	}
	if !info.Mode().IsRegular() || info.Mode().Perm() != 0600 {
		return "", fmt.Errorf("%s 权限必须为 0600", p)
	}
	data, e := os.ReadFile(p)
	if e != nil {
		return "", e
	}
	var password string
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, v, ok := strings.Cut(line, "=")
		if !ok || strings.TrimSpace(key) != "VPN_PASSWORD" {
			return "", errors.New("secrets.env 仅支持 VPN_PASSWORD")
		}
		v = strings.TrimSpace(v)
		if strings.HasPrefix(v, "'") || strings.HasPrefix(v, "\"") {
			var entry struct {
				Password string `toml:"VPN_PASSWORD"`
			}
			if _, e := toml.Decode(line, &entry); e != nil {
				return "", errors.New("secrets.env 密码格式无效")
			}
			password = entry.Password
		} else {
			v, _, _ = strings.Cut(v, "#")
			password = strings.TrimSpace(v)
		}
	}
	return password, nil
}
func encode(v any) ([]byte, error) {
	var b bytes.Buffer
	e := toml.NewEncoder(&b).Encode(v)
	return b.Bytes(), e
}
func writePrivate(path string, b []byte) error {
	dir := filepath.Dir(path)
	if e := os.MkdirAll(dir, 0700); e != nil {
		return e
	}
	f, e := os.CreateTemp(dir, ".och-*")
	if e != nil {
		return e
	}
	defer os.Remove(f.Name())
	_, e = f.Write(b)
	ce := f.Close()
	if e != nil {
		return e
	}
	if ce != nil {
		return ce
	}
	return os.Rename(f.Name(), path)
}
func (c config) checkVPN() error {
	if c.VPN.Host == "" || strings.HasPrefix(c.VPN.Host, "-") {
		return errors.New("未设置有效的 [vpn].host，请运行 och setup")
	}
	if c.VPN.User == "" {
		return errors.New("未设置 [vpn].user")
	}
	return nil
}
