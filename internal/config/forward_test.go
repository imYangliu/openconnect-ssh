package config

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestNativeSSHAndForwardConfiguration(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(t.TempDir(), "config.yaml")
	body := `socket: /tmp/och-forward-config-test.sock
ssh:
  compute:
    address: compute.internal:22
    username: student
    identity_file: ~/.ssh/id_ed25519
forwards:
  notebook:
    ssh: compute
    listen: 8888
    target: "8888"
  callback:
    direction: remote
    ssh: compute
    listen: "[::1]:9000"
    target: localhost:3000
`
	if err := os.WriteFile(path, []byte(body), 0600); err != nil {
		t.Fatal(err)
	}
	c, err := Load(path)
	if err != nil {
		t.Fatal(err)
	}
	p := c.SSH["compute"]
	if p.Via != "direct" || p.KnownHostsFile != filepath.Join(home, ".ssh", "known_hosts") || p.IdentityFile != filepath.Join(home, ".ssh", "id_ed25519") {
		t.Fatalf("SSH defaults/paths incorrect: %+v", p)
	}
	if c.Forwards["notebook"].Direction != "local" || c.Forwards["callback"].Direction != "remote" {
		t.Fatal("directions not preserved")
	}
	if f := c.Forwards["notebook"]; f.Listen != "127.0.0.1:8888" || f.Target != "127.0.0.1:8888" {
		t.Fatalf("port-only YAML endpoints not expanded: %+v", f)
	}
}

func TestForwardLoopbackDefaults(t *testing.T) {
	for _, tc := range []struct {
		listen, target, wantListen, wantTarget string
	}{
		{"0", "8888", "127.0.0.1:0", "127.0.0.1:8888"},
		{"65535", "65535", "127.0.0.1:65535", "127.0.0.1:65535"},
		{"[::1]:9000", "localhost:3000", "[::1]:9000", "localhost:3000"},
	} {
		f := Forward{SSH: "compute", Listen: tc.listen, Target: tc.target}.WithDefaults()
		if f.Listen != tc.wantListen || f.Target != tc.wantTarget || f.WithDefaults() != f {
			t.Fatalf("incorrect or non-idempotent defaults: %+v", f)
		}
		if err := (Config{SSH: map[string]SSHProfile{"compute": {}}}).ValidateForward(f); err != nil {
			t.Fatal(err)
		}
	}
}

func TestRejectInvalidNativeForwards(t *testing.T) {
	base := Config{SSH: map[string]SSHProfile{"compute": {}}}
	valid := Forward{SSH: "compute", Listen: "127.0.0.1:0", Target: "localhost:8888"}
	for _, tc := range []struct {
		name   string
		mutate func(*Forward)
	}{
		{"mode", func(f *Forward) { f.Direction = "sideways" }},
		{"conflict", func(f *Forward) { f.Via = "direct" }},
		{"missing-path", func(f *Forward) { f.SSH = "" }},
		{"ssh-reference", func(f *Forward) { f.SSH = "absent" }},
		{"vpn-reference", func(f *Forward) { f.SSH = ""; f.Via = "absent" }},
		{"remote-direct", func(f *Forward) { f.Direction = "remote"; f.SSH = ""; f.Via = "direct" }},
		{"public-local", func(f *Forward) { f.Listen = "0.0.0.0:8888" }},
		{"public-remote", func(f *Forward) { f.Direction = "remote"; f.Listen = "0.0.0.0:8888" }},
		{"hostname-bind", func(f *Forward) { f.Listen = "localhost:8888" }},
		{"listen-port", func(f *Forward) { f.Listen = "127.0.0.1:65536" }},
		{"bare-listen-port", func(f *Forward) { f.Listen = "65536" }},
		{"bare-negative-listen", func(f *Forward) { f.Listen = "-1" }},
		{"empty-listen", func(f *Forward) { f.Listen = "" }},
		{"empty-listen-host", func(f *Forward) { f.Listen = ":8888" }},
		{"target-port", func(f *Forward) { f.Target = "host:0" }},
		{"bare-target-zero", func(f *Forward) { f.Target = "0" }},
		{"bare-target-port", func(f *Forward) { f.Target = "65536" }},
		{"bare-target-negative", func(f *Forward) { f.Target = "-1" }},
		{"bare-target-space", func(f *Forward) { f.Target = " 8888" }},
		{"empty-target", func(f *Forward) { f.Target = "" }},
		{"empty-target-host", func(f *Forward) { f.Target = ":8888" }},
		{"target-control", func(f *Forward) { f.Target = "bad\nhost:80" }},
	} {
		t.Run(tc.name, func(t *testing.T) {
			f := valid
			tc.mutate(&f)
			if err := base.ValidateForward(f); err == nil {
				t.Fatal("invalid forwarding accepted")
			}
		})
	}
}

func TestRejectInvalidSSHProfilesAndPrivateYAMLErrors(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.yaml")
	for _, fields := range []string{
		"address: host:0\n    username: user",
		"address: host:22",
		"address: host:22\n    username: user\n    via: absent",
		"address: host:22\n    username: user\n    identity_file: relative/key",
		"address: host:22\n    username: user\n    key_passphrase_env: TEST",
		"address: host:22\n    username: user\n    typo: private-fixture-secret",
	} {
		body := fmt.Sprintf("socket: /tmp/och-forward-config-test.sock\nssh:\n  compute:\n    %s\n", fields)
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		_, err := Load(path)
		if err == nil {
			t.Fatalf("accepted invalid SSH fields %s", fields)
		}
		if strings.Contains(err.Error(), "private-fixture-secret") {
			t.Fatal("YAML error exposed secret")
		}
	}
}
