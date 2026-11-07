package setup

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/discovery"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

func workspace(t *testing.T) string {
	t.Helper()
	p, e := os.MkdirTemp("/tmp", "och-setup-")
	if e != nil {
		t.Fatal(e)
	}
	t.Cleanup(func() { os.RemoveAll(p) })
	return p
}
func discovered(context.Context, string, string) (discovery.Result, error) {
	return discovery.Result{Server: "https://vpn.example.com", Protocol: "atrust", Detected: true, Methods: []discovery.Method{{Type: "auth/cas", Domain: "campus", Name: "SSO", Supported: true}, {Type: "auth/qrcode", Name: "QR"}}}, nil
}
func fixtureRunner(home string, output *bytes.Buffer) *Runner {
	return &Runner{Home: home, Executable: "/opt/och", Input: strings.NewReader(""), Output: output, Discover: discovered}
}
func TestSetupPreservesExistingConfigAndKeepsStateOutsideRepository(t *testing.T) {
	home := workspace(t)
	repo := filepath.Join(home, "repository")
	os.Mkdir(repo, 0700)
	path := filepath.Join(repo, "config.yaml")
	old := `# preserved comment
profiles:
  other:
    protocol: anyconnect
    server: https://other.invalid
policies:
  untouched:
    rules: []
listeners: null
`
	os.WriteFile(path, []byte(old), 0600)
	var output bytes.Buffer
	r := fixtureRunner(home, &output)
	r.Bootstrap = func(_ context.Context, p config.Profile, o vpn.BootstrapOptions) (config.Profile, error) {
		if !strings.HasPrefix(p.AuthStateFile, filepath.Join(home, ".config", "och", "auth")) {
			t.Fatal("state would be written into checkout", p.AuthStateFile)
		}
		return p, nil
	}
	err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://vpn.example.com", Name: "campus", Listen: "127.0.0.1:1080", NoStart: true, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	c, err := config.Load(path)
	if err != nil || len(c.Profiles) != 2 || len(c.Policies) != 1 || c.Listeners["campus"].Via != "campus" {
		t.Fatal(c, err)
	}
	b, _ := os.ReadFile(path)
	if !bytes.Contains(b, []byte("# preserved comment")) {
		t.Fatal("lost comment")
	}
	info, _ := os.Stat(path)
	if info.Mode().Perm() != 0600 {
		t.Fatal(info.Mode())
	}
}

func TestSetupWritesManagedPathsAndOmitsDefaultSocket(t *testing.T) {
	home := workspace(t)
	t.Setenv("HOME", home)
	path := filepath.Join(home, "config.yaml")
	var output bytes.Buffer
	r := fixtureRunner(home, &output)
	var bootstrapped config.Profile
	r.Bootstrap = func(_ context.Context, p config.Profile, _ vpn.BootstrapOptions) (config.Profile, error) {
		if !filepath.IsAbs(p.AuthStateFile) {
			t.Fatalf("bootstrap received unresolved state path %q", p.AuthStateFile)
		}
		p.TunnelCertificateFile = p.AuthStateFile + ".certificates.pem"
		bootstrapped = p
		return p, nil
	}
	if err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://vpn.example.com", Name: "campus", Listen: "127.0.0.1:1080", NoStart: true, NonInteractive: true}); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Contains(data, []byte("auth_state_file: auto\n")) || !bytes.Contains(data, []byte("tunnel_certificate_file: auto\n")) || bytes.Contains(data, []byte("tunnel_server_name:")) || bytes.Contains(data, []byte("socket:")) || bytes.Contains(data, []byte("password_env:")) || bytes.Contains(data, []byte(home)) {
		t.Fatalf("unexpected generated configuration:\n%s", data)
	}
	c, err := config.Load(path)
	if err != nil {
		t.Fatal(err)
	}
	if c.Profiles["campus"] != bootstrapped || c.Socket != filepath.Join(home, "run", "och.sock") {
		t.Fatal("generated configuration changed the resolved profile or default socket")
	}
}

