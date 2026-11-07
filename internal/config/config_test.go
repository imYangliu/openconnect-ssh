package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestConfigRejectsUnsafeListenersAndUnknownFields(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "config.yaml")
	for _, body := range []string{"unknown: true", `profiles:
  campus:
    protocol: anyconnect
    server: https://vpn.invalid
listeners:
  public:
    listen: 0.0.0.0:1080
    via: campus`, `policies:
  auto:
    rules:
      - domain: example.com
        cidr: 10.0.0.0/8
        via: direct`} {
		os.WriteFile(path, []byte("socket: /tmp/och-test.sock\n"+body), 0600)
		if _, err := Load(path); err == nil {
			t.Fatal("unsafe config accepted", body)
		}
	}
}
