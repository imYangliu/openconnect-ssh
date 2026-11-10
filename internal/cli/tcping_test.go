package cli

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"net"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
	"time"
)

func TestTCPingCLICompiled(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "och")
	if output, err := exec.Command("go", "build", "-o", binary, "../../cmd/och").CombinedOutput(); err != nil {
		t.Fatalf("build CLI %v\n%s", err, output)
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
	t.Run("defaults make four real TCP connections without bytes", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()
		var probes, closed atomic.Int32
		var wg sync.WaitGroup
		wg.Add(1)
		go func() {
			defer wg.Done()
			for {
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				probes.Add(1)
				conn.SetReadDeadline(time.Now().Add(time.Second))
				var b [1]byte
				n, err := conn.Read(b[:])
				conn.Close()
				if n == 0 && err == io.EOF {
					closed.Add(1)
				}
			}
		}()
		_, path := cliPingConfig(t, func(ctx context.Context, network, address string) (net.Conn, error) {
			if network != "tcp" || address != "example.test:443" {
				t.Errorf("wrong backend dial %s %s", network, address)
			}
			return (&net.Dialer{}).DialContext(ctx, "tcp", listener.Addr().String())
		})
		start := time.Now()
		output, err := command("tcping", "--config", path, "--via", "campus", "example.test", "443").CombinedOutput()
		listener.Close()
		wg.Wait()
		body := string(output)
		if err != nil || probes.Load() != 4 || closed.Load() != 4 || strings.Count(body, "connected to example.test:443") != 4 || !strings.Contains(body, "4 probes attempted, 4 connections succeeded, 0.0% failed") || !strings.Contains(body, "DNS+connect min/avg/max") || time.Since(start) < 2800*time.Millisecond {
			t.Fatalf("wrong default CLI result %v %d/%d\n%s", err, probes.Load(), closed.Load(), body)
		}
	})
	t.Run("invalid inputs never dial", func(t *testing.T) {
		_, path := cliPingConfig(t, func(context.Context, string, string) (net.Conn, error) {
			t.Error("invalid command dialed")
			return nil, errors.New("unexpected dial")
		})
		for _, args := range [][]string{
			{"example.test", "443"}, {"--via", "direct", "example.test", "443"}, {"--via", "missing", "example.test", "443"},
			{"--via", "campus", "example.test"}, {"--via", "campus", "example.test", "443", "extra"}, {"--via", "campus", "bad/host", "443"},
			{"--via", "campus", "example.test", "http"}, {"--via", "campus", "example.test", "65536"},
			{"--via", "campus", "--count", "0", "example.test", "443"}, {"--via", "campus", "--count", "65536", "example.test", "443"},
			{"--via", "campus", "--interval", "1ms", "example.test", "443"}, {"--via", "campus", "--interval", "61s", "example.test", "443"},
			{"--via", "campus", "--timeout", "0s", "example.test", "443"}, {"--via", "campus", "--timeout", "61s", "example.test", "443"},
			{"--via", "campus", "--policy", "auto", "example.test", "443"}, {"--via", "campus", "example.test", "443", "--count", "1"},
		} {
			output, err := command(append([]string{"tcping", "--config", path}, args...)...).CombinedOutput()
			if err == nil || len(output) == 0 {
				t.Fatalf("invalid command succeeded %q\n%s", args, output)
			}
		}
	})
	t.Run("failures exit nonzero and sanitize errors", func(t *testing.T) {
		_, path := cliPingConfig(t, func(context.Context, string, string) (net.Conn, error) {
			return nil, errors.New("Cookie=private-secret https://private.invalid/?ticket=private-secret")
		})
		output, err := command("tcping", "--config", path, "--via", "campus", "--count", "2", "--interval", "10ms", "example.test", "443").CombinedOutput()
		body := string(output)
		if err == nil || !strings.Contains(body, "2 probes attempted, 0 connections succeeded, 100.0% failed") || !strings.Contains(body, "tcping established no connections") || strings.Contains(body, "private") {
			t.Fatalf("wrong failure result %v\n%s", err, body)
		}
	})
	t.Run("interrupt prints partial statistics", func(t *testing.T) {
		_, path := cliPingConfig(t, func(context.Context, string, string) (net.Conn, error) { c, s := net.Pipe(); s.Close(); return c, nil })
		cmd := command("tcping", "--config", path, "--via", "campus", "--count", "100", "--interval", "60s", "example.test", "443")
		stdout, err := cmd.StdoutPipe()
		if err != nil {
			t.Fatal(err)
		}
		var stderr bytes.Buffer
		cmd.Stderr = &stderr
		if err := cmd.Start(); err != nil {
			t.Fatal(err)
		}
		defer func() { cmd.Process.Kill(); cmd.Wait() }()
		first, done := make(chan struct{}, 1), make(chan string, 1)
		go func() {
			var output strings.Builder
			scanner := bufio.NewScanner(stdout)
			for scanner.Scan() {
				line := scanner.Text()
				output.WriteString(line + "\n")
				if strings.Contains(line, "connected to") {
					select {
					case first <- struct{}{}:
					default:
					}
				}
			}
			done <- output.String()
		}()
		select {
		case <-first:
		case <-time.After(3 * time.Second):
			t.Fatal("no first connection")
		}
		if err := cmd.Process.Signal(os.Interrupt); err != nil {
			t.Fatal(err)
		}
		select {
		case output := <-done:
			if err := cmd.Wait(); err != nil || !strings.Contains(output, "1 probes attempted, 1 connections succeeded") || !strings.Contains(output, "DNS+connect min/avg/max") {
				t.Fatalf("wrong interrupted result %v\n%s\n%s", err, output, stderr.String())
			}
		case <-time.After(3 * time.Second):
			t.Fatal("CLI ignored interrupt")
		}
	})
}

func TestTCPingMixedOutcomesAndPacing(t *testing.T) {
	var probes atomic.Int32
	starts := make(chan time.Time, 3)
	c, _ := cliPingConfig(t, func(ctx context.Context, _, _ string) (net.Conn, error) {
		starts <- time.Now()
		n := probes.Add(1)
		if n == 1 {
			select {
			case <-time.After(45 * time.Millisecond):
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		if n == 2 {
			return nil, syscall.ECONNREFUSED
		}
		c, s := net.Pipe()
		s.Close()
		return c, nil
	})
	var output bytes.Buffer
	err := runTCPing(context.Background(), c, pingOptions{via: "campus", args: []string{"example.test", "443"}, count: 3, interval: 30 * time.Millisecond, timeout: time.Second}, &output)
	if err != nil || !strings.Contains(output.String(), "3 probes attempted, 2 connections succeeded, 33.3% failed") || !strings.Contains(output.String(), "tcp_seq=2: tcping connection refused") {
		t.Fatalf("wrong mixed result %v\n%s", err, output.String())
	}
	<-starts
	second, third := <-starts, <-starts
	if third.Sub(second) < 25*time.Millisecond {
		t.Fatalf("ticker burst %s", third.Sub(second))
	}
}
