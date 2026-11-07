package config

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

func TestYAMLConfigStrictAndPrivateErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	invalid := []string{
		"", "# comment only\n", "null\n", "[]\n",
		"socket: /tmp/a\nsocket: /tmp/b\n",
		"policies: {}\n---\nprofiles: {}\n",
		"policies: {}\n---\n",
		"profiles:\n  campus:\n    protocol: anyconnect\n    server: https://vpn.invalid\n    typo: secret-fixture-value\n",
		"profiles:\n  campus:\n    protocol: anyconnect\n    server: https://vpn.invalid\n    insecure: secret-fixture-value\n",
		"socket: [secret-fixture-value\n",
	}
	for _, input := range invalid {
		if err := os.WriteFile(path, []byte(input), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil {
			t.Fatalf("accepted invalid YAML %q", input)
		}
		if strings.Contains(err.Error(), "secret-fixture-value") {
			t.Fatal("configuration value leaked in error")
		}
	}
}
func TestYAMLExampleAndRuleOrder(t *testing.T) {
	path, err := filepath.Abs("../../examples/config.yaml")
	if err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Profiles["campus"].Protocol != "anyconnect" || c.Profiles["lab"].Protocol != "atrust" {
		t.Fatal("profiles lost")
	}
	rules := c.Policies["campus-auto"].Rules
	if len(rules) != 3 || rules[0].DomainSuffix != "internal.example" || rules[1].CIDR != "10.20.0.0/16" || rules[2].Via != "direct" {
		t.Fatal("rule order changed")
	}
}

func TestDefaultConfigUsesYAML(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	if got := DefaultPath(); got != filepath.Join(home, ".config", "och", "config.yaml") {
		t.Fatalf("unexpected default path %s", got)
	}
}

func TestYAMLManagedProfilePaths(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.yaml")
	const body = `socket: /tmp/och-config-test.sock
profiles:
  ecnu: &atrust
    protocol: atrust
    server: https://vpn.example.edu
    username: fixture
    login_domain: example.edu
    auth_type: auth/cas
    auth_group: staff
    auth_state_file: auto
    tunnel_server_name: tunnel.example.edu
    tunnel_certificate_file: auto
  other:
    <<: *atrust
    auth_state_file: /private/custom-state.json
  campus:
    protocol: anyconnect
    server: https://anyconnect.example.edu
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	dir := filepath.Join(home, ".config", "och", "auth")
	state := filepath.Join(dir, "1841f61c2f788e57c57f92c3cf9bb30e.json")
	if p := c.Profiles["ecnu"]; p.AuthStateFile != state || p.TunnelCertificateFile != state+".certificates.pem" {
		t.Fatalf("managed paths changed existing cache naming: %+v", p)
	}
	if p := c.Profiles["other"]; p.AuthStateFile != "/private/custom-state.json" || p.TunnelCertificateFile != filepath.Join(dir, "ed0ea24f01bf3ed5b2eeb9b66d3d5b4c.json.certificates.pem") {
		t.Fatalf("explicit state path affected managed certificates: %+v", p)
	}
	if p := c.Profiles["campus"]; p.AuthStateFile != "" || p.TunnelCertificateFile != "" {
		t.Fatal("omitted paths changed existing trust or persistence")
	}
	if _, err := os.Stat(dir); !os.IsNotExist(err) {
		t.Fatal("loading configuration created state", err)
	}
}

func TestTunnelServerNameIsExplicitATrustOnly(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, tc := range []struct {
		protocol, name string
		valid          bool
	}{
		{"atrust", "vpn.example.edu", true},
		{"anyconnect", "vpn.example.edu", false},
		{"atrust", "https://vpn.example.edu", false},
		{"atrust", "vpn.example.edu:443", false},
		{"atrust", "*.example.edu", false},
	} {
		body := "socket: /tmp/och-config-test.sock\nprofiles:\n  campus:\n    protocol: " + tc.protocol + "\n    server: https://vpn.example.edu\n    tunnel_server_name: " + strconv.Quote(tc.name) + "\n"
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
}

func TestTunnelCertificateConfigurationRequiresVerifiedATrust(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, tc := range []struct {
		protocol, name, file string
		insecure, valid      bool
	}{
		{"atrust", "sdp.ssl.com", "/tmp/cert.pem", false, true},
		{"atrust", "", "/tmp/cert.pem", false, true},
		{"atrust", "sdp.ssl.com", "relative.pem", false, false},
		{"atrust", "sdp.ssl.com", "/tmp/cert.pem", true, false},
		{"anyconnect", "sdp.ssl.com", "/tmp/cert.pem", false, false},
		{"atrust", "sdp.ssl.com", "auto", false, true},
		{"atrust", "", "auto", false, true},
		{"atrust", "sdp.ssl.com", "auto", true, false},
		{"anyconnect", "", "auto", false, false},
	} {
		body := "socket: /tmp/och-cert-test.sock\nprofiles:\n  campus:\n    protocol: " + tc.protocol + "\n    server: https://vpn.example.edu\n    tunnel_server_name: " + strconv.Quote(tc.name) + "\n    tunnel_certificate_file: " + strconv.Quote(tc.file) + "\n    insecure: " + strconv.FormatBool(tc.insecure) + "\n"
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if (err == nil) != tc.valid {
			t.Fatal(tc, err)
		}
	}
}
