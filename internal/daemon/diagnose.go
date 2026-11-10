package daemon

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/diagnostic"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

func Diagnose(ctx context.Context, c config.Config, request routing.Request) (diagnostic.Report, error) {
	budget := 10 * time.Second
	if end, ok := ctx.Deadline(); ok {
		budget = time.Until(end)
	}
	conn, reply, err := RequestControl(ctx, c, Request{Action: "diagnose", Request: request, Timeout: budget})
	if conn != nil {
		conn.Close()
	}
	if err != nil {
		return diagnostic.Report{}, err
	}
	if reply.Diagnostic == nil || reply.Diagnostic.Version != 1 || len(reply.Diagnostic.Checks) == 0 {
		return diagnostic.Report{}, errors.New("daemon does not support diagnostics; restart it with the current OCH build")
	}
	return *reply.Diagnostic, nil
}

func (d *daemon) diagnose(ctx context.Context, request Request) diagnostic.Report {
	budget := request.Timeout
	if budget <= 0 {
		budget = 10 * time.Second
	}
	if budget > 60*time.Second {
		budget = 60 * time.Second
	}
	ctx, cancel := context.WithTimeout(ctx, budget)
	defer cancel()
	report := diagnostic.Report{Version: 1, Source: "running_daemon"}
	decision, err := d.router.Resolve(ctx, request.Request)
	if err != nil {
		code := "route"
		var dns *routing.DNSError
		if errors.As(err, &dns) {
			code = diagnostic.NetworkCode(err)
		}
		if ctx.Err() != nil {
			code = diagnostic.NetworkCode(ctx.Err())
		}
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "route", State: "failed", Code: code, Message: err.Error(), Advice: "Check the requested policy, rule order and DNS configuration."})
		return report
	}
	report.Route = &decision
	report.Checks = append(report.Checks, diagnostic.Check{Stage: "route", State: "ok", Message: "Selected from the running daemon configuration."})
	report.Checks = append(report.Checks, diagnostic.Check{Stage: "dns", State: "info", Code: decision.DNS, Message: dnsDescription(decision.DNS)})
	if decision.Via != "direct" {
		if err = d.sessions.Ensure(ctx, decision.Via); err != nil {
			code, status := vpn.LoginFailureDetails(err)
			message := "VPN session initialization failed."
			advice := fmt.Sprintf("Check VPN %q and its service logs.", decision.Via)
			if code == "login_required" {
				message = "VPN authentication is missing or expired."
				advice = fmt.Sprintf("Run och login --via %q.", decision.Via)
			}
			if code == "unknown" {
				code = diagnostic.NetworkCode(err)
			}
			if status != 0 {
				message = fmt.Sprintf("VPN initialization failed with category %s and status %d.", code, status)
			}
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "vpn", State: "failed", Code: code, Message: message, Advice: advice})
			return report
		}
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "vpn", State: "ok", Message: "Selected VPN session initialized; target TCP is checked separately."})
	} else {
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "vpn", State: "skipped", Message: "The selected route is direct."})
	}
	target, err := d.sessions.Dial(ctx, decision.Via, decision.Address)
	if err != nil {
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "failed", Code: diagnostic.NetworkCode(err), Message: "Could not connect to the selected target TCP port.", Advice: "Check target address, DNS, access permissions and whether the application is listening."})
		return report
	}
	target.Close()
	report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "ok", Message: "Target TCP connection succeeded; no application data was sent."})
	return report
}

func dnsDescription(mode string) string {
	switch mode {
	case "literal":
		return "Target is an IP literal; no DNS lookup is needed."
	case "system":
		return "System DNS results used during rule selection pin the TCP target address."
	case "system_at_dial":
		return "System DNS is delegated to the direct TCP dial; it is not a separate DNS success check."
	default:
		return "DNS is delegated to the VPN backend; it is not independently verified here."
	}
}
