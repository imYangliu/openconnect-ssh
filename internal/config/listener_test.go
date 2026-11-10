package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestRejectInvalidMixedListenerPorts(t *testing.T) {
	for _, port := range []string{"65536", "-1", "", "http"} {
		t.Run(port, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "c.yaml")
			b := "socket: /tmp/och-review.sock\nprofiles:\n  test:\n    protocol: anyconnect\n    server: https://example.invalid\nlisteners:\n  test:\n    via: test\n    listen: \"127.0.0.1:" + port + "\"\n"
			if err := os.WriteFile(p, []byte(b), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p); err == nil {
				t.Fatalf("accepted invalid configured mixed listener port %q", port)
			}
		})
	}
}

func TestMixedListenerNumericPortBoundaries(t *testing.T) {
	for _, port := range []string{"0", "65535"} {
		t.Run(port, func(t *testing.T) {
			p := filepath.Join(t.TempDir(), "c.yaml")
			b := "socket: /tmp/och-listener-boundary.sock\nprofiles:\n  test:\n    protocol: anyconnect\n    server: https://example.invalid\nlisteners:\n  test:\n    via: test\n    listen: \"127.0.0.1:" + port + "\"\n"
			if err := os.WriteFile(p, []byte(b), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := Load(p); err != nil {
				t.Fatalf("valid numeric listener port %s rejected: %v", port, err)
			}
		})
	}
}
