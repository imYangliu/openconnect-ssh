package forward

import (
	"context"
	"fmt"
	"net"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/diagnostic"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
)

func ProbeSSH(ctx context.Context, profile config.SSHProfile, target string, dial DialFunc) diagnostic.Report {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	report := diagnostic.Report{Version: 1, Source: "foreground"}
	if target != "" {
		target = (config.Forward{Target: target}).WithDefaults().Target
		host, port, err := net.SplitHostPort(target)
		if err != nil || routing.ValidateTarget(host, port) != nil {
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "failed", Code: "invalid_target", Message: "Target must be PORT or HOST:PORT with port 1-65535."})
			return report
		}
	}
	s, c := probeSession(ctx, profile, dial, &report)
	defer s.close()
	if c == nil {
		return report
	}
	if target != "" {
		probeSSHTarget(ctx, c, target, &report)
	}
	return report
}

func ProbeForward(ctx context.Context, settings config.Config, rule config.Forward, dial DialFunc) diagnostic.Report {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	report := diagnostic.Report{Version: 1, Source: "foreground"}
	rule = rule.WithDefaults()
	if err := settings.ValidateForward(rule); err != nil {
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "forward", State: "failed", Code: "invalid_rule", Message: err.Error()})
		return report
	}
	if rule.Direction == "local" {
		l, err := net.Listen("tcp", rule.Listen)
		if err != nil {
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "local_bind", State: "failed", Code: "bind_failed", Message: fmt.Sprintf("Cannot bind local listener %s: %v", rule.Listen, err), Advice: "Choose a free port or stop the listener you intended to replace; OCH does not stop other processes."})
			return report
		}
		address := l.Addr().String()
		l.Close()
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "local_bind", State: "ok", Message: fmt.Sprintf("Temporarily bound and released %s; this is not a port reservation.", address)})
	}
	if rule.SSH == "" {
		host, port, _ := net.SplitHostPort(rule.Target)
		decision, err := routing.New(settings, nil).Resolve(ctx, routing.Request{Host: host, Port: port, Via: rule.Via})
		if err != nil {
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "route", State: "failed", Code: "route", Message: err.Error()})
			return report
		}
		report.Route = &decision
		probeDirectTarget(ctx, dial, rule.Via, rule.Target, &report)
		return report
	}
	s, c := probeSession(ctx, settings.SSH[rule.SSH], dial, &report)
	defer s.close()
	if c == nil {
		return report
	}
	if rule.Direction == "remote" {
		l, err := sshOperation(ctx, c, func() (net.Listener, error) { return c.client.Listen("tcp", rule.Listen) })
		if err != nil {
			code := diagnostic.NetworkCode(err)
			message := "Remote listener check timed out or was canceled."
			advice := "Check SSH transport connectivity and the diagnostic timeout."
			if code == "connection_failed" {
				code = "remote_bind_rejected"
				message = "SSH server refused or could not establish the remote listener."
				advice = "The port may be occupied or SSH forwarding policy may deny the request."
			}
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "remote_bind", State: "failed", Code: code, Message: message, Advice: advice})
			return report
		}
		address := l.Addr().String()
		_, err = sshOperation(ctx, c, func() (bool, error) { return true, l.Close() })
		if err != nil {
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "remote_bind", State: "failed", Code: diagnostic.NetworkCode(err), Message: "Remote listener was bound but its release did not complete."})
			return report
		}
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "remote_bind", State: "ok", Message: fmt.Sprintf("Temporarily bound and released remote %s; this is not a port reservation.", address)})
		probeDirectTarget(ctx, dial, "direct", rule.Target, &report)
	} else {
		probeSSHTarget(ctx, c, rule.Target, &report)
	}
	return report
}

func probeSession(ctx context.Context, profile config.SSHProfile, dial DialFunc, report *diagnostic.Report) (*sshSession, *sshConnection) {
	transportFailed := false
	traced := func(ctx context.Context, via, address string) (net.Conn, error) {
		conn, err := dial(ctx, via, address)
		if err != nil {
			transportFailed = true
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "ssh_transport", State: "failed", Code: diagnostic.NetworkCode(err), Message: "Cannot connect to the SSH server through the configured transport.", Advice: "Check the VPN service, selected profile, DNS and SSH server TCP port."})
		} else {
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "ssh_transport", State: "ok", Message: fmt.Sprintf("Connected to SSH server %s via %s.", address, via)})
		}
		return conn, err
	}
	s := &sshSession{ctx: ctx, profile: profile, dial: traced}
	c, err := s.get(ctx)
	if err != nil {
		check := diagnostic.Check{Stage: "ssh", State: "failed", Code: "ssh_failed", Message: err.Error(), Advice: "Check known_hosts, SSH identity/agent/password settings and server authentication policy."}
		if ctx.Err() != nil {
			check.Code = diagnostic.NetworkCode(ctx.Err())
		}
		if transportFailed {
			check.State = "skipped"
			check.Code = "transport_failed"
			check.Message = "SSH authentication was not reached."
			check.Advice = ""
		}
		report.Checks = append(report.Checks, check)
		return s, nil
	}
	report.Checks = append(report.Checks, diagnostic.Check{Stage: "ssh", State: "ok", Message: "Native SSH handshake, host key verification and authentication succeeded."})
	return s, c
}

func probeSSHTarget(ctx context.Context, c *sshConnection, target string, report *diagnostic.Report) {
	conn, err := sshOperation(ctx, c, func() (net.Conn, error) { return c.client.Dial("tcp", target) })
	if err != nil {
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "failed", Code: diagnostic.NetworkCode(err), Message: fmt.Sprintf("SSH server could not connect to target %s.", target), Advice: "Check the target service, remote DNS and SSH forwarding permissions."})
		return
	}
	conn.Close()
	report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "ok", Message: fmt.Sprintf("SSH server connected to target %s; no application data was sent.", target)})
}

func probeDirectTarget(ctx context.Context, dial DialFunc, via, target string, report *diagnostic.Report) {
	conn, err := dial(ctx, via, target)
	if err != nil {
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "failed", Code: diagnostic.NetworkCode(err), Message: fmt.Sprintf("OCH could not connect to target %s via %s.", target, via), Advice: "Check target address, DNS and whether the service is listening."})
		return
	}
	conn.Close()
	report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "ok", Message: fmt.Sprintf("OCH connected to target %s via %s; no application data was sent.", target, via)})
}