func TestSetupRestoresAuthenticationBeforeReadyClaim(t *testing.T) {
	home := workspace(t)
	path := filepath.Join(home, "config.yaml")
	var output bytes.Buffer
	r := fixtureRunner(home, &output)
	stages := []string{}
	r.Bootstrap = func(_ context.Context, p config.Profile, o vpn.BootstrapOptions) (config.Profile, error) {
		stages = append(stages, "bootstrap")
		return p, nil
	}
	r.Start = func(_ context.Context, _, path string, c config.Config) error {
		if _, err := os.Stat(path); err != nil {
			t.Fatal("started before saving config")
		}
		stages = append(stages, "start")
		return nil
	}
	r.Ensure = func(context.Context, config.Config, string) error { stages = append(stages, "restore"); return nil }
	if err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://vpn.example.com", Name: "campus", Listen: "127.0.0.1:1080", NonInteractive: true}); err != nil {
		t.Fatal(err)
	}
	if strings.Join(stages, ",") != "bootstrap,start,restore" || !strings.Contains(output.String(), "VPN ready") {
		t.Fatal(stages, output.String())
	}
}
func TestSetupDoesNotClaimReadyWithoutRestoration(t *testing.T) {
	home := workspace(t)
	var output bytes.Buffer
	r := fixtureRunner(home, &output)
	r.Bootstrap = func(_ context.Context, p config.Profile, o vpn.BootstrapOptions) (config.Profile, error) {
		return p, nil
	}
	r.Start = func(context.Context, string, string, config.Config) error { return nil }
	err := r.Run(context.Background(), Options{ConfigPath: filepath.Join(home, "config.yaml"), Server: "https://vpn.example.com", Name: "campus", Listen: "127.0.0.1:1080", NonInteractive: true})
	if err == nil || strings.Contains(output.String(), "VPN ready") {
		t.Fatal("missing restore verifier", err, output.String())
	}
}
func TestSetupDetectsConcurrentConfigurationEdit(t *testing.T) {
	home := workspace(t)
	path := filepath.Join(home, "config.yaml")
	original := []byte("profiles: {}\n")
	os.WriteFile(path, original, 0600)
	var output bytes.Buffer
	r := fixtureRunner(home, &output)
	updated := []byte("# user changed this during login\nprofiles: {}\n")
	r.Bootstrap = func(_ context.Context, p config.Profile, o vpn.BootstrapOptions) (config.Profile, error) {
		os.WriteFile(path, updated, 0600)
		return p, nil
	}
	err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://vpn.example.com", Name: "campus", Listen: "127.0.0.1:1080", NoStart: true, NonInteractive: true})
	got, _ := os.ReadFile(path)
	if err == nil || !bytes.Equal(got, updated) {
		t.Fatal(err, string(got))
	}
}
func TestSetupCertificateTrustIsExplicitAndBrowserUsesChosenProfile(t *testing.T) {
	home := workspace(t)
	path := filepath.Join(home, "config.yaml")
	var output bytes.Buffer
	r := fixtureRunner(home, &output)
	r.Input = strings.NewReader("n\n")
	r.BrowserFor = func(name string, p config.Profile) vpn.CASCallback {
		if name != "chosen" || p.LoginDomain != "campus" {
			t.Fatal(name, p.LoginDomain)
		}
		return func(context.Context, string) (string, error) { return "", nil }
	}
	r.Bootstrap = func(ctx context.Context, p config.Profile, o vpn.BootstrapOptions) (config.Profile, error) {
		_, err := o.TrustCertificates(ctx, []vpn.CertificateInfo{{SHA256: "fixture", Subject: "CN=private", DNSNames: []string{"sdp.example"}, NotAfter: time.Now().Add(time.Hour)}}, "sdp.example")
		if err == nil {
			t.Fatal("accepted trust without yes")
		}
		return p, err
	}
	err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://vpn.example.com", Name: "chosen", Listen: "127.0.0.1:1080", NoStart: true})
	if err == nil {
		t.Fatal("declined trust succeeded")
	}
	if _, err = os.Stat(path); !os.IsNotExist(err) {
		t.Fatal("saved rejected profile")
	}
}

