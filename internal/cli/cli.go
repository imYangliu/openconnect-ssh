package cli

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/browser"
	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
	"github.com/imyangliu/openconnect-ssh/internal/discovery"
	"github.com/imyangliu/openconnect-ssh/internal/forward"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
	"github.com/imyangliu/openconnect-ssh/internal/service"
	"github.com/imyangliu/openconnect-ssh/internal/setup"
	"github.com/imyangliu/openconnect-ssh/internal/vpn"
)

const help = `och — native VPN sessions and SSH routing

  och setup [--server URL]                   Configure, authenticate and start
  och auth-info --server URL                 Discover public authentication methods
  och service install --config FILE          Install a user login service
  och serve --config FILE                    Run the shared VPN service
  och status --config FILE                   Show session and listener state
  och stop --config FILE                     Stop this service
  och login --via NAME [--remember] [--browser PATH]       Authenticate or restore a VPN
  och logout --via NAME                      Close this VPN and forget saved authentication
  och proxy-command --config FILE --via NAME HOST PORT
  och proxy-command --config FILE --policy NAME HOST PORT
  och tcping --via NAME [--count 4] [--interval 1s] [--timeout 2s] HOST PORT
  och ping --via NAME [--count 4] [--interval 1s] [--timeout 2s] IP_OR_DOMAIN
  och doctor [--json] [--timeout 10s]         Check config and service state
  och doctor --via NAME HOST PORT            Probe a fixed route
  och doctor --policy NAME HOST PORT         Explain and probe the live route
  och doctor --ssh NAME [TARGET]              Check native SSH and optional target
  och doctor --forward NAME                  Check a saved forwarding rule
  och forward [--config FILE] NAME...        Run saved TCP forwarding rules
  och forward --ssh NAME LISTEN TARGET       Forward through native SSH
  och forward --reverse --ssh NAME LISTEN TARGET
  och forward --via NAME LISTEN TARGET       Forward directly through a VPN

SOCKS5 and HTTP listeners use the fixed VPN profile in the configuration.
Policies match domain/domain_suffix/CIDR in order; unmatched targets connect directly.
`

