package cli

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
	"github.com/imyangliu/openconnect-ssh/internal/diagnostic"
	"github.com/imyangliu/openconnect-ssh/internal/forward"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
)

type doctorOptions struct {
	via, policy, ssh, forward string
	json                      bool
	timeout                   time.Duration
	args                      []string
}

func runDoctor(ctx context.Context, path string, o doctorOptions) error {
	count := 0
	for _, selector := range []string{o.via, o.policy, o.ssh, o.forward} {
		if selector != "" {
			count++
		}
	}
	if count > 1 {
		return errors.New("doctor accepts exactly one of --via, --policy, --ssh or --forward")
	}
	if o.timeout <= 0 {
		return errors.New("doctor --timeout must be positive")
	}
	switch {
	case o.via != "" || o.policy != "":
		if len(o.args) != 2 {
			return errors.New("doctor --via/--policy requires HOST PORT")
		}
		if err := routing.ValidateTarget(strings.Trim(o.args[0], "[]"), o.args[1]); err != nil {
			return err
		}
	case o.ssh != "":
		if len(o.args) > 1 {
			return errors.New("doctor --ssh accepts at most one TARGET, as PORT or HOST:PORT")
		}
	default:
		if len(o.args) != 0 {
			return errors.New("doctor uses --via, --policy, --ssh or --forward to select a target")
		}
	}
	ctx, cancel := context.WithTimeout(ctx, o.timeout)
	defer cancel()
	report := diagnostic.Report{Version: 1, Source: "foreground"}
	c, err := config.Load(path)
	if err != nil {
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "config", State: "failed", Code: "invalid_config", Message: err.Error(), Advice: "Check the selected YAML file, field names and references."})
		return printDoctor(report, o.json)
	}
	report.Checks = append(report.Checks, diagnostic.Check{Stage: "config", State: "ok", Message: "Local YAML parsed and validated."})
	switch {
	case o.via != "" || o.policy != "":
		request := routing.Request{Host: o.args[0], Port: o.args[1], Via: o.via, Policy: o.policy}
		if o.via == "direct" {
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "daemon", State: "skipped", Message: "A direct target probe does not require the VPN daemon."})
			decision, resolveErr := routing.New(c, nil).Resolve(ctx, request)
			if resolveErr != nil {
				report.Checks = append(report.Checks, diagnostic.Check{Stage: "route", State: "failed", Code: "route", Message: resolveErr.Error()})
			} else {
				report.Route = &decision
				report.Checks = append(report.Checks, diagnostic.Check{Stage: "route", State: "ok", Message: "Explicit direct route; DNS may occur inside the TCP dial."})
				conn, dialErr := (&net.Dialer{}).DialContext(ctx, "tcp", decision.Address)
				if dialErr != nil {
					report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "failed", Code: diagnostic.NetworkCode(dialErr), Message: "Could not connect to the direct target TCP port.", Advice: "Check the target address, DNS and listening service."})
				} else {
					conn.Close()
					report.Checks = append(report.Checks, diagnostic.Check{Stage: "target_tcp", State: "ok", Message: "Target TCP connection succeeded; no application data was sent."})
				}
			}
		} else if doctorStatus(ctx, c, path, &report) {
			result, probeErr := daemon.Diagnose(ctx, c, request)
			if probeErr != nil {
				code, advice := "daemon_probe", "Check the running service and selected config."
				if ctx.Err() != nil {
					code = diagnostic.NetworkCode(ctx.Err())
				}
				if strings.Contains(probeErr.Error(), "unknown action") || strings.Contains(probeErr.Error(), "does not support diagnostics") {
					code = "daemon_unsupported"
					advice = "Restart the OCH daemon with the current build to enable doctor."
				}
				report.Checks = append(report.Checks, diagnostic.Check{Stage: "daemon", State: "failed", Code: code, Message: probeErr.Error(), Advice: advice})
			} else {
				report.Source = result.Source
				report.Route = result.Route
				for i := range result.Checks {
					if result.Checks[i].Code == "login_required" && result.Route != nil {
						result.Checks[i].Advice = fmt.Sprintf("Run och login --config %q --via %q.", path, result.Route.Via)
					}
				}
				report.Checks = append(report.Checks, result.Checks...)
			}
		}
	case o.ssh != "":
		p, ok := c.SSH[o.ssh]
		if !ok {
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "ssh", State: "failed", Code: "unknown_ssh", Message: "No native SSH profile with that name exists in the OCH YAML."})
			break
		}
		if doctorNeedsService(ctx, c, path, p.Via, &report) {
			target := ""
			if len(o.args) == 1 {
				target = o.args[0]
			}
			result := forward.ProbeSSH(ctx, p, target, forwardingDialer(c))
			report.Checks = append(report.Checks, result.Checks...)
		}
	case o.forward != "":
		rule, ok := c.Forwards[o.forward]
		if !ok {
			report.Checks = append(report.Checks, diagnostic.Check{Stage: "forward", State: "failed", Code: "unknown_forward", Message: "No forwarding rule with that name exists in the OCH YAML."})
			break
		}
		via := rule.Via
		if rule.SSH != "" {
			via = c.SSH[rule.SSH].Via
		}
		if doctorNeedsService(ctx, c, path, via, &report) {
			result := forward.ProbeForward(ctx, c, rule, forwardingDialer(c))
			report.Route = result.Route
			report.Checks = append(report.Checks, result.Checks...)
		}
	default:
		doctorStatus(ctx, c, path, &report)
	}
	return printDoctor(report, o.json)
}

