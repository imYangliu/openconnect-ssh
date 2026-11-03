package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestExistingConfigRoundTrip(t *testing.T) {
	dir := t.TempDir()
	t.Setenv("OCH_CONFIG_FILE", filepath.Join(dir, "config.toml"))
	input := `[vpn]
host = "vpn.invalid"
user = "fixture"
[routes]
extra = ["10.0.0.1/32"]
[app]
language = "en"
[paths]
[proxy]
local_port = "1234"
`
	if e := writePrivate(configPath(), []byte(input)); e != nil {
		t.Fatal(e)
	}
	c, e := readConfig()
	if e != nil || c.Routes.Mode != "extra" || c.Proxy == nil || c.Proxy.LocalPort != "1234" {
		t.Fatal(c, e)
	}
	b, e := encode(c)
	if e != nil || !strings.Contains(string(b), "[vpn]") {
		t.Fatal(string(b), e)
	}
	if e := writePrivate(configPath(), append(b, []byte("\n[unknown]\nx=1\n")...)); e != nil {
		t.Fatal(e)
	}
	if _, e = readConfig(); e == nil {
		t.Fatal("accepted unknown config")
	}
}
func TestExistingSecrets(t *testing.T) {
	p := filepath.Join(t.TempDir(), "secrets.env")
	t.Setenv("OCH_SECRETS_FILE", p)
	for input, want := range map[string]string{`VPN_PASSWORD="a#b" # comment`: "a#b", "VPN_PASSWORD=plain # comment": "plain"} {
		if e := writePrivate(p, []byte(input)); e != nil {
			t.Fatal(e)
		}
		got, e := readSecret()
		if e != nil || got != want {
			t.Fatal("secret round trip failed")
		}
	}
	if e := os.Chmod(p, 0644); e != nil {
		t.Fatal(e)
	}
	if _, e := readSecret(); e == nil {
		t.Fatal("accepted public secret file")
	}
}
