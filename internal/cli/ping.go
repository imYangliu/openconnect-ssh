package cli

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/imyangliu/openconnect-ssh/internal/config"
	"github.com/imyangliu/openconnect-ssh/internal/daemon"
)

type pingOptions struct {
	via      string
	args     []string
	count    int
	interval time.Duration
	timeout  time.Duration
}

func runPing(ctx context.Context, c config.Config, options pingOptions, output io.Writer) error {
	if len(options.args) != 1 {
		return errors.New("ping requires --via NAME and one IP_OR_DOMAIN; put flags before the target")
	}
	if options.via == "" || options.via == "direct" {
		return errors.New("ping requires --via NAME selecting a configured VPN profile")
	}
	if err := c.ValidateVia(options.via); err != nil {
		return err
	}
	host := options.args[0]
	if err := daemon.ValidatePingTarget(host); err != nil {
		return err
	}
	if err := validateProbeOptions("ping", options); err != nil {
		return err
	}
	fmt.Fprintf(output, "PING %s via %s\n", host, options.via)
	stats := repeatProbes(ctx, options, func(sequence int) (time.Duration, error) {
		result, err := daemon.Ping(ctx, c, options.via, host, sequence, options.timeout)
		if err != nil {
			if ctx.Err() == nil {
				fmt.Fprintf(output, "icmp_seq=%d: %v\n", sequence, err)
			}
			return 0, err
		}
		fmt.Fprintf(output, "%d bytes from %s: icmp_seq=%d time=%.3f ms\n", result.Bytes, result.Address, result.Sequence, milliseconds(result.RTT))
		return result.RTT, nil
	})
	fmt.Fprintf(output, "\n--- %s ping statistics ---\n%d probes attempted, %d replies received, %.1f%% unanswered\n", host, stats.attempted, stats.succeeded, stats.loss())
	if stats.succeeded == 0 {
		return errors.New("ping received no replies")
	}
	fmt.Fprintf(output, "rtt min/avg/max = %.3f/%.3f/%.3f ms\n", milliseconds(stats.minimum), milliseconds(stats.total/time.Duration(stats.succeeded)), milliseconds(stats.maximum))
	return nil
}

func milliseconds(duration time.Duration) float64 {
	return float64(duration) / float64(time.Millisecond)
}
