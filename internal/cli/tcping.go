package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
)

func runTCPing(ctx context.Context, c config.Config, options pingOptions, output io.Writer) error {
	if len(options.args) != 2 {
		return errors.New("tcping requires --via NAME and HOST PORT; put flags before the target")
	}
	if options.via == "" || options.via == "direct" {
		return errors.New("tcping requires --via NAME selecting a configured VPN profile")
	}
	if err := c.ValidateVia(options.via); err != nil {
		return err
	}
	host, port := options.args[0], options.args[1]
	if err := daemon.ValidateTCPingTarget(host, port); err != nil {
		return err
	}
	if err := validateProbeOptions("tcping", options); err != nil {
		return err
	}
	address := net.JoinHostPort(host, port)
	fmt.Fprintf(output, "TCPING %s via %s (backend DNS+connect time)\n", address, options.via)
	stats := repeatProbes(ctx, options, func(sequence int) (time.Duration, error) {
		result, err := daemon.TCPing(ctx, c, options.via, host, port, options.timeout)
		if err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(output, "tcp_seq=%d: %v\n", sequence, err)
			}
			return 0, err
		}
		fmt.Fprintf(output, "connected to %s: tcp_seq=%d DNS+connect time=%.3f ms\n", result.Address, sequence, milliseconds(result.Duration))
		return result.Duration, nil
	})
	fmt.Fprintf(output, "\n--- %s tcping statistics ---\n%d probes attempted, %d connections succeeded, %.1f%% failed\n", address, stats.attempted, stats.succeeded, stats.loss())
	if stats.succeeded == 0 {
		return errors.New("tcping established no connections")
	}
	fmt.Fprintf(output, "DNS+connect min/avg/max = %.3f/%.3f/%.3f ms\n", milliseconds(stats.minimum), milliseconds(stats.total/time.Duration(stats.succeeded)), milliseconds(stats.maximum))
	return nil
}