func TestSetupPromptsForTunnelNameOnlyWithoutSuggestion(t *testing.T) {
	certificates := []vpn.CertificateInfo{{SHA256: "fixture", DNSNames: []string{"sdp.example"}}}
	for _, tc := range []struct {
		name, suggestion, input string
		prompt                  bool
	}{
		{"known name", "sdp.example", "y\n", false},
		{"manual name", "", "y\nsdp.example\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var output bytes.Buffer
			ctx := context.Background()
			r := Runner{Output: &output, context: ctx, reader: strings.NewReader(tc.input)}
			name, err := r.trustCertificates(ctx, certificates, tc.suggestion)
			if name != "sdp.example" || err != nil {
				t.Fatalf("name = %q, error = %v", name, err)
			}
			if !strings.Contains(output.String(), "Trust exactly these certificates") || strings.Contains(output.String(), "Tunnel TLS server name") != tc.prompt {
				t.Fatalf("unexpected prompts: %s", &output)
			}
		})
	}
}
func TestDraftSkipsAuthenticationAndRetainsNullMappings(t *testing.T) {
	home := workspace(t)
	path := filepath.Join(home, "config.yaml")
	os.WriteFile(path, []byte("profiles: null\nlisteners: null\n"), 0600)
	var output bytes.Buffer
	r := fixtureRunner(home, &output)
	r.Bootstrap = func(context.Context, config.Profile, vpn.BootstrapOptions) (config.Profile, error) {
		t.Fatal("draft authenticated")
		return config.Profile{}, nil
	}
	err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://vpn.example.com", Name: "campus", Listen: "127.0.0.1:1080", SkipLogin: true, NoStart: true, NonInteractive: true})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = config.Load(path); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(output.String(), "Draft only") {
		t.Fatal(output.String())
	}
}
func TestSetupRejectsExistingProfileWithoutInteractiveConfirmation(t *testing.T) {
	home := workspace(t)
	path := filepath.Join(home, "config.yaml")
	body := []byte("profiles:\n  campus:\n    protocol: atrust\n    server: https://vpn.example.com\n")
	os.WriteFile(path, body, 0600)
	var output bytes.Buffer
	r := fixtureRunner(home, &output)
	err := r.Run(context.Background(), Options{ConfigPath: path, Server: "https://vpn.example.com", Name: "campus", Listen: "127.0.0.1:1080", SkipLogin: true, NoStart: true, NonInteractive: true})
	got, _ := os.ReadFile(path)
	if err == nil || !bytes.Equal(got, body) {
		t.Fatal(err, string(got))
	}
}
func TestSSHConfigPreservesExistingAndDoesNotOverwriteAlias(t *testing.T) {
	home := workspace(t)
	dir := filepath.Join(home, ".ssh")
	os.Mkdir(dir, 0700)
	old := []byte("Host personal\n  HostName personal.invalid\n")
	os.WriteFile(filepath.Join(dir, "config"), old, 0600)
	err := WriteSSH(home, "/path with spaces/och", "/path/config.yaml", "campus", "campus-server", "server.internal.example", "student", "22")
	if err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "config"))
	if !bytes.HasSuffix(b, old) || !bytes.HasPrefix(b, []byte("Include ~/.ssh/och.d/*.config\n")) {
		t.Fatal(string(b))
	}
	if err = WriteSSH(home, "/path/och", "/path/config.yaml", "campus", "personal", "other.example", "student", "22"); err == nil {
		t.Fatal("overwrote existing alias")
	}
}
func TestSetupInputCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	_, err := readLine(ctx, strings.NewReader("anything\n"))
	if !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}

func TestInvalidListenerFailsBeforeLogin(t *testing.T) {
	for _, address := range []string{"127.0.0.1:108O", "127.0.0.1:99999", "127.0.0.1:0", "0.0.0.0:1080"} {
		home := workspace(t)
		var output bytes.Buffer
		r := fixtureRunner(home, &output)
		r.Bootstrap = func(context.Context, config.Profile, vpn.BootstrapOptions) (config.Profile, error) {
			t.Fatal("invalid listener reached login")
			return config.Profile{}, nil
		}
		err := r.Run(context.Background(), Options{ConfigPath: filepath.Join(home, "config.yaml"), Server: "https://vpn.example.com", Name: "campus", Listen: address, NoStart: true, NonInteractive: true})
		if err == nil {
			t.Fatal("accepted invalid listener", address)
		}
	}
}
func TestAuthenticationGroupUsesSeparateStateBinding(t *testing.T) {
	home := workspace(t)
	var paths []string
	for i, group := range []string{"students", "staff"} {
		var output bytes.Buffer
		r := fixtureRunner(home, &output)
		r.Discover = func(context.Context, string, string) (discovery.Result, error) {
			return discovery.Result{Server: "https://vpn.example.com", Protocol: "anyconnect"}, nil
		}
		r.Bootstrap = func(_ context.Context, p config.Profile, o vpn.BootstrapOptions) (config.Profile, error) {
			paths = append(paths, p.AuthStateFile)
			return p, nil
		}
		err := r.Run(context.Background(), Options{ConfigPath: filepath.Join(home, fmt.Sprintf("config%d.yaml", i)), Server: "https://vpn.example.com", Name: "campus", Username: "fixture", AuthGroup: group, Listen: "127.0.0.1:1080", NoStart: true, NonInteractive: true})
		if err != nil {
			t.Fatal(err)
		}
	}
	if paths[0] == paths[1] {
		t.Fatal("different groups share state binding")
	}
}
