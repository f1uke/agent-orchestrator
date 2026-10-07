package cli

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"os/signal"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

// An app's stdout, kept in a FILE.
//
// `print` goes to stdout, and an app SpringBoard launches has its stdout on
// /dev/null. The way to keep it that AO refuses to offer is a pipe
// (`--console-pipe`): a pipe nobody drains fills at 64 KB and blocks the app
// in write() on its main thread. A regular file never blocks its writer, so
// `simctl launch --stdout=<file>` gives the same output with no such failure.
// What was measured, and what the code below depends on:
//
//   - The same path for --stdout and --stderr is opened ONCE and shared, so the
//     two streams interleave in their true order in one file.
//   - simctl does not truncate: a relaunch writes over the start of the old
//     file. The file is removed before every launch.
//   - A file-backed stdout is fully buffered: nothing a `print` writes appears
//     until the buffer fills. SIMCTL_CHILD_NSUnbufferedIO=YES fixes it.
//   - Anything else that relaunches the app - a Maestro `launchApp` without
//     `stopApp: false`, the home screen, a plain `ao sim launch` - gives the new
//     process /dev/null again. A record of the pid the file belongs to is kept
//     beside it, so a reader can tell the file is no longer the app's.

const (
	// simConsoleUnbuffered goes on the simctl process; simctl hands SIMCTL_CHILD_*
	// to the app without the prefix.
	simConsoleUnbuffered = "SIMCTL_CHILD_NSUnbufferedIO=YES"
	// simConsoleRelaunchNote is said at every console launch, because the
	// capture ending silently is the thing a reader would not guess.
	simConsoleRelaunchNote = "Read it with `ao sim console`. Only this launch is captured: a relaunch by anything else " +
		"(a Maestro `launchApp` without `stopApp: false`, the home screen, `ao sim launch` without --console) " +
		"sends stdout back to /dev/null."
	defaultSimConsoleMaxLines = 200
	simConsolePoll            = 250 * time.Millisecond
	// simConsoleCheckEvery is how many polls pass between reads of the process
	// table during --follow, to notice the app being relaunched under it.
	simConsoleCheckEvery = 8
)

// simConsoleRecord is written beside the console file at launch.
type simConsoleRecord struct {
	PID        int       `json:"pid"`
	BundleID   string    `json:"bundleId"`
	UDID       string    `json:"udid"`
	LaunchedAt time.Time `json:"launchedAt"`
}

// simConsoleCapture is whether the file is still the app's output.
type simConsoleCapture string

const (
	simConsoleCurrent    simConsoleCapture = "current"
	simConsoleRelaunched simConsoleCapture = "relaunched"
	simConsoleEnded      simConsoleCapture = "not-running"
	simConsoleUnknown    simConsoleCapture = "unknown"
)

// simConsoleResult is the `ao sim console --json` payload.
type simConsoleResult struct {
	UDID        string            `json:"udid"`
	Name        string            `json:"name"`
	BundleID    string            `json:"bundleId"`
	Path        string            `json:"path"`
	RecordedPID int               `json:"recordedPid,omitempty"`
	LaunchedAt  *time.Time        `json:"launchedAt,omitempty"`
	CurrentPID  int               `json:"currentPid,omitempty"`
	Capture     simConsoleCapture `json:"capture"`
	// Note says why the file may not hold the app's latest output.
	Note    string   `json:"note,omitempty"`
	Lines   []string `json:"lines"`
	Matched int      `json:"matched"`
	Total   int      `json:"total"`
	Dropped int      `json:"dropped,omitempty"`
}

// simConsolePaths are the capture file and its launch record.
func simConsolePaths(sessionID, udid, bundleID string) (string, string, error) {
	cfg, err := config.Load()
	if err != nil {
		return "", "", err
	}
	base := filepath.Join(simrecord.ConsoleDir(cfg.DataDir, sessionID), udid+"-"+bundleID)
	return base + ".log", base + ".json", nil
}

