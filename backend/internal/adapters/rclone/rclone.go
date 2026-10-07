// Package rclone uploads files to a cloud folder and lists it through the
// `rclone` binary, with the remotes the person configured in rclone.
//
// It only copies and lists. It never writes rclone's config, and never runs
// `rclone link`, which makes a file public to anyone holding the URL.
package rclone

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"slices"
	"strings"
	"time"

	aoprocess "github.com/aoagents/agent-orchestrator/backend/internal/process"
)

// Binary is the CLI's name on PATH.
const Binary = "rclone"

const (
	defaultCallTimeout = 2 * time.Minute
	// defaultCopyTimeout bounds one upload: screen recordings take minutes.
	defaultCopyTimeout = 30 * time.Minute
)

// fallbackBinaries are where Homebrew installs rclone: the desktop app's
// daemon starts with a PATH that may not include them.
var fallbackBinaries = []string{"/opt/homebrew/bin/rclone", "/usr/local/bin/rclone"}

// Why a call failed.
var (
	ErrBinaryMissing = errors.New("rclone is not installed")
	ErrRemoteMissing = errors.New("rclone has no such remote")
	ErrAuth          = errors.New("rclone could not sign in to the remote")
	ErrUnavailable   = errors.New("rclone failed")
)

// Output is what one rclone run printed, and how it exited.
type Output struct {
	Stdout   []byte
	Stderr   []byte
	ExitCode int
}

// Runner runs rclone. A non-zero exit is reported in Output, not as an error;
// the error is for a run that could not complete at all.
type Runner func(ctx context.Context, name string, args ...string) (Output, error)

// LookPath resolves a binary, matching os/exec.LookPath.
type LookPath func(file string) (string, error)

// Options configures a Client. Zero fields take the real implementations.
type Options struct {
	LookPath LookPath
	Runner   Runner
	// Fallbacks are the paths tried when rclone is not on PATH.
	Fallbacks   []string
	CallTimeout time.Duration
	CopyTimeout time.Duration
}

// Client runs rclone.
type Client struct {
	lookPath    LookPath
	run         Runner
	fallbacks   []string
	callTimeout time.Duration
	copyTimeout time.Duration
}

// New builds a Client.
func New(opts Options) *Client {
	c := &Client{lookPath: opts.LookPath, run: opts.Runner, fallbacks: opts.Fallbacks, callTimeout: opts.CallTimeout, copyTimeout: opts.CopyTimeout}
	if c.lookPath == nil {
		c.lookPath = exec.LookPath
	}
	if c.run == nil {
		c.run = execRunner
	}
	if c.fallbacks == nil {
		c.fallbacks = fallbackBinaries
	}
	if c.callTimeout == 0 {
		c.callTimeout = defaultCallTimeout
	}
	if c.copyTimeout == 0 {
		c.copyTimeout = defaultCopyTimeout
	}
	return c
}

// File is one file in a remote folder.
type File struct {
	Name string
	// ID is the backend's own id for the file: on Google Drive, the file id
	// its links name.
	ID string
}

// HasRemote says whether rclone has a remote of that name configured.
func (c *Client) HasRemote(ctx context.Context, name string) (bool, error) {
	if name == "" {
		return false, nil
	}
	out, err := c.output(ctx, c.callTimeout, "", "listremotes")
	if err != nil {
		return false, err
	}
	for _, line := range strings.Split(string(out.Stdout), "\n") {
		if strings.TrimSuffix(strings.TrimSpace(line), ":") == name {
			return true, nil
		}
	}
	return false, nil
}

// Copy uploads the named files of the local folder src into the remote folder
// dst ("remote:path"), creating it as needed. A file the remote already has
// unchanged is not sent again. It returns the names rclone transferred, also
// when the copy fails partway.
func (c *Client) Copy(ctx context.Context, src, dst string, names []string) ([]string, error) {
	for _, n := range names {
		if n == "" || strings.ContainsAny(n, "\n\r") {
			return nil, fmt.Errorf("%w: %q cannot be listed in --files-from-raw", ErrUnavailable, n)
		}
	}
	list, err := os.CreateTemp("", "ao-rclone-files-*.txt")
	if err != nil {
		return nil, fmt.Errorf("%w: write the file list: %w", ErrUnavailable, err)
	}
	defer func() { _ = os.Remove(list.Name()) }()
	_, werr := list.WriteString(strings.Join(names, "\n") + "\n")
	if cerr := list.Close(); werr == nil {
		werr = cerr
	}
	if werr != nil {
		return nil, fmt.Errorf("%w: write the file list: %w", ErrUnavailable, werr)
	}
	out, err := c.output(ctx, c.copyTimeout, remoteOf(dst),
		"copy", src, dst, "--files-from-raw", list.Name(), "--use-json-log", "-v")
	return copied(out.Stderr), err
}

// copied is the files rclone's JSON log says it transferred: "Copied (new)",
// "Copied (replaced existing)", and the like.
func copied(log []byte) []string {
	var names []string
	lines := bufio.NewScanner(bytes.NewReader(log))
	lines.Buffer(make([]byte, 0, 64*1024), 1024*1024)
	for lines.Scan() {
		var entry struct {
			Msg    string `json:"msg"`
			Object string `json:"object"`
		}
		if json.Unmarshal(lines.Bytes(), &entry) == nil && strings.HasPrefix(entry.Msg, "Copied") && entry.Object != "" {
			names = append(names, entry.Object)
		}
	}
	slices.Sort(names)
	return slices.Compact(names)
}

