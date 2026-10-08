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

// runReport is how `ao sim run` tells the run bar what it is doing and how it
// ended, through the directory the daemon named in iosrun.EnvRunDir. A nil
// report is a command a human or an agent typed: it reports nothing.
type runReport struct {
	dir string
	now func() time.Time

	mu           sync.Mutex
	progress     iosrun.Progress
	buildSeconds float64
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

func (r *runReport) writeLocked() {
	body, err := json.Marshal(r.progress)
	if err != nil {
		return
	}
	if err := os.MkdirAll(r.dir, 0o750); err != nil {
		return
	}
	// Progress is advisory: a write that fails leaves the bar on the last one.
	_ = fsatomic.WriteFile(filepath.Join(r.dir, iosrun.ProgressFile), body, 0o600)
}

// finish writes the verdict. It is one sentence, never the build log: the
// output is already in the pane the bar points at.
func (r *runReport) finish(result simRunResult, runErr error) {
	if r == nil {
		return
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	finished := r.now().UTC()
	verdict := iosrun.Result{State: iosrun.RunSucceeded, FinishedAt: &finished, BuildSeconds: r.buildSeconds}
	if runErr != nil {
		verdict.State = iosrun.RunFailed
		verdict.Summary = firstLineOf(runErr)
	} else {
		verdict.Summary = fmt.Sprintf("Built %s (%s) and launched %s on %s.",
			result.Scheme, result.Configuration, result.BundleID, result.Name)
		// A run that worked and installed a broken app is still a run that
		// worked; the warning rides beside the verdict.
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

// progressPoll is how often a build's result stream is read; the bar polls
// every two seconds.
const progressPoll = 500 * time.Millisecond

// sharedCheckEvery is how many polls pass between process-table reads.
const sharedCheckEvery = 6

// watchBuildProgress follows the result stream at streamPath while the build
// runs, and reports what it says. stop ends the watch after one last read.
func (c *commandContext) watchBuildProgress(ctx context.Context, streamPath string, report *runReport) (stop func() xcresultstream.Snapshot) {
	var reader xcresultstream.Reader
	var offset int64
	readMore := func() {
		f, err := os.Open(streamPath) //nolint:gosec // the stream is this command's own temp file
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

// otherProgressBuilds is whether any xcodebuild other than ours is posting
// progress. Only `ao sim run` passes the flag, and its counts reach every result
// stream on the Mac with nothing saying whose they are.
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
