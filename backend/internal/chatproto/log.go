package chatproto

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"time"
)

// Direction says who wrote a log entry.
type Direction string

// Log directions.
const (
	DirOut  Direction = "out"  // claude's stdout
	DirIn   Direction = "in"   // the host's write to claude's stdin
	DirHost Direction = "host" // a lifecycle note from the host (see HostNote)
)

// Entry is one line of the append-only event log. Seq starts at 1 and
// increases by at least one per entry.
type Entry struct {
	Seq     uint64          `json:"seq"`
	TS      time.Time       `json:"ts"`
	Dir     Direction       `json:"dir"`
	Payload json.RawMessage `json:"payload"`
}

// DecodeLog reads a log written one Entry per line. It fails on invalid JSON,
// an unknown direction, or a seq that does not increase, and names the line.
func DecodeLog(r io.Reader) ([]Entry, error) {
	br := bufio.NewReader(r)
	var entries []Entry
	var last uint64
	for lineNo := 1; ; lineNo++ {
		line, readErr := br.ReadBytes('\n')
		if line = bytes.TrimSpace(line); len(line) > 0 {
			var e Entry
			if err := json.Unmarshal(line, &e); err != nil {
				return nil, fmt.Errorf("chatproto: log line %d: %w", lineNo, err)
			}
			switch e.Dir {
			case DirOut, DirIn, DirHost:
			default:
				return nil, fmt.Errorf("chatproto: log line %d: unknown dir %q", lineNo, e.Dir)
			}
			if e.Seq <= last {
				return nil, fmt.Errorf("chatproto: log line %d: seq %d does not follow %d", lineNo, e.Seq, last)
			}
			last = e.Seq
			entries = append(entries, e)
		}
		if errors.Is(readErr, io.EOF) {
			return entries, nil
		}
		if readErr != nil {
			return nil, fmt.Errorf("chatproto: read log: %w", readErr)
		}
	}
}

// HostNoteKind names a host lifecycle note. Unknown kinds are kept as written.
type HostNoteKind string

// Host note kinds.
const (
	NoteSpawn      HostNoteKind = "spawn"
	NoteExit       HostNoteKind = "exit"
	NoteUserOrigin HostNoteKind = "user_origin"
	NoteBashStart  HostNoteKind = "bash_start"
	NoteBashOutput HostNoteKind = "bash_output"
	NoteBashEnd    HostNoteKind = "bash_end"
)

// Origin says where a user input came from when it was not typed in the chat.
type Origin struct {
	Via  string `json:"via"` // "ao_send", or OriginRemoteControl for a prompt typed on the Remote Control side
	From string `json:"from"`
}

// OriginRemoteControl is the Via of a user row the folds add for a prompt typed
// on claude.ai/code or the Claude app. AO did not send it, so no host note
// names an origin and From is empty.
const OriginRemoteControl = "remote_control"

// HostNote is the parsed payload of a DirHost entry.
type HostNote struct {
	Kind   HostNoteKind
	UUID   string
	Origin *Origin
	Resume string
	Argv   []string
	Code   *int // nil when claude, or a "!" command for bash_end, died on a signal
	Bash   BashNote
	Raw    json.RawMessage
}

// BashNote is the body of a bash_start, bash_output or bash_end note, which
// follow one "!" command the person ran. ID names the run. Each kind sets its
// own fields; bash_end also sets HostNote.Code.
type BashNote struct {
	ID        string
	Command   string     // bash_start
	CWD       string     // bash_start
	Stream    BashStream // bash_output
	Text      string     // bash_output
	Signal    *string    // bash_end: the signal that ended it, nil when it exited
	Stopped   bool       // bash_end: stopped by the person, or because claude went away
	Truncated bool       // bash_end: output past the display cap was not logged
}

// ParseHostNote parses the payload of a DirHost entry.
func ParseHostNote(payload json.RawMessage) (HostNote, error) {
	var w struct {
		Kind   HostNoteKind `json:"kind"`
		UUID   string       `json:"uuid"`
		Origin *Origin      `json:"origin"`
		Resume string       `json:"resume"`
		Argv   []string     `json:"argv"`
		Code   *int         `json:"code"`

		ID        string     `json:"id"`
		Command   string     `json:"command"`
		CWD       string     `json:"cwd"`
		Stream    BashStream `json:"stream"`
		Text      string     `json:"text"`
		Signal    *string    `json:"signal"`
		Stopped   bool       `json:"stopped"`
		Truncated bool       `json:"truncated"`
	}
	if err := json.Unmarshal(payload, &w); err != nil {
		return HostNote{}, fmt.Errorf("chatproto: parse host note: %w", err)
	}
	if w.Kind == "" {
		return HostNote{}, errors.New("chatproto: parse host note: missing kind")
	}
	n := HostNote{Kind: w.Kind, UUID: w.UUID, Origin: w.Origin, Resume: w.Resume, Argv: w.Argv, Code: w.Code, Raw: bytes.Clone(payload)}
	switch w.Kind {
	case NoteBashStart, NoteBashOutput, NoteBashEnd:
		n.Bash = BashNote{
			ID: w.ID, Command: w.Command, CWD: w.CWD, Stream: w.Stream, Text: w.Text,
			Signal: w.Signal, Stopped: w.Stopped, Truncated: w.Truncated,
		}
	}
	return n, nil
}
