package terminal

import (
	"context"
	"encoding/base64"
	"sync"
	"testing"
	"time"
)

// recordingInputRecorder captures the terminal ids reported as typed into.
type recordingInputRecorder struct {
	mu  sync.Mutex
	ids []string
}

func (r *recordingInputRecorder) NoteInput(id string) {
	r.mu.Lock()
	r.ids = append(r.ids, id)
	r.mu.Unlock()
}

func (r *recordingInputRecorder) count(id string) int {
	r.mu.Lock()
	defer r.mu.Unlock()
	n := 0
	for _, got := range r.ids {
		if got == id {
			n++
		}
	}
	return n
}

func TestServeRecordsClientInput(t *testing.T) {
	pty := newFakePTY()
	sp := &fakeSpawner{ptys: []*fakePTY{pty}}
	src := &fakeSource{alive: true, spawner: sp}
	rec := &recordingInputRecorder{}
	mgr := NewManager(src, nil, testLogger(), WithHeartbeat(0), WithInputRecorder(rec))
	defer mgr.Close()

	conn := newFakeConn()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go mgr.Serve(ctx, conn)

	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgOpen}
	recv(t, conn, chTerminal, msgOpened, time.Second)

	// A keystroke frame is recorded as input for its pane id.
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("h"))}
	eventually(t, time.Second, func() bool { return rec.count("t1") == 1 })

	// An empty data frame is not counted as typing.
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: ""}
	// A resize frame is not input either.
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgResize, Rows: 30, Cols: 100}
	// A second real keystroke, so we can assert the count settled at exactly 2.
	conn.in <- clientMsg{Ch: chTerminal, ID: "t1", Type: msgData, Data: base64.StdEncoding.EncodeToString([]byte("i"))}
	eventually(t, time.Second, func() bool { return rec.count("t1") == 2 })
}
