package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
	"github.com/aoagents/agent-orchestrator/backend/internal/service/iosrun"
	"github.com/aoagents/agent-orchestrator/backend/internal/xcodeproj"
	"github.com/aoagents/agent-orchestrator/backend/internal/xcresultstream"
)

type runReport struct {
	dir string
	now func() time.Time

	mu           sync.Mutex
	progress     iosrun.Progress
	buildSeconds float64
	end          appEnd
	issues       xcresultstream.Snapshot
}

func newRunReport() *runReport {
	dir := strings.TrimSpace(os.Getenv(iosrun.EnvRunDir))
	if dir == "" {
		return nil
	}
	r := &runReport{dir: dir, now: time.Now}
	r.stage(iosrun.StagePreparing)
	return r
}

func (r *runReport) stage(stage iosrun.Stage) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	now := r.now().UTC()
	r.progress.PID = os.Getpid()
	r.progress.Stage, r.progress.StageStartedAt = stage, now
	if stage == iosrun.StageBuilding {
		r.progress.BuildStartedAt = &now
		r.progress.Build = &iosrun.BuildProgress{}
	}
	r.writeLocked()
}

func (r *runReport) build(snap xcresultstream.Snapshot, shared bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	build := iosrun.BuildProgress{Phase: snap.Phase, Shared: shared, Errors: snap.Errors, Warnings: snap.Warnings}
	if !shared {
		build.Counts = snap.Counts
	}
	r.progress.Build = &build
	r.writeLocked()
}

func (r *runReport) buildSucceeded() {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.progress.BuildStartedAt != nil {
		r.buildSeconds = r.now().Sub(*r.progress.BuildStartedAt).Seconds()
	}
}

func (r *runReport) buildFinished(snap xcresultstream.Snapshot) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.issues = snap
}

func (r *runReport) buildLog() *os.File {
	if r == nil {
		return nil
	}
	if err := os.MkdirAll(r.dir, 0o750); err != nil {
		return nil
	}
	f, err := os.Create(filepath.Join(r.dir, iosrun.LogFile))
	if err != nil {
		return nil
	}
	return f
}

// appEnd is how an attached app's run ended.
type appEnd int

const (
	appExited appEnd = iota + 1
	appStoppedByUser
)

func (r *runReport) ended(how appEnd) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	r.end = how
}

func (r *runReport) writeLocked() {
	body, err := json.Marshal(r.progress)
	if err != nil {
		return
	}
	if err := os.MkdirAll(r.dir, 0o750); err != nil {
		return
	}
	_ = fsatomic.WriteFile(filepath.Join(r.dir, iosrun.ProgressFile), body, 0o600)
}

func (r *runReport) finish(result simRunResult, runErr error, stopped bool) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	finished := r.now().UTC()
	verdict := iosrun.Result{
		State: iosrun.RunSucceeded, FinishedAt: &finished, BuildSeconds: r.buildSeconds,
		Errors: r.issues.Errors, Warnings: r.issues.Warnings, Issues: r.issues.Issues,
	}
	switch {
	case runErr != nil && stopped:
		verdict.State = iosrun.RunStopped
		verdict.Summary = firstLineOf(runErr)
	case runErr != nil:
		verdict.State = iosrun.RunFailed
		verdict.Summary = firstLineOf(runErr)
	case result.NoBuild:
		verdict.Summary = fmt.Sprintf("Launched the last build of %s (%s), %s, on %s.",
			result.Scheme, result.Configuration, result.BundleID, result.Name)
	case result.BuildOnly:
		verdict.Summary = fmt.Sprintf("Built %s (%s).", result.Scheme, result.Configuration)
	default:
		verdict.Summary = fmt.Sprintf("Built %s (%s) and launched %s on %s.",
			result.Scheme, result.Configuration, result.BundleID, result.Name)
		switch r.end {
		case appStoppedByUser:
			verdict.Summary += " It ran until you stopped it."
		case appExited:
			verdict.Summary += " It has exited."
		}
		verdict.Warning = firstLine(result.Warning)
	}
	body, err := json.Marshal(verdict)
	if err != nil {
		return
	}
	if err := os.MkdirAll(r.dir, 0o750); err != nil {
		return
	}
	_ = fsatomic.WriteFile(filepath.Join(r.dir, iosrun.ResultFile), body, 0o600)
}

const progressPoll = 500 * time.Millisecond

const sharedCheckEvery = 6

func (c *commandContext) watchBuildProgress(ctx context.Context, streamPath string, report *runReport) (stop func() xcresultstream.Snapshot) {
	var reader xcresultstream.Reader
	var offset int64
	readMore := func() {
		f, err := os.Open(streamPath)
		if err != nil {
			return
		}
		defer func() { _ = f.Close() }()
		if _, err := f.Seek(offset, io.SeekStart); err != nil {
			return
		}
		chunk, err := io.ReadAll(f)
		if err != nil {
			return
		}
		offset += int64(len(chunk))
		reader.Feed(chunk)
	}
	shared := false
	done := make(chan struct{})
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		ticker := time.NewTicker(progressPoll)
		defer ticker.Stop()
		for tick := 0; ; tick++ {
			select {
			case <-done:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
			}
			if tick%sharedCheckEvery == 0 {
				if out, err := c.deps.CommandOutput(ctx, "ps", "-axo", "pid=,args="); err == nil {
					shared = otherProgressBuilds(out, streamPath)
					reader.Share(shared)
				}
			}
			readMore()
			report.build(reader.Snapshot(), shared)
		}
	}()
	return func() xcresultstream.Snapshot {
		close(done)
		<-finished
		readMore()
		snap := reader.Snapshot()
		report.build(snap, shared)
		return snap
	}
}

func otherProgressBuilds(ps []byte, ownStream string) bool {
	for _, line := range bytes.Split(ps, []byte("\n")) {
		text := string(line)
		if strings.Contains(text, "xcodebuild") && strings.Contains(text, xcodeproj.ProgressFlag) &&
			!strings.Contains(text, ownStream) {
			return true
		}
	}
	return false
}
