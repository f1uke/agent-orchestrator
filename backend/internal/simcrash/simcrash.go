// Package simcrash reads the crash reports simulator apps leave on the host.
//
// A simulator app that crashes is a host process, so its report lands in the
// Mac's own ~/Library/Logs/DiagnosticReports as `<Name>-<date>.ips`, among the
// reports of every other app on the machine and every other simulator. What
// ties a report to one device is its procPath, which runs through
// `/CoreSimulator/Devices/<UDID>/data/...`.
//
// An .ips file is two JSON documents: the first line is a small header (app,
// version, time, bundle id) and the rest of the file is the report body.
package simcrash

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

// DefaultDir is where macOS writes crash reports for this user.
func DefaultDir() (string, error) {
	home, err := os.UserHomeDir()
	if err != nil {
		return "", err
	}
	return filepath.Join(home, "Library", "Logs", "DiagnosticReports"), nil
}

// Header is the first line of an .ips file.
type Header struct {
	AppName      string `json:"app_name"`
	Timestamp    string `json:"timestamp"`
	BundleID     string `json:"bundleID"`
	AppVersion   string `json:"app_version"`
	BuildVersion string `json:"build_version"`
	BugType      string `json:"bug_type"`
	IncidentID   string `json:"incident_id"`
}

// Body is the part of the report body this package reads.
type Body struct {
	ProcPath               string                     `json:"procPath"`
	Exception              Exception                  `json:"exception"`
	Termination            Termination                `json:"termination"`
	ASI                    map[string]json.RawMessage `json:"asi,omitempty"`
	LastExceptionBacktrace []Frame                    `json:"lastExceptionBacktrace,omitempty"`
	FaultingThread         int                        `json:"faultingThread"`
	Threads                []Thread                   `json:"threads"`
	UsedImages             []Image                    `json:"usedImages"`
}

// Exception is the mach exception and the signal it became.
type Exception struct {
	Type   string `json:"type"`
	Signal string `json:"signal"`
}

// Termination is why the process ended and who ended it.
type Termination struct {
	Indicator string `json:"indicator"`
	ByProc    string `json:"byProc"`
}

// Thread is one thread's stack.
type Thread struct {
	Triggered bool    `json:"triggered,omitempty"`
	ID        int64   `json:"id,omitempty"`
	Name      string  `json:"name,omitempty"`
	Queue     string  `json:"queue,omitempty"`
	Frames    []Frame `json:"frames"`
}

// Frame is one stack frame. ImageIndex points into Body.UsedImages.
type Frame struct {
	ImageIndex     int    `json:"imageIndex"`
	ImageOffset    uint64 `json:"imageOffset"`
	Symbol         string `json:"symbol,omitempty"`
	SymbolLocation int64  `json:"symbolLocation,omitempty"`
	SourceFile     string `json:"sourceFile,omitempty"`
	SourceLine     int    `json:"sourceLine,omitempty"`
}

// Image is one loaded binary.
type Image struct {
	Name string `json:"name,omitempty"`
	Path string `json:"path,omitempty"`
}

// Report is one crash report of an app on one device.
type Report struct {
	Path   string    `json:"path"`
	Time   time.Time `json:"time"`
	Header Header    `json:"header"`
	Body   Body      `json:"body"`
	// Unreadable is why the body could not be read. The report is still
	// listed: a cut-off report of this device's app is still a crash.
	Unreadable string `json:"unreadable,omitempty"`
}

// List reads every report in dir of an app on the device with this udid,
// newest first. A bundle id narrows it to that app; empty keeps every app. A
// directory that does not exist holds no reports.
func List(dir, udid, bundleID string) ([]Report, error) {
	entries, err := os.ReadDir(dir)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	device := strings.ToUpper(strings.TrimSpace(udid))
	var reports []Report
	for _, entry := range entries {
		if entry.IsDir() || filepath.Ext(entry.Name()) != ".ips" {
			continue
		}
		path := filepath.Join(dir, entry.Name())
		report, ok := read(path, device)
		if !ok {
			continue
		}
		if bundleID != "" && !strings.EqualFold(report.Header.BundleID, bundleID) {
			continue
		}
		reports = append(reports, report)
	}
	sort.SliceStable(reports, func(i, j int) bool { return reports[i].Time.After(reports[j].Time) })
	return reports, nil
}