// List lists the files directly in a remote folder ("remote:path"), with
// their ids. A folder that does not exist is ErrUnavailable.
func (c *Client) List(ctx context.Context, dir string) ([]File, error) {
	out, err := c.output(ctx, c.callTimeout, remoteOf(dir), "lsjson", dir, "--files-only")
	if err != nil {
		return nil, err
	}
	var raw []struct {
		Name string `json:"Name"`
		ID   string `json:"ID"`
	}
	if err := json.Unmarshal(out.Stdout, &raw); err != nil {
		return nil, fmt.Errorf("%w: unreadable output of rclone lsjson %s: %w", ErrUnavailable, dir, err)
	}
	files := make([]File, len(raw))
	for i, r := range raw {
		files[i] = File(r)
	}
	return files, nil
}

// remoteOf is the remote name a "remote:path" names.
func remoteOf(path string) string {
	name, _, _ := strings.Cut(path, ":")
	return name
}

// output runs rclone and returns what it printed, or the sentinel for how it
// failed. remote names the remote the call uses, for an auth error's advice.
func (c *Client) output(ctx context.Context, timeout time.Duration, remote string, args ...string) (Output, error) {
	bin, err := c.binary()
	if err != nil {
		return Output{}, err
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	res, err := c.run(ctx, bin, args...)
	if err != nil {
		if ctx.Err() != nil {
			return res, fmt.Errorf("%w: rclone %s timed out after %s", ErrUnavailable, args[0], timeout)
		}
		return res, fmt.Errorf("%w: run rclone %s: %w", ErrUnavailable, args[0], err)
	}
	if res.ExitCode != 0 {
		return res, runError(res, remote, args[0])
	}
	return res, nil
}

func (c *Client) binary() (string, error) {
	if p, err := c.lookPath(Binary); err == nil {
		return p, nil
	}
	for _, p := range c.fallbacks {
		if info, err := os.Stat(p); err == nil && !info.IsDir() {
			return p, nil
		}
	}
	return "", fmt.Errorf("%w: %s is not on PATH, nor at %s. Install it: brew install rclone",
		ErrBinaryMissing, Binary, strings.Join(c.fallbacks, " or "))
}

// sharedClientNotice is what rclone prints on every call to a Google Drive
// remote that uses rclone's own OAuth client: a notice, not a failure.
const sharedClientNotice = "rclone's shared Google Drive client_id"

// authSigns are what rclone and Google print when the remote's token no longer
// works: expired, revoked, or the OAuth client itself refused.
var authSigns = []string{
	"invalid_grant",
	"oauth2: cannot fetch token",
	"Token has been expired or revoked",
	"unauthorized_client",
	"invalid_client",
	"Error 401",
	"401 Unauthorized",
	"couldn't fetch token",
}

// runError maps a failed run to a sentinel, from what it printed on stderr.
func runError(res Output, remote, command string) error {
	var lines []string
	shared := false
	for _, line := range strings.Split(strings.TrimSpace(string(res.Stderr)), "\n") {
		if strings.Contains(line, sharedClientNotice) {
			shared = true
			continue
		}
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, logMessage(line))
		}
	}
	detail := strings.Join(lines, "; ")
	if detail == "" {
		detail = fmt.Sprintf("exit %d", res.ExitCode)
	}
	switch {
	case slices.ContainsFunc(authSigns, func(s string) bool { return strings.Contains(detail, s) }):
		advice := fmt.Sprintf("its sign-in to Google has expired or was revoked. Run `rclone config reconnect %s:` in a terminal and sign in again", remote)
		if shared {
			advice += ". The remote uses rclone's shared Google client_id, which Google may retire during 2026: if reconnecting does not help, give the remote its own client_id (https://rclone.org/drive/#making-your-own-client-id)"
		}
		return fmt.Errorf("%w %s: %s (rclone %s: %s)", ErrAuth, remote, advice, command, detail)
	case strings.Contains(detail, "didn't find section in config file"):
		return fmt.Errorf("%w: %s (rclone %s: %s)", ErrRemoteMissing, remote, command, detail)
	default:
		return fmt.Errorf("%w: rclone %s exited %d: %s", ErrUnavailable, command, res.ExitCode, detail)
	}
}

// logMessage is a stderr line's message: the msg of a JSON log line, the line
// itself otherwise.
func logMessage(line string) string {
	var entry struct {
		Msg string `json:"msg"`
	}
	if strings.HasPrefix(line, "{") && json.Unmarshal([]byte(line), &entry) == nil && entry.Msg != "" {
		return strings.TrimSpace(entry.Msg)
	}
	return line
}

func execRunner(ctx context.Context, name string, args ...string) (Output, error) {
	var stdout, stderr bytes.Buffer
	cmd := aoprocess.CommandContext(ctx, name, args...)
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) && ctx.Err() == nil {
		return Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes(), ExitCode: exitErr.ExitCode()}, nil
	}
	return Output{Stdout: stdout.Bytes(), Stderr: stderr.Bytes()}, err
}
