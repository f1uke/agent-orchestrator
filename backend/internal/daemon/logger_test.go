package daemon

import (
	stdlog "log"
	"os"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/daemonlog"
)

// An app-owned daemon's stderr dies with the app, so the daemon's own lines -
// and the stdlib logger's, which some adapters still use - must also land in
// the log file under the data dir, where a request id can be looked up later.
func TestNewLoggerAlsoWritesTheLogFileUnderTheDataDir(t *testing.T) {
	prev := stdlog.Writer()
	t.Cleanup(func() { stdlog.SetOutput(prev) })
	dataDir := t.TempDir()

	log, closeLog := newLogger(dataDir)
	log.Error("http request", "id", "host/req-000042", "error", "restore x: agent terminal could not be started")
	stdlog.Printf("WARN gitworktree: fetch failed")
	closeLog()

	got, err := os.ReadFile(daemonlog.Path(dataDir))
	if err != nil {
		t.Fatalf("read log file: %v", err)
	}
	for _, want := range []string{"req-000042", "agent terminal could not be started", "gitworktree: fetch failed"} {
		if !strings.Contains(string(got), want) {
			t.Fatalf("log file missing %q:\n%s", want, got)
		}
	}
}
