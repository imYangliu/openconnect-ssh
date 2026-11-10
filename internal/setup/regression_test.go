package setup

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/discovery"
)

func TestSetupDefaultListenerAvoidsSavedListeners(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	address := availableListener()
	initial := fmt.Sprintf("socket: %q\nprofiles:\n  first:\n    protocol: anyconnect\n    server: https://first.invalid\nlisteners:\n  first:\n    listen: %q\n    via: first\n", filepath.Join(workspace(t), "och.sock"), address)
	if err := os.WriteFile(path, []byte(initial), 0600); err != nil {
		t.Fatal(err)
	}
	r := Runner{Home: home, Output: &bytes.Buffer{}, Discover: func(context.Context, string, string) (discovery.Result, error) {
		return discovery.Result{Protocol: "anyconnect", Server: "https://second.invalid"}, nil
	}}
	err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://second.invalid", Name: "second", Username: "test", SkipLogin: true, NoStart: true, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Listeners["first"].Listen == c.Listeners["second"].Listen {
		t.Fatalf("new profile reused configured listener %s", c.Listeners["second"].Listen)
	}
}

func TestSetupPreservesProfilesInheritedByYAMLMerge(t *testing.T) {
	home := t.TempDir()
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	original := []byte(fmt.Sprintf("socket: %q\n<<:\n  profiles:\n    first:\n      protocol: anyconnect\n      server: https://first.invalid\n", filepath.Join(workspace(t), "och.sock")))
	if err := os.WriteFile(path, original, 0600); err != nil {
		t.Fatal(err)
	}
	r := Runner{Home: home, Output: &bytes.Buffer{}, Discover: func(context.Context, string, string) (discovery.Result, error) {
		return discovery.Result{Protocol: "anyconnect", Server: "https://second.invalid"}, nil
	}}
	if err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://second.invalid", Name: "second", Username: "test", Listen: "127.0.0.1:1081", SkipLogin: true, NoStart: true, NonInteractive: true}); err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := c.Profiles["first"]; !ok {
		t.Fatal("setup silently deleted existing YAML-merged profile first")
	}
}

func TestSetupPreservesMergedMappingsAndAnchoredIdentities(t *testing.T) {
	for _, tc := range []struct{ name, body, profile string }{
		{"root-mappings", `<<:
  profiles:
    first:
      protocol: anyconnect
      server: https://first.invalid
      auth_state_file: auto
  listeners:
    first:
      listen: 127.0.0.1:1080
      via: first
`, "third"},
		{"map-merge", `profiles:
  first: &base
    protocol: anyconnect
    server: https://first.invalid
    username: old
    auth_state_file: auto
  second:
    <<: *base
    username: second
listeners:
  first: &listener
    listen: 127.0.0.1:1080
    via: first
  second:
    <<: *listener
    listen: 127.0.0.1:1082
    via: second
`, "third"},
		{"anchored-profile-overwrite", `profiles:
  first: &base
    protocol: anyconnect
    server: https://first.invalid
    username: old
    auth_state_file: auto
  second: *base
`, "first"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home := workspace(t)
			t.Setenv("HOME", home)
			path := filepath.Join(home, "config.yaml")
			original := fmt.Sprintf("# preserved header\nsocket: %q\n", filepath.Join(workspace(t), "och.sock")) + tc.body
			if err := os.WriteFile(path, []byte(original), 0600); err != nil {
				t.Fatal(err)
			}
			r := Runner{Home: home, Input: strings.NewReader("y\n"), Output: &bytes.Buffer{}, Discover: func(context.Context, string, string) (discovery.Result, error) {
				return discovery.Result{Protocol: "anyconnect", Server: "https://new.invalid"}, nil
			}}
			if err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://new.invalid", Name: tc.profile, Username: "new", Listen: "127.0.0.1:1081", SkipLogin: true, NoStart: true}); err != nil {
				t.Fatal(err)
			}
			c, err := config.Load(path)
			if err != nil {
				t.Fatal(err)
			}
			if tc.profile != "first" && c.Profiles["first"].Server != "https://first.invalid" {
				t.Fatalf("first profile lost: %+v", c.Profiles)
			}
			if tc.name != "root-mappings" && c.Profiles["second"].Server != "https://first.invalid" {
				t.Fatalf("shared anchored identity changed: %+v", c.Profiles["second"])
			}
			if tc.name != "anchored-profile-overwrite" && c.Listeners["first"].Listen != "127.0.0.1:1080" {
				t.Fatalf("inherited listener lost: %+v", c.Listeners)
			}
			raw, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			if !bytes.Contains(raw, []byte("# preserved header")) || bytes.Contains(raw, []byte(home)) {
				t.Fatalf("comments or auto paths rewritten: %s", raw)
			}
		})
	}
}

func TestSetupDefaultListenerUsesUnreservedEphemeralPort(t *testing.T) {
	var reserved []string
	for port := 1080; port < 1100; port++ {
		reserved = append(reserved, fmt.Sprintf("127.0.0.1:%d", port))
	}
	address := availableListener(reserved...)
	if err := validateListener(address); err != nil {
		t.Fatalf("no valid unreserved listener: %v", err)
	}
	for _, used := range reserved {
		if address == used {
			t.Fatalf("ephemeral allocation reused configured listener %s", address)
		}
	}
}
