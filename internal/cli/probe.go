package cli

import (
	"context"
	"fmt"
	"time"
)

type probeStats struct {
	attempted, succeeded    int
	total, minimum, maximum time.Duration
}

func validateProbeOptions(name string, options pingOptions) error {
	if options.count < 1 || options.count > 65535 {
		return fmt.Errorf("%s count must be between 1 and 65535", name)
	}
	if options.interval < 10*time.Millisecond || options.interval > 60*time.Second {
		return fmt.Errorf("%s interval must be between 10ms and 60s", name)
	}
	if options.timeout <= 0 || options.timeout > 60*time.Second {
		return fmt.Errorf("%s timeout must be greater than zero and at most 60s", name)
	}
	return nil
}

func repeatProbes(ctx context.Context, options pingOptions, probe func(int) (time.Duration, error)) probeStats {
	var stats probeStats
	var lastStart time.Time
	for sequence := 1; sequence <= options.count; sequence++ {
		if !lastStart.IsZero() {
			if delay := time.Until(lastStart.Add(options.interval)); delay > 0 {
				timer := time.NewTimer(delay)
				select {
				case <-ctx.Done():
					timer.Stop()
				case <-timer.C:
				}
			}
		}
		if ctx.Err() != nil {
			break
		}
		lastStart = time.Now()
		stats.attempted++
		elapsed, err := probe(sequence)
		if err != nil {
			continue
		}
		stats.succeeded++
		stats.total += elapsed
		if stats.succeeded == 1 || elapsed < stats.minimum {
			stats.minimum = elapsed
		}
		if elapsed > stats.maximum {
			stats.maximum = elapsed
		}
	}
	return stats
}

func (s probeStats) loss() float64 {
	if s.attempted == 0 {
		return 0
	}
	return 100 * float64(s.attempted-s.succeeded) / float64(s.attempted)
}