// launchSimConsole launches the app with its stdout and stderr in the session's
// console file, and records which pid the file belongs to. The device's lease
// is the caller's to have taken.
func (c *commandContext) launchSimConsole(ctx context.Context, device simDevice, bundleID string) (string, string, error) {
	sessionID, err := simSessionID("--console")
	if err != nil {
		return "", "", err
	}
	logPath, recordPath, err := simConsolePaths(sessionID, device.UDID, bundleID)
	if err != nil {
		return "", "", err
	}
	if err := os.MkdirAll(filepath.Dir(logPath), 0o750); err != nil {
		return "", "", fmt.Errorf("create console directory: %w", err)
	}
	for _, path := range []string{logPath, recordPath} {
		if err := os.Remove(path); err != nil && !errors.Is(err, fs.ErrNotExist) {
			return "", "", fmt.Errorf("remove the previous capture %s: %w", path, err)
		}
	}
	out, err := c.deps.CommandOutputWithEnv(ctx, []string{simConsoleUnbuffered}, simctl.Binary, "simctl", "launch",
		"--terminate-running-process", "--stdout="+logPath, "--stderr="+logPath, device.UDID, bundleID)
	if err != nil {
		return "", "", fmt.Errorf("`simctl launch` failed for %s on %s: %w: %s", bundleID, device.Label(), err, simctl.Output(out))
	}
	pid := launchedPID(out)
	record := simConsoleRecord{BundleID: bundleID, UDID: device.UDID, LaunchedAt: c.deps.Now().UTC()}
	record.PID, _ = strconv.Atoi(pid)
	body, err := json.Marshal(record)
	if err != nil {
		return "", "", err
	}
	if err := os.WriteFile(recordPath, body, 0o600); err != nil {
		return "", "", fmt.Errorf("write the launch record %s: %w", recordPath, err)
	}
	return pid, logPath, nil
}

// writeSimConsoleLine is the launch output's line about the capture.
func writeSimConsoleLine(out io.Writer, path string) error {
	if path == "" {
		return nil
	}
	_, err := fmt.Fprintf(out, "Console: %s\n  %s\n", path, simConsoleRelaunchNote)
	return err
}

type simConsoleOptions struct {
	follow   bool
	grep     string
	maxLines int
	json     bool
}

func newSimConsoleCommand(ctx *commandContext) *cobra.Command {
	opts := simConsoleOptions{}
	cmd := &cobra.Command{
		Use:   "console [bundle-id]",
		Short: "Read an app's stdout and stderr, captured by `ao sim launch --console`",
		Long: "Read the file `ao sim launch --console` (or `ao sim run --console`) sends an app's stdout and " +
			"stderr to - where `print` and `debugPrint` go - on this session's own simulator: its primary one, " +
			"or with --device one it claimed.\n\n" +
			"The app is the bundle id given, else $AO_SIM_APP, else the newest installed app. It prints the " +
			"last --max-lines lines (default 200), only those matching --grep when given; --follow keeps " +
			"printing what is appended until interrupted.\n\n" +
			"Only the launch that made the file is captured. When the app has been relaunched since - by a " +
			"Maestro `launchApp`, the home screen, or a launch without --console - its output goes to /dev/null " +
			"again, and this says so above the lines: run `ao sim launch --console` again. A file never blocks " +
			"the app, unlike a pipe, so a capture nobody reads costs nothing.",
		Example: `  ao sim launch --console && ao sim console
  ao sim console --grep "resp:" --max-lines 50
  ao sim console --follow`,
		Args: cobra.MaximumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return ctx.runSimConsole(cmd, firstArg(args), opts)
		},
	}
	f := cmd.Flags()
	f.BoolVarP(&opts.follow, "follow", "f", false, "Keep printing lines as the app writes them, until interrupted")
	f.StringVar(&opts.grep, "grep", "", "Only lines matching this regular expression")
	f.IntVar(&opts.maxLines, "max-lines", defaultSimConsoleMaxLines, "Print at most this many of the most recent lines")
	f.BoolVar(&opts.json, "json", false, "Output the result as JSON (one object per line with --follow)")
	return cmd
}

