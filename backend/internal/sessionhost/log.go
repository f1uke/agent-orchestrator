package sessionhost

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

type eventLog struct {
	path  string
	blobs chatblob.Store
	file  *os.File
	seq   uint64
	size  int64
	index []logPos
}

type logPos struct {
	seq uint64
	off int64
}

func openLog(path string) (*eventLog, *chatproto.Folder, error) {
	if err := os.MkdirAll(filepath.Dir(path), 0o700); err != nil {
		return nil, nil, fmt.Errorf("sessionhost: create log dir: %w", err)
	}
	data, err := os.ReadFile(path)
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		return nil, nil, fmt.Errorf("sessionhost: read log: %w", err)
	}
	if whole := bytes.LastIndexByte(data, '\n') + 1; whole < len(data) {
		if err := os.Truncate(path, int64(whole)); err != nil {
			return nil, nil, fmt.Errorf("sessionhost: cut torn log line: %w", err)
		}
		data = data[:whole]
	}
	entries, err := chatproto.DecodeLog(bytes.NewReader(data))
	if err != nil {
		return nil, nil, fmt.Errorf("sessionhost: %w", err)
	}
	folder := chatproto.NewFolder()
	for _, e := range entries {
		if err := folder.Apply(e); err != nil {
			return nil, nil, fmt.Errorf("sessionhost: fold log: %w", err)
		}
	}
	// 0600: the log holds the account email from claude's initialize answer.
	f, err := os.OpenFile(path, os.O_RDWR|os.O_CREATE|os.O_APPEND, 0o600)
	if err != nil {
		return nil, nil, fmt.Errorf("sessionhost: open log: %w", err)
	}
	lg := &eventLog{path: path, blobs: chatblob.ForLog(path), file: f, size: int64(len(data))}
	for i, off := range lineStarts(data) {
		lg.index = append(lg.index, logPos{seq: entries[i].Seq, off: off})
		lg.seq = entries[i].Seq
	}
	return lg, folder, nil
}

func (l *eventLog) append(e chatproto.Entry) ([]byte, chatproto.Entry, error) {
	payload, err := l.blobs.Externalize(e.Payload)
	if err != nil {
		return nil, e, fmt.Errorf("sessionhost: %w", err)
	}
	e.Payload = payload
	line, err := marshal(e)
	if err != nil {
		return nil, e, fmt.Errorf("sessionhost: encode entry: %w", err)
	}
	line = append(line, '\n')
	if _, err := l.file.Write(line); err != nil {
		_ = os.Truncate(l.path, l.size)
		return nil, e, fmt.Errorf("sessionhost: append log: %w", err)
	}
	l.seq = e.Seq
	l.index = append(l.index, logPos{seq: e.Seq, off: l.size})
	l.size += int64(len(line))
	return line, e, nil
}

func (l *eventLog) after(from uint64) ([][]byte, error) {
	i := sort.Search(len(l.index), func(i int) bool { return l.index[i].seq > from })
	if i == len(l.index) {
		return nil, nil
	}
	off := l.index[i].off
	data := make([]byte, l.size-off)
	if _, err := l.file.ReadAt(data, off); err != nil {
		return nil, fmt.Errorf("sessionhost: read log: %w", err)
	}
	var lines [][]byte
	for _, line := range bytes.Split(data, []byte("\n")) {
		if len(bytes.TrimSpace(line)) > 0 {
			lines = append(lines, line)
		}
	}
	return lines, nil
}

func lineStarts(data []byte) []int64 {
	var starts []int64
	for off := 0; off < len(data); {
		n := bytes.IndexByte(data[off:], '\n')
		if len(bytes.TrimSpace(data[off:off+n])) > 0 {
			starts = append(starts, int64(off))
		}
		off += n + 1
	}
	return starts
}

func marshal(v any) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}

const noteNonJSON chatproto.HostNoteKind = "non_json"

type spawnNote struct {
	Kind   chatproto.HostNoteKind `json:"kind"`
	Argv   []string               `json:"argv"`
	Resume string                 `json:"resume"`
	PID    int                    `json:"pid"`
}

type exitNote struct {
	Kind   chatproto.HostNoteKind `json:"kind"`
	Code   *int                   `json:"code"`
	Signal *string                `json:"signal"`
}

type originNote struct {
	Kind   chatproto.HostNoteKind `json:"kind"`
	UUID   string                 `json:"uuid"`
	Origin chatproto.Origin       `json:"origin"`
}

type nonJSONNote struct {
	Kind chatproto.HostNoteKind `json:"kind"`
	Text string                 `json:"text"`
}
