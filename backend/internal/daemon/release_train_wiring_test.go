package daemon

import (
	"context"
	"errors"
	"io"
	"log/slog"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
	rtsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/releasetrain"
	"github.com/aoagents/agent-orchestrator/backend/internal/storage/sqlite"
)

func TestReleaseTrainsLoopRunsWithoutConfigAndStopsOnCancel(t *testing.T) {
	dir := t.TempDir()
	store, err := sqlite.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	reg := looptelemetry.New(func() time.Time { return time.Now().UTC() })
	ctx, cancel := context.WithCancel(context.Background())

	svc, done := startReleaseTrains(ctx, store, dir, reg, slog.New(slog.NewTextHandler(io.Discard, nil)))

	var loop *looptelemetry.LoopStatus
	for i := 0; i < 200 && (loop == nil || loop.LastRunAt == nil); i++ {
		for _, l := range reg.Snapshot() {
			if l.Name == "release-trains" {
				l := l
				loop = &l
			}
		}
		time.Sleep(5 * time.Millisecond)
	}
	if loop == nil || loop.Display != "Release trains" || loop.IntervalMs != rtsvc.Tick.Milliseconds() || loop.LastRunAt == nil {
		t.Fatalf("loop = %+v", loop)
	}
	rep, err := svc.Report(ctx)
	if err != nil || rep.Config.State != rtsvc.ConfigAbsent || len(rep.Resources) != 0 {
		t.Fatalf("report without config = %+v, %v", rep, err)
	}

	path, err := svc.InitConfig()
	if err != nil || path != filepath.Join(dir, rtsvc.ConfigFileName) {
		t.Fatalf("InitConfig = %q, %v", path, err)
	}
	if _, err := os.Stat(path); err != nil {
		t.Fatal(err)
	}
	if _, err := svc.InitConfig(); !errors.Is(err, rtsvc.ErrConfigExists) {
		t.Fatalf("second InitConfig err = %v", err)
	}

	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the release trains loop did not stop on cancel")
	}
}
