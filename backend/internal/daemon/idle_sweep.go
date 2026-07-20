package daemon

import (
	"context"
	"log/slog"
	"time"
)

// idleSweepIntervalDefault is how often the daemon scans for idle sessions to
// auto-close while it is running. Independent of the idle TTL: the TTL decides
// WHICH sessions close, this decides HOW PROMPTLY they are noticed.
const idleSweepIntervalDefault = 5 * time.Minute

// startTickerSweep launches a background goroutine that calls sweep on every
// tick until ctx is cancelled, returning a channel closed when the goroutine
// exits so daemon shutdown can drain it (mirroring the preview poller's
// lifecycle). A non-positive interval disables the sweep: the returned channel
// is already closed and sweep is never called.
//
// name labels the loop in the failure log, so several sweeps can share this one
// implementation and still be told apart.
func startTickerSweep(ctx context.Context, name string, interval time.Duration, sweep func(context.Context) error, log *slog.Logger) <-chan struct{} {
	done := make(chan struct{})
	if interval <= 0 {
		close(done)
		return done
	}
	go func() {
		defer close(done)
		t := time.NewTicker(interval)
		defer t.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-t.C:
				if err := sweep(ctx); err != nil {
					log.Warn(name+" failed", "err", err)
				}
			}
		}
	}()
	return done
}
