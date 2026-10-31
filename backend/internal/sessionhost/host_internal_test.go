package sessionhost

import (
	"bytes"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

func bareHost(t *testing.T) *host {
	t.Helper()
	lg, folder, err := openLog(filepath.Join(t.TempDir(), "events.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = lg.file.Close() })
	return &host{pane: &paneWriter{w: io.Discard}, log: lg, folder: folder, subs: map[*subscriber]struct{}{}, stdin: io.Discard}
}

func frameSeq(t *testing.T, f []byte) uint64 {
	t.Helper()
	var w struct {
		Entry chatproto.Entry `json:"entry"`
	}
	if err := json.Unmarshal(f, &w); err != nil {
		t.Fatalf("frame %s: %v", f, err)
	}
	return w.Entry.Seq
}

func TestSubscribeNeitherMissesNorDoublesAnEntry(t *testing.T) {
	h := bareHost(t)
	line := []byte(`{"type":"system","subtype":"status","status":"thinking"}`)
	stop := make(chan struct{})
	var writers sync.WaitGroup
	for range 4 {
		writers.Add(1)
		go func() {
			defer writers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					h.handleLine(line)
				}
			}
		}()
	}
	defer func() {
		close(stop)
		writers.Wait()
	}()

	for range 500 {
		h.mu.Lock()
		from := h.log.seq
		h.mu.Unlock()
		if from > 2 {
			from -= 2
		}
		replay, s, err := h.replayAndRegister(from)
		if err != nil {
			t.Fatal(err)
		}
		last := from
		if n := len(replay); n > 0 {
			last = frameSeq(t, replay[n-1])
		}
		select {
		case f, ok := <-s.ch:
			if !ok {
				t.Fatal("subscriber dropped before its first live frame")
			}
			if got := frameSeq(t, f); got != last+1 {
				t.Fatalf("replay ended at seq %d but the first live frame is seq %d", last, got)
			}
		case <-time.After(testTimeout):
			t.Fatal("no live frame")
		}
		h.unsubscribe(s)
	}
}

func TestFullSubscriberIsDropped(t *testing.T) {
	h := bareHost(t)
	_, s, err := h.replayAndRegister(0)
	if err != nil {
		t.Fatal(err)
	}
	h.mu.Lock()
	for range subscriberBuffer + 1 {
		h.broadcastLocked([]byte("{}\n"))
	}
	_, still := h.subs[s]
	h.mu.Unlock()
	if still {
		t.Fatal("a subscriber with a full buffer is still registered")
	}
	n := 0
	for range s.ch {
		n++
	}
	if n != subscriberBuffer {
		t.Errorf("dropped subscriber kept %d frames, want the %d it had room for", n, subscriberBuffer)
	}
}

func TestStdoutLines(t *testing.T) {
	h := bareHost(t)
	_, s, err := h.replayAndRegister(0)
	if err != nil {
		t.Fatal(err)
	}
	verbatim := []byte(`{"type":"assistant","message":{"id":"m1","content":[{"type":"text","text":"<b>a & b</b> ünïcode"}]}}`)
	stream := []byte(`{"type":"stream_event","event":{"type":"content_block_delta","index":0,"delta":{"type":"text_delta","text":"hi"}}}`)
	h.handleLine(verbatim)
	h.handleLine([]byte("Warning: not json"))
	h.handleLine([]byte(`{"no_type":true}`))
	h.handleLine(stream)

	entries := loadEntries(t, h.log.path)
	if len(entries) != 3 {
		t.Fatalf("logged %d entries, want 3 (the stream event is not logged)", len(entries))
	}
	if !bytes.Equal(entries[0].Payload, verbatim) {
		t.Errorf("out payload %s, want claude's line byte for byte %s", entries[0].Payload, verbatim)
	}
	for i, text := range []string{"Warning: not json", `{"no_type":true}`} {
		e := entries[i+1]
		var note nonJSONNote
		if err := json.Unmarshal(e.Payload, &note); err != nil || e.Dir != chatproto.DirHost || note.Kind != noteNonJSON || note.Text != text {
			t.Errorf("entry %d = %s %s, want a non_json note of %q", e.Seq, e.Dir, e.Payload, text)
		}
	}
	if _, err := chatproto.Fold(entries); err != nil {
		t.Errorf("log does not fold: %v", err)
	}
	var frames [][]byte
	for len(s.ch) > 0 {
		frames = append(frames, <-s.ch)
	}
	if n := len(frames); n != 4 || !bytes.Equal(frames[3], frame("delta", stream)) {
		t.Errorf("frames %q, want three entries then the delta", frames)
	}
}

func TestOpenLogCutsATornLastLine(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.jsonl")
	whole := `{"seq":1,"ts":"2026-10-30T00:00:00Z","dir":"host","payload":{"kind":"spawn","argv":["claude"],"resume":"","pid":1}}` + "\n"
	if err := os.WriteFile(path, []byte(whole+`{"seq":2,"ts":"2026-10-30T00:00:01Z","dir":"out","payl`), 0o600); err != nil {
		t.Fatal(err)
	}
	lg, _, err := openLog(path)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = lg.file.Close() }()
	if lg.seq != 1 {
		t.Errorf("seq = %d, want 1", lg.seq)
	}
	if b, _ := os.ReadFile(path); string(b) != whole {
		t.Errorf("log = %q, want only the whole line", b)
	}
}

func TestImagesAreLoggedOnceAsBlobRefs(t *testing.T) {
	h := bareHost(t)
	_, s, err := h.replayAndRegister(0)
	if err != nil {
		t.Fatal(err)
	}
	const b64 = "iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAYAAAAfFcSJAAAADUlEQVR42mNk"
	read := []byte(`{"type":"user","message":{"role":"user","content":[{"tool_use_id":"t1","type":"tool_result","content":[{"type":"image","source":{"type":"base64","data":"` + b64 + `","media_type":"image/png"}}]}]},"tool_use_result":{"type":"image","file":{"base64":"` + b64 + `","type":"image/png"}}}`)
	h.handleLine(read)

	log, err := os.ReadFile(h.log.path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(log, []byte(b64)) {
		t.Fatalf("log carries the image bytes: %s", log)
	}
	blobs, err := os.ReadDir(filepath.Join(filepath.Dir(h.log.path), "blobs"))
	if err != nil || len(blobs) != 1 {
		t.Fatalf("blobs = %v, %v; want one file next to the log", blobs, err)
	}
	if n := bytes.Count(log, []byte(blobs[0].Name())); n != 2 {
		t.Errorf("log names the blob %d times, want 2 (the content source and tool_use_result.file)", n)
	}
	f := <-s.ch
	if bytes.Contains(f, []byte(b64)) || !bytes.Contains(f, []byte(`"type":"ao_blob"`)) {
		t.Errorf("live frame %s, want the logged ref", f)
	}
	if _, err := chatproto.Fold(loadEntries(t, h.log.path)); err != nil {
		t.Errorf("log does not fold: %v", err)
	}
}