// read parses one report, and says whether it belongs to the device. Files
// that are not reports, or are another device's or the Mac's own, are not
// ours to explain and are skipped.
func read(path, device string) (Report, bool) {
	raw, err := os.ReadFile(path) //nolint:gosec // a crash report in the directory the caller named
	if err != nil {
		return Report{}, false
	}
	// procPath is JSON, where "/" may be written "\/"; the udid is the same
	// either way, and checking the raw bytes first skips parsing the body of
	// every report on the machine that is not this device's.
	if !bytes.Contains(bytes.ToUpper(raw), []byte(device)) {
		return Report{}, false
	}
	first, rest, _ := bytes.Cut(raw, []byte("\n"))
	report := Report{Path: path}
	if err := json.Unmarshal(first, &report.Header); err != nil {
		return Report{}, false
	}
	report.Time = reportTime(report.Header.Timestamp, path)
	if err := json.Unmarshal(rest, &report.Body); err != nil {
		report.Unreadable = err.Error()
		return report, true
	}
	return report, strings.Contains(strings.ToUpper(report.Body.ProcPath), "/DEVICES/"+device+"/")
}

// reportTime is when the report says it was written, else the file's mtime.
func reportTime(stamp, path string) time.Time {
	for _, layout := range []string{"2006-01-02 15:04:05.00 -0700", "2006-01-02 15:04:05 -0700"} {
		if t, err := time.Parse(layout, stamp); err == nil {
			return t
		}
	}
	if info, err := os.Stat(path); err == nil {
		return info.ModTime()
	}
	return time.Time{}
}

// CrashedThread is the thread that faulted.
func (r Report) CrashedThread() (Thread, bool) {
	if i := r.Body.FaultingThread; i >= 0 && i < len(r.Body.Threads) {
		return r.Body.Threads[i], true
	}
	for _, t := range r.Body.Threads {
		if t.Triggered {
			return t, true
		}
	}
	return Thread{}, false
}

// ImageName names the binary a frame is in.
func (r Report) ImageName(index int) string {
	if index < 0 || index >= len(r.Body.UsedImages) {
		return "???"
	}
	image := r.Body.UsedImages[index]
	switch {
	case image.Name != "":
		return image.Name
	case image.Path != "":
		return filepath.Base(image.Path)
	default:
		return "???"
	}
}

// FrameLine is one frame as a reader wants it:
// `#i image  symbol + loc  (file:line)`, or `#i image + offset` unsymbolicated.
func (r Report) FrameLine(i int, f Frame) string {
	image := r.ImageName(f.ImageIndex)
	if f.Symbol == "" {
		return fmt.Sprintf("#%d  %s + %d", i, image, f.ImageOffset)
	}
	line := fmt.Sprintf("#%d  %s  %s + %d", i, image, f.Symbol, f.SymbolLocation)
	if f.SourceFile != "" {
		line += fmt.Sprintf("  (%s:%d)", f.SourceFile, f.SourceLine)
	}
	return line
}

// ASILines is the app-specific information - a Swift fatalError's message,
// among others - one line per message, prefixed with the image that wrote it.
func (r Report) ASILines() []string {
	images := make([]string, 0, len(r.Body.ASI))
	for image := range r.Body.ASI {
		images = append(images, image)
	}
	sort.Strings(images)
	var lines []string
	for _, image := range images {
		var messages []string
		if err := json.Unmarshal(r.Body.ASI[image], &messages); err != nil {
			messages = []string{string(r.Body.ASI[image])}
		}
		for _, m := range messages {
			lines = append(lines, image+": "+m)
		}
	}
	return lines
}
