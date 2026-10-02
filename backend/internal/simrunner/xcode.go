package simrunner

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Launcher is everything the Manager needs from Xcode. It is an interface so
// every lifecycle path is testable without a simulator.
type Launcher interface {
	// Build makes sure the runner is built for this machine's Xcode and
	// returns its .xctestrun.
	Build(ctx context.Context) (string, error)
	// Start launches the runner on a device. The process it returns is the
	// one AO owns and later stops - by that pid, never by name.
	Start(spec StartSpec) (Process, error)
	// TerminateRunner stops the runner app on the device itself. Killing the
	// xcodebuild that launched it does not always reach it, and a runner left
	// alive keeps its port until its idle timeout.
	TerminateRunner(ctx context.Context, udid string) error
	// ProcessCommand is a live process's command line, or "" when it is not
	// running. The sweep uses it so a recycled pid is never signalled.
	ProcessCommand(ctx context.Context, pid int) string
}

// StartSpec is one runner launch.
type StartSpec struct {
	XCTestRun string
	UDID      string
	Port      int
	// Idle is how long the runner waits without a request before it stops
	// itself. It is the guarantee that a daemon that died leaves no runner.
	Idle time.Duration
	// Dir holds this launch's log and result bundle.
	Dir string
}

// Process is a launched runner.
type Process interface {
	Pid() int
	// Wait blocks until it exits. Safe to call more than once.
	Wait() error
	// Terminate asks its process group to exit (SIGTERM); Kill insists.
	Terminate() error
	Kill() error
}

// xcodeLauncher is the production Launcher.
type xcodeLauncher struct {
	dataDir  string
	lookPath func(string) (string, error)

	mu        sync.Mutex
	xctestrun string
}

func newXcodeLauncher(dataDir string) *xcodeLauncher {
	return &xcodeLauncher{dataDir: dataDir, lookPath: exec.LookPath}
}

// ErrUnavailable is a machine that cannot run the runner at all: no Xcode.
var ErrUnavailable = errors.New("simrunner: xcodebuild is not available, so the XCTest reader cannot run")

func (x *xcodeLauncher) xcodebuild() (string, error) {
	bin, err := x.lookPath("xcodebuild")
	if err != nil {
		return "", ErrUnavailable
	}
	return bin, nil
}

// Build compiles the runner once per AO build and Xcode version. Concurrent
// callers wait for the one build rather than starting their own.
func (x *xcodeLauncher) Build(ctx context.Context) (string, error) {
	x.mu.Lock()
	defer x.mu.Unlock()
	if x.xctestrun != "" {
		if _, err := os.Stat(x.xctestrun); err == nil {
			return x.xctestrun, nil
		}
	}
	bin, err := x.xcodebuild()
	if err != nil {
		return "", err
	}
	srcHash, err := sourceHash()
	if err != nil {
		return "", err
	}
	version, err := exec.CommandContext(ctx, bin, "-version").Output()
	if err != nil {
		return "", fmt.Errorf("simrunner: `xcodebuild -version` failed: %w", err)
	}
	sum := sha256.Sum256(version)
	key := srcHash + "-" + hex.EncodeToString(sum[:])[:8]

	root := filepath.Join(x.dataDir, "sim", "runner")
	project, err := installSources(filepath.Join(root, "src", srcHash))
	if err != nil {
		return "", err
	}
	buildDir := filepath.Join(root, "build", key)
	if found := findXCTestRun(buildDir); found != "" {
		x.xctestrun = found
		return found, nil
	}
	if err := os.MkdirAll(buildDir, 0o750); err != nil {
		return "", fmt.Errorf("create the runner build directory: %w", err)
	}
	logPath := filepath.Join(buildDir, "build.log")
	logFile, err := os.Create(logPath)
	if err != nil {
		return "", fmt.Errorf("create the runner build log: %w", err)
	}
	defer func() { _ = logFile.Close() }()
	// DerivedData under AO's own data dir, never ~/Library: app state lives
	// under ~/.ao, and a shared DerivedData would be pruned by Xcode itself.
	cmd := exec.CommandContext(ctx, bin, "build-for-testing",
		"-project", project,
		"-scheme", schemeName,
		"-destination", "generic/platform=iOS Simulator",
		"-derivedDataPath", buildDir)
	cmd.Stdout, cmd.Stderr = logFile, logFile
	if err := cmd.Run(); err != nil {
		return "", fmt.Errorf("simrunner: building the XCTest runner failed (%w); the log is %s:\n%s",
			err, logPath, tailFile(logPath, 15))
	}
	found := findXCTestRun(buildDir)
	if found == "" {
		return "", fmt.Errorf("simrunner: the runner built but no .xctestrun is in %s", buildDir)
	}
	pruneSiblings(filepath.Join(root, "build"), key)
	pruneSiblings(filepath.Join(root, "src"), srcHash)
	x.xctestrun = found
	return found, nil
}

