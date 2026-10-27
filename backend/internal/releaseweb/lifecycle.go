package releaseweb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
	"github.com/aoagents/agent-orchestrator/backend/internal/processalive"
)

const (
	// DefaultPort is where the release web listens unless told otherwise.
	DefaultPort = 7420
	// statusInterval refreshes status.json even when nothing changed, so
	// expired sessions drop out of the count.
	statusInterval = 30 * time.Second
	startTimeout   = 10 * time.Second
	stopGrace      = 5 * time.Second
	shutdownGrace  = 5 * time.Second
	pollInterval   = 50 * time.Millisecond
)

// DefaultAddr is DefaultPort on loopback.
var DefaultAddr = net.JoinHostPort("127.0.0.1", strconv.Itoa(DefaultPort))

// runInfo is run.json: the process serving and where.
type runInfo struct {
	PID       int       `json:"pid"`
	Port      int       `json:"port"`
	URL       string    `json:"url"`
	StartedAt time.Time `json:"startedAt"`
}

// statusInfo is status.json.
type statusInfo struct {
	Sessions  int       `json:"sessions"`
	UpdatedAt time.Time `json:"updatedAt"`
}

// Status is what ReadStatus reports. It never carries the key.
type Status struct {
	Running       bool      `json:"running"`
	PID           int       `json:"pid,omitempty"`
	URL           string    `json:"url,omitempty"`
	Port          int       `json:"port,omitempty"`
	Sessions      int       `json:"sessions"`
	StartedAt     time.Time `json:"startedAt,omitzero"`
	KeyPresent    bool      `json:"keyPresent"`
	KeyGeneration int       `json:"keyGeneration,omitempty"`
}

// Serve listens on o.Addr (DefaultAddr when empty), which must be on
// 127.0.0.1, until ctx is cancelled; the caller cancels ctx on SIGTERM so Stop
// shuts it down cleanly. It writes run.json once listening, keeps status.json
// current, and removes both on the way out.
func Serve(ctx context.Context, o Options) error {
	addr := o.Addr
	if addr == "" {
		addr = DefaultAddr
	}
	if host, _, err := net.SplitHostPort(addr); err != nil || host != "127.0.0.1" {
		return fmt.Errorf("release web: refusing to listen on %q: only 127.0.0.1 is allowed", addr)
	}
	if st := ReadStatus(o.Paths); st.Running {
		return fmt.Errorf("release web: already running as pid %d at %s", st.PID, st.URL)
	}
	srv, err := NewServer(o)
	if err != nil {
		return err
	}
	defer func() { _ = srv.Close() }()

	ln, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("release web: listen: %w", err)
	}
	tcp, ok := ln.Addr().(*net.TCPAddr)
	if !ok {
		_ = ln.Close()
		return fmt.Errorf("release web: listener address %v is not TCP", ln.Addr())
	}
	pid := os.Getpid()
	defer removeRunFiles(o.Paths, pid)
	sessions := srv.Sessions()
	if err := writeStatusFile(o.Paths, sessions); err != nil {
		_ = ln.Close()
		return err
	}
	if err := writeJSONFile(o.Paths.RunFile(), runInfo{
		PID:       pid,
		Port:      tcp.Port,
		URL:       fmt.Sprintf("http://127.0.0.1:%d/", tcp.Port),
		StartedAt: time.Now().UTC(),
	}); err != nil {
		_ = ln.Close()
		return err
	}

	hs := &http.Server{
		Handler:           srv,
		ReadHeaderTimeout: 10 * time.Second,
		ReadTimeout:       30 * time.Second,
		WriteTimeout:      60 * time.Second,
		IdleTimeout:       120 * time.Second,
		MaxHeaderBytes:    16 << 10,
		ErrorLog:          slog.NewLogLogger(srv.log.Handler(), slog.LevelWarn),
	}
	served := make(chan error, 1)
	go func() { served <- hs.Serve(ln) }()
	tick := time.NewTicker(statusInterval)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			shutdownCtx, cancel := context.WithTimeout(context.Background(), shutdownGrace)
			err := hs.Shutdown(shutdownCtx)
			cancel()
			<-served
			return err
		case err := <-served:
			return fmt.Errorf("release web: serve: %w", err)
		case <-srv.changed:
			if n := srv.Sessions(); n != sessions {
				sessions = n
				srv.writeStatus(o.Paths, n)
			}
		case <-tick.C:
			sessions = srv.Sessions()
			srv.writeStatus(o.Paths, sessions)
			srv.access.flush(srv.now())
		}
	}
}

func (s *Server) writeStatus(p Paths, sessions int) {
	if err := writeStatusFile(p, sessions); err != nil {
		s.log.Error("release web: write status", "err", err)
	}
}