func doctorNeedsService(ctx context.Context, c config.Config, path, via string, report *diagnostic.Report) bool {
	if via == "direct" {
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "daemon", State: "skipped", Message: "This direct-only probe does not require the VPN daemon."})
		return true
	}
	return doctorStatus(ctx, c, path, report)
}

func doctorStatus(ctx context.Context, c config.Config, path string, report *diagnostic.Report) bool {
	conn, reply, err := daemon.RequestControl(ctx, c, daemon.Request{Action: "status"})
	if conn != nil {
		conn.Close()
	}
	if err != nil {
		code := "service_unavailable"
		if ctx.Err() != nil {
			code = diagnostic.NetworkCode(ctx.Err())
		} else if diagnostic.NetworkCode(err) == "timeout" || diagnostic.NetworkCode(err) == "canceled" {
			code = diagnostic.NetworkCode(err)
		}
		report.Checks = append(report.Checks, diagnostic.Check{Stage: "daemon", State: "failed", Code: code, Message: "The configured OCH service did not answer its status request.", Advice: fmt.Sprintf("Run och serve --config %q, or inspect the existing service.", path)})
		return false
	}
	report.Profiles = reply.Profiles
	report.Listeners = reply.Listeners
	report.Checks = append(report.Checks, diagnostic.Check{Stage: "daemon", State: "ok", Message: "Daemon answered. Profile states are snapshots, not target connectivity checks."})
	return true
}

func printDoctor(report diagnostic.Report, jsonOutput bool) error {
	if jsonOutput {
		if err := json.NewEncoder(os.Stdout).Encode(report); err != nil {
			return err
		}
	} else {
		fmt.Fprintf(os.Stdout, "OCH doctor (source: %s)\n", report.Source)
		for _, check := range report.Checks {
			fmt.Fprintf(os.Stdout, "[%s] %s: %s\n", check.State, check.Stage, check.Message)
			if check.Advice != "" {
				fmt.Fprintf(os.Stdout, "  Next: %s\n", check.Advice)
			}
		}
		if r := report.Route; r != nil {
			fmt.Fprintf(os.Stdout, "Route: via=%s address=%s reason=%s rule=%d dns=%s\n", r.Via, r.Address, r.Reason, r.RuleIndex, r.DNS)
			if r.Match != "" {
				fmt.Fprintf(os.Stdout, "  Matched: %s\n", r.Match)
			}
			if len(r.Resolved) > 0 {
				fmt.Fprintf(os.Stdout, "  System DNS: %s\n", strings.Join(r.Resolved, ", "))
			}
		}
		names := make([]string, 0, len(report.Profiles))
		for name := range report.Profiles {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(os.Stdout, "Profile %s: %s (observed state only)\n", name, report.Profiles[name])
		}
		names = names[:0]
		for name := range report.Listeners {
			names = append(names, name)
		}
		sort.Strings(names)
		for _, name := range names {
			fmt.Fprintf(os.Stdout, "Listener %s: %s (reported address, not probed)\n", name, report.Listeners[name])
		}
	}
	if !report.OK() {
		return errors.New("doctor found failed checks")
	}
	return nil
}
