package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
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
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
	"golang.org/x/net/icmp"
	"golang.org/x/net/ipv4"
)

type cliPingSession struct {
	dial func(context.Context, string, string) (net.Conn, error)
}

func (s *cliPingSession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	return s.dial(ctx, network, address)
}
func (*cliPingSession) Close() error { return nil }

type cliPingConn struct{ net.Conn }

func (cliPingConn) RemoteAddr() net.Addr { return &net.IPAddr{IP: net.ParseIP("192.0.2.9")} }

func cliEchoPing(ctx context.Context, network, address string) (net.Conn, error) {
	if network != "ping" || address != "192.0.2.9" {
		return nil, errors.New("unexpected CLI ping destination")
	}
	client, server := net.Pipe()
	go func() {
		defer server.Close()
		packet := make([]byte, 1500)
		n, err := server.Read(packet)
		if err != nil {
			return
		}
		message, err := icmp.ParseMessage(1, packet[:n])
		if err != nil {
			return
		}
		message.Type = ipv4.ICMPTypeEchoReply
		reply, err := message.Marshal(nil)
		if err == nil {
			server.Write(reply)
		}
	}()
	return cliPingConn{client}, nil
}

func cliPingConfig(t *testing.T, dial func(context.Context, string, string) (net.Conn, error)) (config.Config, string) {
	t.Helper()
	dir, err := os.MkdirTemp("/tmp", "och-cli-ping-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	c := config.Config{Socket: filepath.Join(dir, "och.sock"), Profiles: map[string]config.Profile{"campus": {Protocol: "anyconnect", Server: "https://vpn.invalid"}}}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	ready := make(chan struct{})
	go func() {
		done <- daemon.Serve(ctx, c, func(context.Context, config.Profile) (vpn.Session, error) { return &cliPingSession{dial: dial}, nil }, func(map[string]string) { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(3 * time.Second):
			t.Error("CLI fixture daemon failed to stop")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatal(err)
	case <-time.After(3 * time.Second):
		t.Fatal("CLI fixture daemon failed to start")
	}
	path := filepath.Join(dir, "config.yaml")
	if err := os.WriteFile(path, []byte("socket: "+c.Socket+"\nprofiles:\n  campus:\n    protocol: anyconnect\n    server: https://vpn.invalid\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return c, path
}

func TestPingCLICompiled(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "och")
	build := exec.Command("go", "build", "-o", binary, "../../cmd/och")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v\n%s", err, output)
	}
	home := t.TempDir()
	command := func(args ...string) *exec.Cmd {
		cmd := exec.Command(binary, args...)
		for _, entry := range os.Environ() {
			if !strings.HasPrefix(entry, "HOME=") {
				cmd.Env = append(cmd.Env, entry)
			}
		}
		cmd.Env = append(cmd.Env, "HOME="+home)
		return cmd
	}
	t.Run("default four real replies", func(t *testing.T) {
		var probes atomic.Int32
		_, path := cliPingConfig(t, func(ctx context.Context, n, a string) (net.Conn, error) {
			probes.Add(1)
			return cliEchoPing(ctx, n, a)
		})
		output, err := command("ping", "--config", path, "--via", "campus", "--interval", "10ms", "192.0.2.9").CombinedOutput()
		if err != nil {
			t.Fatalf("ping CLI: %v\n%s", err, output)
		}
		body := string(output)
		if probes.Load() != 4 || strings.Count(body, "bytes from 192.0.2.9") != 4 || !strings.Contains(body, "4 probes attempted, 4 replies received, 0.0% unanswered") || !strings.Contains(body, "rtt min/avg/max") || strings.Contains(body, "ttl=") {
			t.Fatalf("wrong ping output: %s", body)
		}
	})
	t.Run("invalid options never reach VPN", func(t *testing.T) {
		_, path := cliPingConfig(t, func(context.Context, string, string) (net.Conn, error) {
			t.Error("invalid CLI options reached VPN")
			return nil, errors.New("unexpected dial")
		})
		for _, args := range [][]string{
			{"192.0.2.9"},
			{"--via", "direct", "192.0.2.9"},
			{"--via", "missing", "192.0.2.9"},
			{"--via", "campus", "--count", "0", "192.0.2.9"},
			{"--via", "campus", "--count", "65536", "192.0.2.9"},
			{"--via", "campus", "--interval", "1ms", "192.0.2.9"},
			{"--via", "campus", "--interval", "61s", "192.0.2.9"},
			{"--via", "campus", "--timeout", "0s", "192.0.2.9"},
			{"--via", "campus", "--timeout", "61s", "192.0.2.9"},
			{"--via", "campus", "bad/host"},
			{"--via", "campus", "192.0.2.9", "extra"},
			{"--via", "campus", "192.0.2.9", "--count", "1"},
		} {
			full := append([]string{"ping", "--config", path}, args...)
			output, err := command(full...).CombinedOutput()
			if err == nil || len(output) == 0 {
				t.Fatalf("invalid CLI succeeded: %q, %s", args, output)
			}
		}
	})
	t.Run("no replies exit nonzero", func(t *testing.T) {
		_, path := cliPingConfig(t, func(ctx context.Context, _, _ string) (net.Conn, error) {
			<-ctx.Done()
			return nil, ctx.Err()
		})
		output, err := command("ping", "--config", path, "--via", "campus", "--count", "2", "--interval", "10ms", "--timeout", "40ms", "192.0.2.9").CombinedOutput()
		if err == nil || !strings.Contains(string(output), "2 probes attempted, 0 replies received, 100.0% unanswered") || !strings.Contains(string(output), "ping received no replies") {
			t.Fatalf("missing failure summary: %v\n%s", err, output)
		}
	})
	t.Run("interrupt prints partial statistics", func(t *testing.T) {
		_, path := cliPingConfig(t, cliEchoPing)
		cmd := command("ping", "--config", path, "--via", "campus", "--count", "100", "--interval", "60s", "192.0.2.9")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() {
			cmd.Process.Kill()
			cmd.Wait()
		}()
		first := make(chan struct{}, 1)
		read := make(chan string, 1)
		go func() {
			var output strings.Builder
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				output.WriteString(line + "\n")
				if strings.Contains(line, "bytes from") {
					select {
					case first <- struct{}{}:
					default:
					}
				}
			}
			read <- output.String()
		}()
		select {
		case <-first:
		case <-time.After(3 * time.Second):
			t.Fatal("compiled CLI did not print first reply")
		}
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case output := <-read:
			err := cmd.Wait()
			if err != nil || !strings.Contains(output, "1 probes attempted, 1 replies received") || !strings.Contains(output, "rtt min/avg/max") {
				t.Fatalf("missing interrupted summary: %v\n%s\n%s", err, output, stderr.String())
			}
		case <-time.After(3 * time.Second):
			t.Fatal("compiled CLI ignored interrupt while waiting")
		}
	})
}

func TestPingPacesStartsWithoutTickerBurst(t *testing.T) {
	starts := make(chan time.Time, 3)
	var probes atomic.Int32
	c, _ := cliPingConfig(t, func(ctx context.Context, network, address string) (net.Conn, error) {
		starts <- time.Now()
		if probes.Add(1) == 1 {
			select {
			case <-time.After(45 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		return cliEchoPing(ctx, network, address)
	})
	var output bytes.Buffer
	err := runPing(context.Background(), c, pingOptions{via: "campus", args: []string{"192.0.2.9"}, count: 3, interval: 30 * time.Millisecond, timeout: time.Second}, &output)
	if err != nil {
		t.Fatal(err)
	}
	if len(starts) != 3 {
		t.Fatalf("expected three probe starts, got %d", len(starts))
	}
	<-starts
	second, third := <-starts, <-starts
	if third.Sub(second) < 25*time.Millisecond {
		t.Fatalf("probe starts accumulated a ticker burst: %s", third.Sub(second))
	}
}
