package forward_test

import (
	"context"
	"net"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/diagnostic"
	"github.com/imyangliu/openconnect-ssh/internal/forward"
)

func probeCheck(report diagnostic.Report, stage, state, code string) bool {
	for _, check := range report.Checks {
		if check.Stage == stage && check.State == state && (code == "" || check.Code == code) {
			return true
		}
	}
	return false
}

func freeAddress(t *testing.T) string {
	t.Helper()
	l, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	address := l.Addr().String()
	l.Close()
	return address
}

func TestNativeDoctorProbesAndReleaseListeners(t *testing.T) {
	s := newSSHFixture(t, false)
	target := responseServer(t)
	_, port, _ := net.SplitHostPort(target)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	report := forward.ProbeSSH(ctx, s.profile, port, directDial)
	if !report.OK() || !probeCheck(report, "ssh", "ok", "") || !probeCheck(report, "target_tcp", "ok", "") {
		t.Fatalf("native SSH probe failed: %+v", report)
	}
	settings := config.Config{SSH: map[string]config.SSHProfile{"compute": s.profile}}
	for _, direction := range []string{"local", "remote"} {
		t.Run(direction, func(t *testing.T) {
			address := freeAddress(t)
			report := forward.ProbeForward(ctx, settings, config.Forward{Direction: direction, SSH: "compute", Listen: address, Target: port}, directDial)
			stage := "local_bind"
			if direction == "remote" {
				stage = "remote_bind"
			}
			if !report.OK() || !probeCheck(report, stage, "ok", "") || !probeCheck(report, "target_tcp", "ok", "") {
				t.Fatalf("forward probe failed: %+v", report)
			}
			l, err := net.Listen("tcp", address)
			if err != nil {
				t.Fatalf("probe did not release listener: %v", err)
			}
			l.Close()
		})
	}
	direct := forward.ProbeForward(ctx, settings, config.Forward{Via: "direct", Listen: "0", Target: port}, directDial)
	if !direct.OK() || !probeCheck(direct, "target_tcp", "ok", "") {
		t.Fatalf("direct probe failed %+v", direct)
	}
}

func TestNativeDoctorReportsHostKeyTargetAndBindFailures(t *testing.T) {
	s := newSSHFixture(t, false)
	settings := config.Config{SSH: map[string]config.SSHProfile{"compute": s.profile}}
	target := responseServer(t)
	busy := s.listener.Addr().String()
	before := s.logins.Load()
	local := forward.ProbeForward(context.Background(), settings, config.Forward{SSH: "compute", Listen: busy, Target: target}, directDial)
	if local.OK() || !probeCheck(local, "local_bind", "failed", "bind_failed") || s.logins.Load() != before {
		t.Fatalf("occupied local listener not isolated: %+v", local)
	}
	remote := forward.ProbeForward(context.Background(), settings, config.Forward{Direction: "remote", SSH: "compute", Listen: busy, Target: target}, directDial)
	if remote.OK() || !probeCheck(remote, "remote_bind", "failed", "remote_bind_rejected") {
		t.Fatalf("remote bind failure missing %+v", remote)
	}
	targetFailure := forward.ProbeSSH(context.Background(), s.profile, freeAddress(t), directDial)
	if targetFailure.OK() || !probeCheck(targetFailure, "ssh", "ok", "") || !probeCheck(targetFailure, "target_tcp", "failed", "") {
		t.Fatalf("target failure confused with authentication %+v", targetFailure)
	}
	p := s.profile
	p.KnownHostsFile = filepath.Join(t.TempDir(), "unknown_hosts")
	if err := os.WriteFile(p.KnownHostsFile, nil, 0600); err != nil {
		t.Fatal(err)
	}
	wrongKey := forward.ProbeSSH(context.Background(), p, "", directDial)
	if wrongKey.OK() || !probeCheck(wrongKey, "ssh", "failed", "ssh_failed") {
		t.Fatalf("host key was not checked %+v", wrongKey)
	}
	invalid := forward.ProbeSSH(context.Background(), s.profile, "0", func(context.Context, string, string) (net.Conn, error) {
		t.Fatal("invalid target dialed SSH")
		return nil, nil
	})
	if invalid.OK() || !probeCheck(invalid, "target_tcp", "failed", "invalid_target") {
		t.Fatalf("invalid target not rejected %+v", invalid)
	}
}

func TestNativeDoctorCanceledRemoteBind(t *testing.T) {
	s := newSSHFixture(t, true)
	settings := config.Config{SSH: map[string]config.SSHProfile{"compute": s.profile}}
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	report := forward.ProbeForward(ctx, settings, config.Forward{Direction: "remote", SSH: "compute", Listen: "0", Target: "8888"}, directDial)
	if report.OK() || !probeCheck(report, "remote_bind", "failed", "timeout") {
		t.Fatalf("remote bind timeout lost: %+v", report)
	}
	for _, check := range report.Checks {
		if check.Code == "timeout" && strings.Contains(check.Advice, "occupied") {
			t.Fatal("timeout mislabeled as occupied port")
		}
	}
}
