package cli

import (
	"context"
	"errors"
	"fmt"
	"net"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
	"github.com/imyangliu/openconnect-ssh/internal/forward"
	"github.com/imyangliu/openconnect-ssh/internal/routing"
)

func forwardingRules(c config.Config, args []string, sshName, via string, reverse bool) (map[string]config.Forward, error) {
	rules := make(map[string]config.Forward)
	if sshName != "" || via != "" {
		if len(args) != 2 {
			return nil, errors.New("forward requires LISTEN TARGET after --ssh NAME or --via NAME")
		}
		direction := "local"
		if reverse {
			direction = "remote"
		}
		f := config.Forward{Direction: direction, Listen: args[0], Target: args[1], SSH: sshName, Via: via}
		if err := c.ValidateForward(f); err != nil {
			return nil, err
		}
		rules["command"] = f
		return rules, nil
	}
	if reverse {
		return nil, errors.New("--reverse requires --ssh NAME; saved rules use their configured direction")
	}
	if len(args) == 0 {
		return nil, errors.New("forward requires saved rule names or --ssh/--via NAME LISTEN TARGET")
	}
	for _, name := range args {
		rule, ok := c.Forwards[name]
		if !ok {
			return nil, fmt.Errorf("unknown forward %q", name)
		}
		if _, duplicate := rules[name]; duplicate {
			return nil, fmt.Errorf("duplicate forward %q", name)
		}
		rules[name] = rule
	}
	return rules, nil
}

func forwardingDialer(c config.Config) forward.DialFunc {
	return func(ctx context.Context, via, address string) (net.Conn, error) {
		if via == "direct" {
			return (&net.Dialer{}).DialContext(ctx, "tcp", address)
		}
		host, port, err := net.SplitHostPort(address)
		if err != nil {
			return nil, err
		}
		conn, _, err := daemon.RequestControl(ctx, c, daemon.Request{Action: "connect", Request: routing.Request{Host: host, Port: port, Via: via}})
		return conn, err
	}
}
