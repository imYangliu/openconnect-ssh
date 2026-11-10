package daemon

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/diagnostic"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

func hasDiagnostic(r diagnostic.Report, stage, state, code string) bool {
	for _, c := range r.Checks {
		if c.Stage == stage && c.State == state && (code == "" || c.Code == code) {
			return true
		}
	}
	return false
}

func TestDiagnoseSeparatesSessionAndTargetAndUsesLivePolicy(t *testing.T) {
	var initialized atomic.Int32
	live := config.Config{Policies: map[string]config.Policy{"auto": {Rules: []config.Rule{{CIDR: "127.0.0.0/8", Via: "campus"}}}}}
	c, _ := startDaemon(t, live, func(ctx context.Context, p config.Profile) (vpn.Session, error) {
		initialized.Add(1)
		return directFactory(ctx, p)
	})
	statusConn, status, err := RequestControl(context.Background(), c, Request{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	statusConn.Close()
	if initialized.Load() != 0 || status.Profiles["campus"] != "idle" {
		t.Fatalf("status initialized VPN: %d %+v", initialized.Load(), status)
	}
	target := echoTarget(t)
	host, port, _ := net.SplitHostPort(target)
	front := c
	front.Policies = map[string]config.Policy{"auto": {}}
	result, err := Diagnose(context.Background(), front, routing.Request{Host: host, Port: port, Policy: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if !result.OK() || result.Source != "running_daemon" || result.Route == nil || result.Route.Via != "campus" || result.Route.RuleIndex != 1 || !hasDiagnostic(result, "target_tcp", "ok", "") {
		t.Fatalf("wrong live diagnosis %+v", result)
	}
	if initialized.Load() != 1 {
		t.Fatal(initialized.Load())
	}
	unavailable, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	host, port, _ = net.SplitHostPort(unavailable.Addr().String())
	unavailable.Close()
	failed, err := Diagnose(context.Background(), c, routing.Request{Host: host, Port: port, Via: "campus"})
	if err != nil {
		t.Fatal(err)
	}
	if failed.OK() || !hasDiagnostic(failed, "vpn", "ok", "") || !hasDiagnostic(failed, "target_tcp", "failed", "connection_failed") {
		t.Fatalf("target failure confused with VPN: %+v", failed)
	}
	conn, status, err := RequestControl(context.Background(), c, Request{Action: "status"})
	if err != nil {
		t.Fatal(err)
	}
	conn.Close()
	if initialized.Load() != 1 || status.Profiles["campus"] != "ready" {
		t.Fatal("refused port invalidated healthy VPN")
	}
}

func TestDiagnoseAuthenticationAndTimeout(t *testing.T) {
	for _, tc := range []struct {
		name                string
		factory             vpn.Factory
		wantStage, wantCode string
	}{
		{"login-required", func(context.Context, config.Profile) (vpn.Session, error) { return nil, vpn.ErrLoginRequired }, "vpn", "login_required"},
		{"private-init-error", func(context.Context, config.Profile) (vpn.Session, error) {
			return nil, errors.New("private-fixture-secret")
		}, "vpn", "connection_failed"},
		{"timeout", func(context.Context, config.Profile) (vpn.Session, error) {
			return &testSession{dial: func(ctx context.Context, _, _ string) (net.Conn, error) { <-ctx.Done(); return nil, ctx.Err() }}, nil
		}, "target_tcp", "timeout"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			c, _ := startDaemon(t, config.Config{}, tc.factory)
			ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
			defer cancel()
			conn, reply, err := RequestControl(ctx, c, Request{Action: "diagnose", Request: routing.Request{Host: "127.0.0.1", Port: "22", Via: "campus"}, Timeout: 50 * time.Millisecond})
			if conn != nil {
				conn.Close()
			}
			if err != nil {
				t.Fatal(err)
			}
			if reply.Diagnostic == nil || !hasDiagnostic(*reply.Diagnostic, tc.wantStage, "failed", tc.wantCode) {
				t.Fatalf("wrong failure category: %+v", reply)
			}
			body, _ := json.Marshal(reply)
			if strings.Contains(string(body), "private-fixture-secret") {
				t.Fatal("initialization error leaked authentication details")
			}
		})
	}
}

func TestDiagnoseDNSFailureDoesNotInitializeVPN(t *testing.T) {
	original := net.DefaultResolver
	net.DefaultResolver = &net.Resolver{PreferGo: true, Dial: func(context.Context, string, string) (net.Conn, error) {
		return nil, errors.New("fixture DNS unavailable")
	}}
	t.Cleanup(func() { net.DefaultResolver = original })
	var init atomic.Int32
	c, _ := startDaemon(t, config.Config{Policies: map[string]config.Policy{"auto": {Rules: []config.Rule{{CIDR: "10.0.0.0/8", Via: "campus"}}}}}, func(context.Context, config.Profile) (vpn.Session, error) {
		init.Add(1)
		return nil, errors.New("unexpected init")
	})
	report, err := Diagnose(context.Background(), c, routing.Request{Host: "doctor-fixture.invalid.", Port: "22", Policy: "auto"})
	if err != nil {
		t.Fatal(err)
	}
	if !hasDiagnostic(report, "route", "failed", "dns") || init.Load() != 0 {
		t.Fatalf("DNS failure was not isolated: %+v", report)
	}
}

func TestDiagnoseRejectsOldOrEmptyProtocol(t *testing.T) {
	for _, body := range []string{"{\"error\":\"unknown action\"}\n", "{\"diagnostic\":{}}\n"} {
		t.Run(body, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "d.sock")
			if len(path) > 100 {
				dir = "/tmp"
				path = filepath.Join(dir, "och-doctor-old-"+time.Now().Format("150405.000000")+".sock")
			}
			listener, err := net.Listen("unix", path)
			if err != nil {
				t.Fatal(err)
			}
			defer listener.Close()
			done := make(chan struct{})
			go func() {
				defer close(done)
				conn, err := listener.Accept()
				if err != nil {
					return
				}
				defer conn.Close()
				var req Request
				json.NewDecoder(conn).Decode(&req)
				conn.Write([]byte(body))
			}()
			_, err = Diagnose(context.Background(), config.Config{Socket: path}, routing.Request{Host: "127.0.0.1", Port: "22", Via: "direct"})
			if err == nil {
				t.Fatal("accepted missing/old diagnostic protocol")
			}
			<-done
		})
	}
}
