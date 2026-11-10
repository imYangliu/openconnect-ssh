package forward_test

import (
	"context"
	"encoding/json"
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
	"github.com/imyangliu/openconnect-ssh/internal/diagnostic"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
	"go.yaml.in/yaml/v3"
)

type doctorSession struct{}

func (doctorSession) DialContext(ctx context.Context, network, address string) (net.Conn, error) {
	if address == "timeout.fixture:22" {
		<-ctx.Done()
		return nil, ctx.Err()
	}
	return (&net.Dialer{}).DialContext(ctx, network, address)
}
func (doctorSession) Close() error { return nil }

func TestCompiledCLIDoctorReportsActualChecks(t *testing.T) {
	s := newSSHFixture(t, false)
	target := responseServer(t)
	host, port, _ := net.SplitHostPort(target)
	root, err := os.MkdirTemp("/tmp", "och-doctor-cli-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(root) })
	binary := filepath.Join(root, "och")
	if body, err := exec.Command("go", "build", "-o", binary, "../../cmd/och").CombinedOutput(); err != nil {
		t.Fatalf("build CLI: %v %s", err, body)
	}
	home := filepath.Join(root, "home")
	if err := os.Mkdir(home, 0700); err != nil {
		t.Fatal(err)
	}
	p := s.profile
	p.Via = "campus"
	wrong := p
	wrong.KnownHostsFile = filepath.Join(root, "unknown_hosts")
	os.WriteFile(wrong.KnownHostsFile, nil, 0600)
	c := config.Config{Socket: filepath.Join(root, "och.sock"), Profiles: map[string]config.Profile{"campus": {Protocol: "anyconnect", Server: "https://fixture.invalid"}, "needs-login": {Protocol: "anyconnect", Server: "https://login.invalid"}},
		Policies: map[string]config.Policy{"auto": {Rules: []config.Rule{{CIDR: "127.0.0.0/8", Via: "campus"}}}},
		SSH:      map[string]config.SSHProfile{"compute": p, "wrong-key": wrong},
		Forwards: map[string]config.Forward{
			"local":       {SSH: "compute", Listen: "0", Target: port},
			"remote":      {Direction: "remote", SSH: "compute", Listen: "0", Target: port},
			"local-busy":  {SSH: "compute", Listen: s.listener.Addr().String(), Target: target},
			"remote-busy": {Direction: "remote", SSH: "compute", Listen: s.listener.Addr().String(), Target: target},
		}}
	front := c
	front.Policies = map[string]config.Policy{"auto": {}}
	path := filepath.Join(root, "config.yaml")
	save := func(path string, c config.Config) {
		t.Helper()
		body, err := yaml.Marshal(c)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, body, 0600); err != nil {
			t.Fatal(err)
		}
	}
	save(path, front)
	var initialized atomic.Int32
	ctx, cancel := context.WithCancel(context.Background())
	ready := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		done <- daemon.Serve(ctx, c, func(_ context.Context, p config.Profile) (vpn.Session, error) {
			initialized.Add(1)
			if p.Server == "https://login.invalid" {
				return nil, vpn.ErrLoginRequired
			}
			return doctorSession{}, nil
		}, func(map[string]string) { close(ready) })
	}()
	t.Cleanup(func() {
		cancel()
		select {
		case err := <-done:
			if err != nil {
				t.Error(err)
			}
		case <-time.After(5 * time.Second):
			t.Error("doctor fixture daemon did not stop")
		}
	})
	select {
	case <-ready:
	case err := <-done:
		t.Fatalf("daemon startup: %v", err)
	case <-time.After(3 * time.Second):
		t.Fatal("daemon startup timed out")
	}
	run := func(configPath string, wantOK bool, args ...string) diagnostic.Report {
		t.Helper()
		timeout, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		cmdArgs := append([]string{"doctor", "--config", configPath, "--json"}, args...)
		command := exec.CommandContext(timeout, binary, cmdArgs...)
		command.Env = []string{"HOME=" + home, "PATH=/nonexistent", "SSH_AUTH_SOCK="}
		var stderr strings.Builder
		command.Stderr = &stderr
		body, err := command.Output()
		var result struct {
			diagnostic.Report
			OK bool `json:"ok"`
		}
		if parseErr := json.Unmarshal(body, &result); parseErr != nil {
			t.Fatalf("invalid doctor JSON %q (%v): %v %s", body, parseErr, err, stderr.String())
		}
		if result.Version != 1 || result.OK != wantOK || result.Report.OK() != wantOK || (err == nil) != wantOK {
			t.Fatalf("doctor %v: exit=%v report=%s stderr=%s", args, err, body, stderr.String())
		}
		if strings.Contains(string(body), "private-fixture-secret") {
			t.Fatal("doctor printed raw private configuration")
		}
		return result.Report
	}
	bare := run(path, true)
	if initialized.Load() != 0 || bare.Profiles["campus"] != "idle" || probeCheck(bare, "target_tcp", "ok", "") {
		t.Fatalf("bare doctor performed active work %+v", bare)
	}
	for _, selector := range []string{"--via", "--policy"} {
		name := "campus"
		if selector == "--policy" {
			name = "auto"
		}
		result := run(path, true, selector, name, host, port)
		if result.Source != "running_daemon" || result.Route == nil || result.Route.Via != "campus" || !probeCheck(result, "vpn", "ok", "") || !probeCheck(result, "target_tcp", "ok", "") {
			t.Fatalf("not the live daemon path %+v", result)
		}
	}
	if initialized.Load() != 1 {
		t.Fatalf("doctor did not reuse VPN initialization: %d", initialized.Load())
	}
	loginRequired := run(path, false, "--via", "needs-login", host, port)
	if !probeCheck(loginRequired, "vpn", "failed", "login_required") {
		t.Fatalf("missing login diagnosis %+v", loginRequired)
	}
	for _, check := range loginRequired.Checks {
		if check.Code == "login_required" && (!strings.Contains(check.Advice, path) || !strings.Contains(check.Advice, "needs-login")) {
			t.Fatalf("login advice lost config/profile %+v", check)
		}
	}
	for _, args := range [][]string{{"--ssh", "compute"}, {"--ssh", "compute", port}, {"--forward", "local"}, {"--forward", "remote"}} {
		report := run(path, true, args...)
		if !probeCheck(report, "ssh", "ok", "") {
			t.Fatalf("native SSH not checked %+v", report)
		}
	}
	for _, tc := range []struct {
		args        []string
		stage, code string
	}{
		{[]string{"--ssh", "wrong-key"}, "ssh", "ssh_failed"},
		{[]string{"--forward", "local-busy"}, "local_bind", "bind_failed"},
		{[]string{"--forward", "remote-busy"}, "remote_bind", "remote_bind_rejected"},
	} {
		report := run(path, false, tc.args...)
		if !probeCheck(report, tc.stage, "failed", tc.code) {
			t.Fatalf("missing failure stage %+v", report)
		}
	}
	standalone := front
	standalone.Socket = filepath.Join(root, "missing.sock")
	standalonePath := filepath.Join(root, "standalone.yaml")
	save(standalonePath, standalone)
	direct := run(standalonePath, true, "--via", "direct", host, port)
	if !probeCheck(direct, "daemon", "skipped", "") {
		t.Fatalf("direct mode required daemon %+v", direct)
	}
	refused := freeAddress(t)
	closedHost, closedPort, _ := net.SplitHostPort(refused)
	refusedReport := run(path, false, "--via", "campus", closedHost, closedPort)
	if !probeCheck(refusedReport, "vpn", "ok", "") || !probeCheck(refusedReport, "target_tcp", "failed", "") {
		t.Fatalf("refused target mislabeled %+v", refusedReport)
	}
	timeoutReport := run(path, false, "--timeout", "100ms", "--via", "campus", "timeout.fixture", "22")
	timeoutSeen := false
	for _, check := range timeoutReport.Checks {
		if check.Code == "timeout" {
			timeoutSeen = true
		}
	}
	if !timeoutSeen {
		t.Fatalf("timeout category lost %+v", timeoutReport)
	}
	missing := run(standalonePath, false)
	if !probeCheck(missing, "daemon", "failed", "service_unavailable") {
		t.Fatalf("missing daemon not diagnosed %+v", missing)
	}
	invalidPath := filepath.Join(root, "invalid.yaml")
	os.WriteFile(invalidPath, []byte("profiles:\n  campus:\n    typo: private-fixture-secret\n"), 0600)
	if report := run(invalidPath, false); !probeCheck(report, "config", "failed", "invalid_config") {
		t.Fatalf("bad config not diagnosed %+v", report)
	}
	command := exec.Command(binary, "doctor", "--config", path)
	command.Env = []string{"HOME=" + home, "PATH=/nonexistent", "SSH_AUTH_SOCK="}
	body, err := command.CombinedOutput()
	if err != nil || !strings.Contains(string(body), "OCH doctor") || !strings.Contains(string(body), "observed state only") {
		t.Fatalf("human output: %v %s", err, body)
	}
	oldPath := filepath.Join(root, "old.sock")
	old, err := net.Listen("unix", oldPath)
	if err != nil {
		t.Fatal(err)
	}
	defer old.Close()
	oldDone := make(chan struct{})
	go func() {
		defer close(oldDone)
		for i := 0; i < 2; i++ {
			conn, err := old.Accept()
			if err != nil {
				return
			}
			var req daemon.Request
			json.NewDecoder(conn).Decode(&req)
			if req.Action == "status" {
				fmt.Fprintln(conn, `{}`)
			} else {
				fmt.Fprintln(conn, `{"error":"unknown action"}`)
			}
			conn.Close()
		}
	}()
	oldConfig := front
	oldConfig.Socket = oldPath
	oldConfigPath := filepath.Join(root, "old.yaml")
	save(oldConfigPath, oldConfig)
	legacy := run(oldConfigPath, false, "--policy", "auto", host, port)
	if !probeCheck(legacy, "daemon", "failed", "daemon_unsupported") {
		t.Fatalf("old daemon was not actionable %+v", legacy)
	}
	<-oldDone
	if _, err := os.Stat(filepath.Join(home, ".ssh")); !os.IsNotExist(err) {
		t.Fatal("doctor wrote personal SSH configuration")
	}
}
