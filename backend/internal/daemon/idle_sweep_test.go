package daemon

import (
	"context"
	"log/slog"
	"sync/atomic"
	"testing"
	"time"
)

func TestStartIdleSweep_DisabledClosesImmediately(t *testing.T) {
	var calls int32
	done := startTickerSweep(context.Background(), "idle session sweep", 0, func(context.Context) error {
		atomic.AddInt32(&calls, 1)
		return nil
	}, slog.Default())

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("done channel not closed for a disabled (interval<=0) sweep")
	}
	if got := atomic.LoadInt32(&calls); got != 0 {
		t.Fatalf("sweep called %d times when disabled, want 0", got)
	}
}

func TestStartIdleSweep_TicksThenStopsOnCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ticked := make(chan struct{}, 1)
	done := startTickerSweep(ctx, "idle session sweep", 5*time.Millisecond, func(context.Context) error {
		select {
		case ticked <- struct{}{}:
		default:
		}
		return nil
	}, slog.Default())

	select {
	case <-ticked:
	case <-time.After(2 * time.Second):
		t.Fatal("sweep was never called")
	}

	cancel()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("done channel not closed after context cancel")
	}
}
