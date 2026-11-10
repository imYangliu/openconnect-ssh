package forward_test

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
	"github.com/imyangliu/openconnect-ssh/internal/forward"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
	"go.yaml.in/yaml/v3"
)

type fixtureSession struct{ calls *atomic.Int32 }

func (s fixtureSession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	s.calls.Add(1)
	return (&net.Dialer{}).DialContext(ctx, network, address)
}
func (fixtureSession) Close() error { return nil }

func TestCompiledCLINativeForwardingThroughIPC(t *testing.T) {
	s := newSSHFixture(t, false)
	target := responseServer(t)
	_, targetPort, err := net.SplitHostPort(target)
	if err != nil {
		t.Fatal(err)
	}
	root, err := os.MkdirTemp("/tmp", "och-forward-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	binary := filepath.Join(root, "och")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/och")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	p := s.profile
	p.Via = "campus"
	c := config.Config{Socket: filepath.Join(root, "och.sock"),
		Profiles: map[string]config.Profile{"campus": {Protocol: "anyconnect", Server: "https://fixture.invalid"}},
		SSH:      map[string]config.SSHProfile{"compute": p},
		Forwards: map[string]config.Forward{
			"local":        {Direction: "local", SSH: "compute", Listen: "127.0.0.1:0", Target: target},
			"remote":       {Direction: "remote", SSH: "compute", Listen: "127.0.0.1:0", Target: target},
			"local-short":  {Direction: "local", SSH: "compute", Listen: "0", Target: targetPort},
			"remote-short": {Direction: "remote", SSH: "compute", Listen: "0", Target: targetPort},
		}}
	body, err := yaml.Marshal(c)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(root, "config.yaml")
	if err := os.WriteFile(path, body, 0600); err != nil {
		t.Fatal(err)
	}
	var calls atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		done <- daemon.Serve(ctx, c, func(context.Context, config.Profile) (vpn.Session, error) { return fixtureSession{&calls}, nil }, func(map[string]string) { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("fixture daemon failed to stop")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon startup: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon startup timed out")
	}
	for _, tc := range []struct {
		name       string
		args       []string
		readyNames []string
	}{
		{"saved", []string{"local", "remote"}, []string{"local", "remote"}},
		{"saved-short", []string{"local-short", "remote-short"}, []string{"local-short", "remote-short"}},
		{"local", []string{"--ssh", "compute", "127.0.0.1:0", target}, []string{"command"}},
		{"remote", []string{"--reverse", "--ssh", "compute", "127.0.0.1:0", target}, []string{"command"}},
		{"direct-vpn", []string{"--via", "campus", "127.0.0.1:0", target}, []string{"command"}},
		{"local-short", []string{"--ssh", "compute", "0", targetPort}, []string{"command"}},
		{"remote-short", []string{"--reverse", "--ssh", "compute", "0", targetPort}, []string{"command"}},
		{"direct-vpn-short", []string{"--via", "campus", "0", targetPort}, []string{"command"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			args := append([]string{"forward", "--config", path}, tc.args...)
			command := exec.Command(binary, args...)
			command.Env = []string{"HOME=" + home, "PATH=/nonexistent", "SSH_AUTH_SOCK="}
			var stdout bytes.Buffer
			command.Stdout = &stdout
			stderr, err := command.StderrPipe()
			if err != nil {
				t.Fatal(err)
			}
			if err := command.Start(); err != nil {
				t.Fatal(err)
			}
			events := make(chan forward.Event, 16)
			logs := make(chan string, 1)
			go func() {
				var lines []string
				scanner := bufio.NewScanner(stderr)
				for scanner.Scan() {
					line := scanner.Text()
					lines = append(lines, line)
					var name, state, address string
					if _, err := fmt.Sscanf(line, "forward %s %s %s", &name, &state, &address); err == nil {
						events <- forward.Event{Name: name, State: state, Address: address}
					}
				}
				logs <- strings.Join(lines, "\n")
			}()
			finished := make(chan struct{})
			var waitErr error
			go func() {
				waitErr = command.Wait()
				close(finished)
			}()
			t.Cleanup(func() {
				command.Process.Kill()
				select {
				case <-finished:
				case <-time.After(3 * time.Second):
				}
			})
			for _, name := range tc.readyNames {
				assertExchange(t, readyEvent(t, events, name))
			}
			if err := command.Process.Signal(os.Interrupt); err != nil {
				t.Fatal(err)
			}
			select {
			case <-finished:
				if waitErr != nil {
					t.Fatalf("forward exit: %v\n%s", waitErr, <-logs)
				}
			case <-time.After(3 * time.Second):
				t.Fatal("CLI failed to stop after interrupt")
			}
			if stdout.Len() != 0 {
				t.Fatalf("forward wrote to stdout: %q", stdout.String())
			}
		})
	}
	if calls.Load() != 8 {
		t.Fatalf("SSH transports and VPN forward made %d IPC-backed dials, want 8", calls.Load())
	}
	if s.logins.Load() != 6 {
		t.Fatalf("native SSH logins=%d, want 6", s.logins.Load())
	}
	if _, err := os.Stat(filepath.Join(home, ".ssh")); !os.IsNotExist(err) {
		t.Fatal("native forwarding modified personal SSH configuration")
	}
}
