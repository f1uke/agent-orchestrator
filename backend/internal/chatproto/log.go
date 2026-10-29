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
)

// Origin says where a user input came from when it was not typed in the chat.
type Origin struct {
	Via  string `json:"via"` // "ao_send" today
	From string `json:"from"`
}

// HostNote is the parsed payload of a DirHost entry.
type HostNote struct {
	Kind   HostNoteKind
	UUID   string  // user_origin: the user input the origin belongs to
	Origin *Origin // user_origin
	Resume string  // spawn: the claude session id resumed, or empty
	Code   *int    // exit: the exit code, nil when claude died on a signal
	Raw    json.RawMessage
}

// ParseHostNote parses the payload of a DirHost entry.
func ParseHostNote(payload json.RawMessage) (HostNote, error) {
	var w struct {
		Kind   HostNoteKind `json:"kind"`
		UUID   string       `json:"uuid"`
		Origin *Origin      `json:"origin"`
		Resume string       `json:"resume"`
		Code   *int         `json:"code"`
	}
	if err := json.Unmarshal(payload, &w); err != nil {
		return HostNote{}, fmt.Errorf("chatproto: parse host note: %w", err)
	}
	if w.Kind == "" {
		return HostNote{}, errors.New("chatproto: parse host note: missing kind")
	}
	return HostNote{Kind: w.Kind, UUID: w.UUID, Origin: w.Origin, Resume: w.Resume, Code: w.Code, Raw: bytes.Clone(payload)}, nil
}