func Run(args []string) error {
	if len(args) == 0 || args[0] == "--help" || args[0] == "-h" || args[0] == "help" {
		fmt.Print(help)
		return nil
	}
	command := args[0]
	arguments := args[1:]
	if command == "service" {
		if len(arguments) == 0 || arguments[0] != "install" {
			return errors.New("use och service install --config FILE")
		}
		arguments = arguments[1:]
	}
	flags := flag.NewFlagSet(command, flag.ContinueOnError)
	flags.SetOutput(os.Stderr)
	configFile := flags.String("config", config.DefaultPath(), "configuration file")
	var via, policy, browserPath, server, protocol string
	var remember bool
	var sshName string
	var reverse bool
	var doctorJSON bool
	var doctorTimeout time.Duration
	var ping pingOptions
	var forwardName string
	var setupOptions setup.Options
	if command == "proxy-command" || command == "login" || command == "logout" || command == "forward" || command == "doctor" || command == "ping" || command == "tcping" {
		description := "fixed VPN profile (or direct)"
		if command == "ping" || command == "tcping" {
			description = "VPN profile from configuration"
		}
		flags.StringVar(&via, "via", "", description)
	}
	if command == "ping" || command == "tcping" {
		flags.IntVar(&ping.count, "count", 4, "number of probe attempts (1-65535)")
		flags.DurationVar(&ping.interval, "interval", time.Second, "minimum interval between probe starts (10ms-60s)")
		flags.DurationVar(&ping.timeout, "timeout", 2*time.Second, "per-probe timeout (up to 60s)")
	}
	if command == "doctor" {
		flags.BoolVar(&doctorJSON, "json", false, "print a structured diagnostic report")
		flags.DurationVar(&doctorTimeout, "timeout", 10*time.Second, "overall diagnostic timeout")
		flags.StringVar(&forwardName, "forward", "", "saved forwarding rule to probe")
	}
	if command == "forward" || command == "doctor" {
		flags.StringVar(&sshName, "ssh", "", "native SSH profile from OCH YAML")
		if command == "forward" {
			flags.BoolVar(&reverse, "reverse", false, "listen on the SSH host and connect the target from OCH")
		}
	}
	if command == "proxy-command" || command == "doctor" {
		flags.StringVar(&policy, "policy", "", "routing policy")
	}
	if command == "login" || command == "setup" {
		flags.StringVar(&browserPath, "browser", "", "Chrome/Chromium/Edge executable")
	}
	if command == "login" {
		flags.BoolVar(&remember, "remember", false, "retain a private OCH browser login profile")
	}
	if command == "setup" || command == "auth-info" {
		flags.StringVar(&server, "server", "", "HTTPS VPN gateway")
		flags.StringVar(&protocol, "protocol", "", "explicit anyconnect or atrust when needed")
	}
	var noRemember bool
	if command == "setup" {
		flags.StringVar(&setupOptions.Name, "name", "", "VPN profile name")
		flags.StringVar(&setupOptions.AuthType, "auth-type", "", "advertised authentication type")
		flags.StringVar(&setupOptions.LoginDomain, "login-domain", "", "advertised login domain")
		flags.StringVar(&setupOptions.AuthGroup, "auth-group", "", "AnyConnect authentication group")
		flags.StringVar(&setupOptions.Username, "username", "", "VPN username")
		flags.StringVar(&setupOptions.Listen, "listen", "", "loopback mixed proxy address")
		flags.StringVar(&setupOptions.PasswordEnv, "password-env", "", "read an initial password from this environment variable")
		flags.BoolVar(&setupOptions.NonInteractive, "non-interactive", false, "require all choices in flags; never approve new certificates")
		flags.BoolVar(&setupOptions.NoStart, "no-start", false, "save configuration without starting a service")
		flags.BoolVar(&setupOptions.SkipLogin, "skip-login", false, "write an unauthenticated draft (requires --no-start)")
		flags.BoolVar(&setupOptions.InstallService, "install-service", false, "install a user login service after authentication")
		flags.BoolVar(&noRemember, "no-remember", false, "discard the dedicated browser profile after setup")
	}
	switch command {
	case "serve", "status", "stop", "proxy-command", "login", "logout", "setup", "auth-info", "service", "forward", "doctor", "ping", "tcping":
	default:
		return errors.New("unknown command; run och --help")
	}
	if err := flags.Parse(arguments); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	path, err := filepath.Abs(*configFile)
	if err != nil {
		return err
	}
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if command == "auth-info" {
		if server == "" || flags.NArg() != 0 {
			return errors.New("auth-info requires --server URL and no positional arguments")
		}
		result, err := (discovery.Client{}).Discover(ctx, server, protocol)
		if err != nil {
			return err
		}
		return json.NewEncoder(os.Stdout).Encode(result)
	}
	if command == "setup" {
		if flags.NArg() != 0 {
			return errors.New("setup takes no positional arguments")
		}
		setupOptions.ConfigPath, setupOptions.Server, setupOptions.Protocol = path, server, protocol
		runner := setup.Runner{ReadSecret: readPassword, BrowserFor: func(name string, p config.Profile) vpn.CASCallback {
			return browserHandler(name, p, browserPath, !noRemember)
		}}
		return runner.Run(ctx, setupOptions)
	}
	if command == "doctor" {
		return runDoctor(ctx, path, doctorOptions{via: via, policy: policy, ssh: sshName, forward: forwardName, json: doctorJSON, timeout: doctorTimeout, args: flags.Args()})
	}
	c, err := config.Load(path)
	if err != nil {
		return err
	}
	switch command {
	case "tcping":
		ping.via, ping.args = via, flags.Args()
		return runTCPing(ctx, c, ping, os.Stdout)
	case "ping":
		ping.via, ping.args = via, flags.Args()
		return runPing(ctx, c, ping, os.Stdout)
	case "forward":
		rules, err := forwardingRules(c, flags.Args(), sshName, via, reverse)
		if err != nil {
			return err
		}
		return forward.Run(ctx, c, rules, forwardingDialer(c), func(e forward.Event) {
			if e.Err != nil {
				fmt.Fprintf(os.Stderr, "forward %s %s: %v\n", e.Name, e.State, e.Err)
			} else {
				fmt.Fprintf(os.Stderr, "forward %s %s %s\n", e.Name, e.State, e.Address)
			}
		})
	case "login":
		if flags.NArg() != 0 || via == "" {
			return errors.New("login requires --via NAME and no positional arguments")
		}
		if err := daemon.Login(ctx, c, via, vpn.BootstrapOptions{Browser: browserHandler(via, c.Profiles[via], browserPath, remember), PasswordPrompt: readPassword}); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "VPN authentication complete")
		return nil
	case "logout":
		if flags.NArg() != 0 || via == "" {
			return errors.New("logout requires --via NAME and no positional arguments")
		}
		if service.IsRunning(ctx, c) {
			err = daemon.Logout(ctx, c, via)
		} else {
			manager := vpn.NewManager(ctx, c.Profiles, vpn.NewNativeSession)
			err = manager.Logout(ctx, via)
			manager.Close()
		}
		if err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "VPN session stopped and saved authentication removed. Remembered browser SSO state is unchanged.")
		return nil
	case "service":
		if flags.NArg() != 0 {
			return errors.New("service install takes no positional arguments")
		}
		if service.IsRunning(ctx, c) {
			return errors.New("service already running; stop it before installing a user service")
		}
		executable, err := os.Executable()
		if err != nil {
			return err
		}
		if err = service.Install(ctx, executable, path); err != nil {
			return err
		}
		fmt.Fprintln(os.Stdout, "User login service installed")
		return nil
	case "serve":
		if flags.NArg() != 0 {
			return errors.New("serve takes no positional arguments")
		}
		return daemon.Serve(ctx, c, vpn.NewNativeSession, func(map[string]string) { fmt.Fprintln(os.Stderr, "OCH ready:", c.Socket) })
	case "status", "stop":
		if flags.NArg() != 0 {
			return errors.New(command + " takes no positional arguments")
		}
		conn, reply, err := daemon.RequestControl(ctx, c, daemon.Request{Action: command})
		if err != nil {
			return err
		}
		defer conn.Close()
		return json.NewEncoder(os.Stdout).Encode(reply)
	case "proxy-command":
		if flags.NArg() != 2 {
			return errors.New("proxy-command requires HOST PORT")
		}
		if (via == "") == (policy == "") {
			return errors.New("specify exactly one of --via or --policy")
		}
		return daemon.ProxyCommand(ctx, c, daemon.Request{Action: "connect", Request: routing.Request{Host: flags.Arg(0), Port: flags.Arg(1), Via: via, Policy: policy}}, os.Stdin, os.Stdout)
	}
	return io.EOF
}
func browserHandler(via string, p config.Profile, executable string, remember bool) vpn.CASCallback {
	return func(ctx context.Context, loginURL string) (string, error) {
		fmt.Fprintln(os.Stderr, "Complete authentication in the dedicated browser window.")
		options := browser.Options{Executable: executable}
		if remember {
			key := sha256.Sum256([]byte(via + "\x00" + p.Server + "\x00" + p.LoginDomain))
			options.ProfileDirectory = filepath.Join(filepath.Dir(config.DefaultPath()), "browser", fmt.Sprintf("%x", key[:16]))
		}
		return browser.OpenCAS(ctx, loginURL, options)
	}
}