func (c *commandContext) runSimConsole(cmd *cobra.Command, bundleID string, opts simConsoleOptions) error {
	if opts.maxLines <= 0 {
		return usageError{fmt.Errorf("--max-lines must be positive, got %d", opts.maxLines)}
	}
	var filter *regexp.Regexp
	if opts.grep != "" {
		re, err := regexp.Compile(opts.grep)
		if err != nil {
			return usageError{fmt.Errorf("--grep %q is not a regular expression: %w", opts.grep, err)}
		}
		filter = re
	}
	ctx := cmd.Context()
	target, err := c.ownSimApp(ctx, cmd, "`ao sim console`", bundleID)
	if err != nil {
		return err
	}
	sessionID, _ := simSessionID("`ao sim console`")
	logPath, recordPath, err := simConsolePaths(sessionID, target.device.UDID, target.app.BundleID)
	if err != nil {
		return err
	}
	content, err := os.ReadFile(logPath) //nolint:gosec // this session's own console file
	if errors.Is(err, fs.ErrNotExist) {
		return fmt.Errorf("there is no console capture of %s on %s; `ao sim launch --console %s` relaunches it with its stdout and stderr in a file",
			target.app.BundleID, target.device.Label(), target.app.BundleID)
	}
	if err != nil {
		return err
	}
	result := simConsoleResult{
		UDID: target.device.UDID, Name: target.device.Name, BundleID: target.app.BundleID, Path: logPath,
	}
	record, recorded := readSimConsoleRecord(recordPath)
	if recorded {
		result.RecordedPID = record.PID
		at := record.LaunchedAt
		result.LaunchedAt = &at
	}
	status, err := c.simAppStatus(ctx, target)
	if err != nil {
		return err
	}
	result.CurrentPID = status.PID
	result.Capture, result.Note = simConsoleFreshness(record, recorded, status)

	lines := splitConsoleLines(content)
	result.Total = len(lines)
	result.Lines = []string{}
	for _, line := range lines {
		if filter == nil || filter.MatchString(line) {
			result.Matched++
			result.Lines = append(result.Lines, line)
		}
	}
	if len(result.Lines) > opts.maxLines {
		result.Dropped = len(result.Lines) - opts.maxLines
		result.Lines = result.Lines[result.Dropped:]
	}

	out := cmd.OutOrStdout()
	if opts.json && !opts.follow {
		return writeJSON(out, result)
	}
	if opts.json {
		for _, line := range result.Lines {
			if err := writeJSONLine(out, map[string]string{"line": line}); err != nil {
				return err
			}
		}
	} else if err := writeSimConsole(out, result, opts); err != nil {
		return err
	}
	if !opts.follow {
		return nil
	}
	followCtx, stop := signal.NotifyContext(ctx, os.Interrupt, syscall.SIGTERM)
	defer stop()
	follow := simConsoleFollow{target: target, logPath: logPath, recordPath: recordPath, record: record, recorded: recorded,
		capture: result.Capture, filter: filter, asJSON: opts.json}
	return c.followSimConsole(followCtx, out, follow, int64(len(content)))
}

func readSimConsoleRecord(path string) (simConsoleRecord, bool) {
	raw, err := os.ReadFile(path) //nolint:gosec // this session's own launch record
	if err != nil {
		return simConsoleRecord{}, false
	}
	var record simConsoleRecord
	if json.Unmarshal(raw, &record) != nil || record.PID == 0 {
		return simConsoleRecord{}, false
	}
	return record, true
}

// simConsoleFreshness says whether the file is still where the app writes.
func simConsoleFreshness(record simConsoleRecord, recorded bool, status simPIDResult) (simConsoleCapture, string) {
	again := fmt.Sprintf("run `ao sim launch --console %s` again to capture it", status.BundleID)
	switch {
	case !recorded:
		return simConsoleUnknown, "there is no record of which launch wrote this file, so whether it is still the app's output is unknown; " + again
	case status.State == simAppNotRunning:
		return simConsoleEnded, fmt.Sprintf("%s is not running now: the file ends where pid %d ended (if it crashed, `ao sim crashes --show 1`)",
			status.BundleID, record.PID)
	case status.PID != record.PID:
		return simConsoleRelaunched, fmt.Sprintf("%s was relaunched since this capture (it is pid %d now, the file is pid %d's), "+
			"so its output after that relaunch is not in the file; %s", status.BundleID, status.PID, record.PID, again)
	case status.Hold != nil:
		return simConsoleCurrent, "the app is " + status.Hold.State() + ", so it writes nothing until it is freed: " + status.Hold.Fix()
	default:
		return simConsoleCurrent, ""
	}
}

func splitConsoleLines(content []byte) []string {
	text := strings.TrimSuffix(string(content), "\n")
	if text == "" {
		return nil
	}
	return strings.Split(text, "\n")
}