func writeStatusFile(p Paths, sessions int) error {
	return writeJSONFile(p.StatusFile(), statusInfo{Sessions: sessions, UpdatedAt: time.Now().UTC()})
}

func writeJSONFile(path string, v any) error {
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	if err := fsatomic.WriteFile(path, append(b, '\n'), 0o600); err != nil {
		return fmt.Errorf("release web: write %s: %w", path, err)
	}
	return nil
}

func readJSONFile(path string, v any) bool {
	b, err := os.ReadFile(path)
	return err == nil && json.Unmarshal(b, v) == nil
}

// removeRunFiles removes run.json and status.json while run.json still names
// pid, or is unreadable, so a stopping server never deletes a successor's.
func removeRunFiles(p Paths, pid int) {
	var info runInfo
	if readJSONFile(p.RunFile(), &info) && info.PID != pid {
		return
	}
	_ = os.Remove(p.RunFile())
	_ = os.Remove(p.StatusFile())
}

// running reports whether run.json names a live process that wrote it: alive,
// and started no later than run.json says, so a recycled pid does not count.
func running(info runInfo) bool {
	if !processalive.Alive(info.PID) {
		return false
	}
	started, err := processStartedAt(info.PID)
	if err != nil {
		return true // no start time to compare (Windows, or ps failed): trust the pid
	}
	return !started.After(info.StartedAt.Add(time.Second)) // ps reports whole seconds
}

// ReadStatus reports whether a release web is serving, where, and with how
// many sessions, and whether a usable key exists.
func ReadStatus(p Paths) Status {
	var st Status
	if k, err := ReadKey(p); err == nil {
		st.KeyPresent, st.KeyGeneration = true, k.Generation
	}
	var info runInfo
	if !readJSONFile(p.RunFile(), &info) || !running(info) {
		return st
	}
	st.Running, st.PID, st.URL, st.Port, st.StartedAt = true, info.PID, info.URL, info.Port, info.StartedAt
	var s statusInfo
	if readJSONFile(p.StatusFile(), &s) {
		st.Sessions = s.Sessions
	}
	return st
}

// Start returns the running server's status, or spawns `exe args...` detached
// (own session, output to web.log, stdin from /dev/null) and waits until it
// listens.
func Start(p Paths, exe string, args []string) (Status, error) {
	if st := ReadStatus(p); st.Running {
		return st, nil
	}
	if _, err := ReadKey(p); err != nil {
		return Status{}, err
	}
	if err := p.Ensure(); err != nil {
		return Status{}, err
	}
	logf, err := os.OpenFile(p.WebLog(), os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return Status{}, fmt.Errorf("release web: open web log: %w", err)
	}
	defer func() { _ = logf.Close() }()
	cmd := exec.Command(exe, args...) //nolint:gosec // G204: exe is the caller's own binary
	cmd.Stdout, cmd.Stderr = logf, logf
	detach(cmd)
	if err := cmd.Start(); err != nil {
		return Status{}, fmt.Errorf("release web: start: %w", err)
	}
	pid := cmd.Process.Pid
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()

	deadline := time.NewTimer(startTimeout)
	defer deadline.Stop()
	tick := time.NewTicker(pollInterval)
	defer tick.Stop()
	for {
		select {
		case err := <-exited:
			if err == nil {
				err = errors.New("exit status 0")
			}
			return Status{}, fmt.Errorf("release web: exited before listening, see %s: %w", p.WebLog(), err)
		case <-deadline.C:
			_ = cmd.Process.Kill()
			return Status{}, fmt.Errorf("release web: not listening after %v; see %s", startTimeout, p.WebLog())
		case <-tick.C:
			if st := ReadStatus(p); st.Running && st.PID == pid {
				return st, nil
			}
		}
	}
}

// Stop ends the server run.json names: SIGTERM, then SIGKILL after a grace
// period. It signals only a pid that is alive and wrote run.json, removes
// stale files, and does nothing when no server runs.
func Stop(p Paths) error {
	var info runInfo
	if readJSONFile(p.RunFile(), &info) && running(info) {
		if err := terminate(info.PID); err != nil && processalive.Alive(info.PID) {
			return fmt.Errorf("release web: stop pid %d: %w", info.PID, err)
		}
		if !waitGone(info.PID, stopGrace) {
			_ = kill(info.PID)
			if !waitGone(info.PID, stopGrace) {
				return fmt.Errorf("release web: pid %d did not exit", info.PID)
			}
		}
	}
	removeRunFiles(p, info.PID)
	return nil
}

func waitGone(pid int, within time.Duration) bool {
	deadline := time.Now().Add(within)
	for processalive.Alive(pid) {
		if time.Now().After(deadline) {
			return false
		}
		time.Sleep(pollInterval)
	}
	return true
}