func findXCTestRun(buildDir string) string {
	matches, _ := filepath.Glob(filepath.Join(buildDir, "Build", "Products", "*.xctestrun"))
	if len(matches) == 0 {
		return ""
	}
	return matches[0]
}

// pruneSiblings removes the builds and sources an older AO or Xcode left.
func pruneSiblings(dir, keep string) {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return
	}
	for _, e := range entries {
		if e.Name() != keep && e.IsDir() {
			_ = os.RemoveAll(filepath.Join(dir, e.Name()))
		}
	}
}

func (x *xcodeLauncher) Start(spec StartSpec) (Process, error) {
	bin, err := x.xcodebuild()
	if err != nil {
		return nil, err
	}
	if err := os.MkdirAll(spec.Dir, 0o750); err != nil {
		return nil, fmt.Errorf("create the runner directory: %w", err)
	}
	result := filepath.Join(spec.Dir, "result.xcresult")
	// xcodebuild refuses a result bundle path that already exists.
	_ = os.RemoveAll(result)
	logFile, err := os.Create(filepath.Join(spec.Dir, "xcodebuild.log"))
	if err != nil {
		return nil, fmt.Errorf("create the runner log: %w", err)
	}
	// NOT CommandContext: the runner outlives the request that started it.
	cmd := exec.Command(bin, "test-without-building",
		"-xctestrun", spec.XCTestRun,
		"-destination", "id="+spec.UDID,
		"-resultBundlePath", result)
	// xcodebuild hands TEST_RUNNER_<NAME> to the test process as <NAME>.
	cmd.Env = append(os.Environ(),
		"TEST_RUNNER_AO_RUNNER_PORT="+strconv.Itoa(spec.Port),
		"TEST_RUNNER_AO_RUNNER_IDLE_SECONDS="+strconv.Itoa(int(spec.Idle/time.Second)))
	cmd.Stdout, cmd.Stderr = logFile, logFile
	isolateProcessGroup(cmd)
	if err := cmd.Start(); err != nil {
		_ = logFile.Close()
		return nil, fmt.Errorf("start the XCTest runner: %w", err)
	}
	p := &execProcess{cmd: cmd, log: logFile, waited: make(chan struct{})}
	go p.wait()
	return p, nil
}

func (x *xcodeLauncher) TerminateRunner(ctx context.Context, udid string) error {
	bin, err := x.lookPath("xcrun")
	if err != nil {
		return err
	}
	out, err := exec.CommandContext(ctx, bin, "simctl", "terminate", udid, RunnerBundleID).CombinedOutput()
	if err != nil {
		// Not running is the ordinary answer after a clean stop.
		return fmt.Errorf("simctl terminate: %w: %s", err, strings.TrimSpace(string(out)))
	}
	return nil
}

func (x *xcodeLauncher) ProcessCommand(ctx context.Context, pid int) string {
	out, err := exec.CommandContext(ctx, "ps", "-o", "command=", "-p", strconv.Itoa(pid)).Output()
	if err != nil {
		return ""
	}
	return strings.TrimSpace(string(out))
}

type execProcess struct {
	cmd     *exec.Cmd
	log     *os.File
	waitErr error
	waited  chan struct{}
}

func (p *execProcess) wait() {
	p.waitErr = p.cmd.Wait()
	_ = p.log.Close()
	close(p.waited)
}

func (p *execProcess) Pid() int { return p.cmd.Process.Pid }

func (p *execProcess) Wait() error {
	<-p.waited
	return p.waitErr
}

func (p *execProcess) Terminate() error { return signalGroup(p.cmd.Process.Pid, false) }
func (p *execProcess) Kill() error      { return signalGroup(p.cmd.Process.Pid, true) }

// tailFile is the last lines of a log, for an error a person reads.
func tailFile(path string, lines int) string {
	body, err := os.ReadFile(path)
	if err != nil {
		return ""
	}
	all := bytes.Split(bytes.TrimRight(body, "\n"), []byte("\n"))
	if len(all) > lines {
		all = all[len(all)-lines:]
	}
	return string(bytes.Join(all, []byte("\n")))
}