func writeSimConsole(out io.Writer, result simConsoleResult, opts simConsoleOptions) error {
	if _, err := fmt.Fprintf(out, "Console of %s on %s (%s): %s\n", result.BundleID, result.Name, result.UDID, result.Path); err != nil {
		return err
	}
	if result.LaunchedAt != nil {
		if _, err := fmt.Fprintf(out, "Captured from pid %d, launched %s.\n", result.RecordedPID, result.LaunchedAt.Format(time.RFC3339)); err != nil {
			return err
		}
	}
	if result.Note != "" {
		if _, err := fmt.Fprintf(out, "Warning: %s\n", result.Note); err != nil {
			return err
		}
	}
	if _, err := fmt.Fprintln(out); err != nil {
		return err
	}
	for _, line := range result.Lines {
		if _, err := fmt.Fprintln(out, line); err != nil {
			return err
		}
	}
	var summary string
	switch {
	case result.Total == 0:
		summary = "The file is empty: the app has written nothing to stdout or stderr since this launch."
	case result.Dropped > 0:
		summary = fmt.Sprintf("%d of %d matching lines shown (the most recent); re-run with --max-lines %d or a narrower --grep to see the rest.",
			len(result.Lines), result.Matched, result.Matched)
	case opts.grep != "":
		summary = fmt.Sprintf("%d of %d lines matched.", result.Matched, result.Total)
	default:
		summary = fmt.Sprintf("%d line%s.", result.Total, pluralS(result.Total))
	}
	if opts.follow {
		summary += " Following; Ctrl-C stops."
	}
	_, err := fmt.Fprintf(out, "\n%s\n", summary)
	return err
}

// simConsoleFollow is what --follow tracks between polls.
type simConsoleFollow struct {
	target              simOwnApp
	logPath, recordPath string
	record              simConsoleRecord
	recorded            bool
	capture             simConsoleCapture
	filter              *regexp.Regexp
	asJSON              bool
}

// followSimConsole prints what is appended to the file until ctx ends. A file
// needs no child process to tail: it is polled. A file that is replaced or
// shrinks was restarted by a new console launch and is read from its start;
// the app being relaunched by anything else is said once, as it happens.
func (c *commandContext) followSimConsole(ctx context.Context, out io.Writer, f simConsoleFollow, offset int64) error {
	path := f.logPath
	opened, _ := os.Stat(path)
	var partial []byte
	emit := func(line string) error {
		if f.filter != nil && !f.filter.MatchString(line) {
			return nil
		}
		if f.asJSON {
			return writeJSONLine(out, map[string]string{"line": line})
		}
		_, err := fmt.Fprintln(out, line)
		return err
	}
	ticker := time.NewTicker(simConsolePoll)
	defer ticker.Stop()
	for polls := 1; ; polls++ {
		select {
		case <-ctx.Done():
			return nil
		case <-ticker.C:
		}
		info, err := os.Stat(path)
		if err != nil {
			continue
		}
		if opened == nil || !os.SameFile(opened, info) || info.Size() < offset {
			opened, offset, partial = info, 0, nil
			if !f.asJSON {
				if _, err := fmt.Fprintln(out, "--- the capture restarted: a new console launch replaced the file ---"); err != nil {
					return err
				}
			}
			f.record, f.recorded = readSimConsoleRecord(f.recordPath)
			f.capture = simConsoleCurrent
		}
		if info.Size() > offset {
			chunk, err := readFrom(path, offset)
			if err != nil {
				return err
			}
			offset += int64(len(chunk))
			partial = append(partial, chunk...)
			for {
				i := bytes.IndexByte(partial, '\n')
				if i < 0 {
					break
				}
				if err := emit(string(partial[:i])); err != nil {
					return err
				}
				partial = partial[i+1:]
			}
		}
		if polls%simConsoleCheckEvery != 0 || f.capture != simConsoleCurrent || f.asJSON {
			continue
		}
		status, err := c.simAppStatus(ctx, f.target)
		if err != nil {
			continue
		}
		if now, note := simConsoleFreshness(f.record, f.recorded, status); now != simConsoleCurrent {
			f.capture = now
			if _, err := fmt.Fprintf(out, "--- Warning: %s ---\n", note); err != nil {
				return err
			}
		}
	}
}

func readFrom(path string, offset int64) ([]byte, error) {
	f, err := os.Open(path) //nolint:gosec // this session's own console file
	if err != nil {
		return nil, err
	}
	defer func() { _ = f.Close() }()
	if _, err := f.Seek(offset, io.SeekStart); err != nil {
		return nil, err
	}
	return io.ReadAll(f)
}
