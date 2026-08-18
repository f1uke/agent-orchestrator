package cli

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// ProcessStream is a running child process's stdout, as it arrives.
//
// It exists for `ao sim log --follow`, which is the first command in this CLI
// whose child does not end on its own: everything else runs a command to
// completion and reads what it printed. A stream has to be readable while the
// child is still running and stoppable from another goroutine, which
// CommandOutput cannot do.
type ProcessStream interface {
	io.Reader
	// Close stops the process and waits for it. It is safe to call from
	// another goroutine while a read is in flight, and safe to call twice.
	Close() error
	// Err is why the output ended: the child's own failure, with what it said
	// on stderr, or nil when it ended cleanly or was stopped on purpose.
	Err() error
}

// startProcessStream is the production ProcessStream.
func startProcessStream(ctx context.Context, name string, args ...string) (ProcessStream, error) {
	return startProcessStreamWithEnv(ctx, nil, name, args...)
}

// startProcessStreamWithEnv is startProcessStream plus extra environment on
// top of the process's own. It is a second entry point rather than a
// parameter on StartStream because that field's only other caller, `ao sim
// log --follow`, has no environment to add and every existing call site would
// otherwise need to pass nil; a nil env here is exactly that case; append(os.
// Environ(), nil...) is just os.Environ(), so startProcessStream delegating
// to this with a nil env is unchanged behaviour, not an approximation of it.
//
// The child's stdout is an os.Pipe we own rather than cmd.StdoutPipe, because
// exec closes a StdoutPipe from Wait - which would race the read this is built
// to allow. Owning the pipe means stopping the child and reading its output are
// independent.
func startProcessStreamWithEnv(ctx context.Context, env []string, name string, args ...string) (ProcessStream, error) {
	readFrom, writeTo, err := os.Pipe()
	if err != nil {
		return nil, err
	}
	cmd := aoprocess.CommandContext(ctx, name, args...)
	cmd.Env = append(os.Environ(), env...)
	cmd.Stdout = writeTo
	stderr := &bytes.Buffer{}
	cmd.Stderr = stderr
	if err := cmd.Start(); err != nil {
		_ = readFrom.Close()
		_ = writeTo.Close()
		return nil, err
	}
	// The parent must not keep the write end open, or the read never sees EOF
	// when the child exits.
	_ = writeTo.Close()
	return &processStream{cmd: cmd, out: readFrom, stderr: stderr}, nil
}

type processStream struct {
	cmd    *exec.Cmd
	out    *os.File
	stderr *bytes.Buffer

	killOnce sync.Once
	waitOnce sync.Once
	stopped  bool
	waitErr  error
}

func (s *processStream) Read(p []byte) (int, error) { return s.out.Read(p) }

// Close stops the child and ends the read.
//
// Closing OUR end of the pipe is not tidiness, it is the only thing that ends
// the read: `simctl spawn` hands this descriptor to the process it starts
// INSIDE the simulator, which is a child of the guest's launchd. Killing simctl
// therefore does not close the write end - the guest process still holds it -
// and a reader waiting for EOF waits forever. Found exactly that way: an
// interrupted `ao sim log --follow` sat there until it was killed outright.
//
// Closing it from another goroutine while a read is in flight is safe; the read
// returns "file already closed", which the caller treats as the end it asked
// for. It also ends the guest process, whose next write to the pipe now fails.
func (s *processStream) Close() error {
	s.killOnce.Do(func() {
		s.stopped = true
		if s.cmd.Process != nil {
			_ = s.cmd.Process.Kill()
		}
		_ = s.out.Close()
	})
	s.wait()
	return nil
}

func (s *processStream) wait() {
	s.waitOnce.Do(func() { s.waitErr = s.cmd.Wait() })
}

func (s *processStream) Err() error {
	s.wait()
	// A child this process killed did what it was told; its exit status is not
	// a failure to report.
	if s.stopped || s.waitErr == nil {
		return nil
	}
	said := strings.TrimSpace(s.stderr.String())
	if said == "" {
		return s.waitErr
	}
	const limit = 400
	if len(said) > limit {
		said = said[:limit] + "…"
	}
	return fmt.Errorf("%w: %s", s.waitErr, said)
}
