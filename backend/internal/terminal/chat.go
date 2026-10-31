package terminal

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatblob"
	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

// ChatSessions finds the sessions the chat channel serves. The session store
// satisfies it.
type ChatSessions interface {
	GetSession(ctx context.Context, id domain.SessionID) (domain.SessionRecord, bool, error)
}

type chatConfig struct {
	sessions ChatSessions
	dataDir  string
}

// WithChat serves the chat channel. sessions says which ids are chat
// sessions, and dataDir is where their hosts keep their event logs and
// sockets.
func WithChat(sessions ChatSessions, dataDir string) Option {
	return func(m *Manager) { m.chat = &chatConfig{sessions: sessions, dataDir: dataDir} }
}

type chatStream struct {
	cancel context.CancelFunc
	done   chan struct{}
}

func (s *chatStream) stop() {
	s.cancel()
	<-s.done
}

func (c *connState) handleChat(msg clientMsg) {
	switch msg.Type {
	case msgOpen:
		c.openChat(msg.ID, chatOpen{fromSeq: msg.FromSeq, blobRefs: msg.BlobRefs, tail: msg.Tail})
	case msgClose:
		c.closeChat(msg.ID)
	}
}

// chatOpen is what a client asked for when it opened a chat stream.
type chatOpen struct {
	fromSeq  uint64
	blobRefs bool
	// tail sends the last turns of a full replay first, then a history
	// marker, then the older entries, so the newest message shows at once.
	tail bool
}

func (c *connState) openChat(id string, open chatOpen) {
	if id == "" {
		c.enqueue(serverMsg{Ch: chChat, Type: msgError, Error: "missing session id"})
		return
	}
	c.mu.Lock()
	if _, ok := c.chats[id]; ok {
		c.mu.Unlock()
		return
	}
	ctx, cancel := context.WithCancel(c.ctx)
	s := &chatStream{cancel: cancel, done: make(chan struct{})}
	c.chats[id] = s
	c.mu.Unlock()

	go func() {
		defer close(s.done)
		defer cancel()
		c.streamChat(ctx, s, id, open)
	}()
}

func (c *connState) closeChat(id string) {
	c.mu.Lock()
	s := c.chats[id]
	delete(c.chats, id)
	c.mu.Unlock()
	if s != nil {
		s.stop()
	}
}

func (c *connState) streamChat(ctx context.Context, s *chatStream, id string, open chatOpen) {
	var blobs chatblob.Store
	send := func(m serverMsg) bool {
		m.Ch, m.ID = chChat, id
		if m.Entry != nil {
			e, err := shapeImages(blobs, *m.Entry, open.blobRefs)
			if err != nil {
				c.mgr.log.Warn("chat: entry images", "session", id, "seq", m.Entry.Seq, "err", err)
			}
			m.Entry = &e
		}
		select {
		case c.out <- m:
			return true
		case <-ctx.Done():
			return false
		}
	}
	end := func(m serverMsg) {
		c.mu.Lock()
		if c.chats[id] == s {
			delete(c.chats, id)
		}
		c.mu.Unlock()
		if ctx.Err() == nil {
			send(m)
		}
	}

	logPath, socket, err := c.mgr.chatPaths(ctx, id)
	if err != nil {
		end(serverMsg{Type: msgError, Error: err.Error()})
		return
	}
	blobs = chatblob.ForLog(logPath)
	data, err := readChatLog(logPath)
	if err != nil {
		end(serverMsg{Type: msgError, Error: err.Error()})
		return
	}
	cut := 0
	if open.tail && open.fromSeq == 0 {
		cut = tailStart(data, tailTurns)
	}
	newest, err := decodeAfter(data[cut:], open.fromSeq)
	if err != nil {
		end(serverMsg{Type: msgError, Error: err.Error()})
		return
	}
	last := open.fromSeq
	for i := range newest {
		if !send(serverMsg{Type: msgEntry, Entry: &newest[i]}) {
			return
		}
		last = newest[i].Seq
	}
	if cut > 0 {
		if !send(serverMsg{Type: msgHistory}) {
			return
		}
		older, err := chatproto.DecodeLog(bytes.NewReader(data[:cut]))
		if err != nil {
			end(serverMsg{Type: msgError, Error: err.Error()})
			return
		}
		for i := range older {
			if !send(serverMsg{Type: msgEntry, Entry: &older[i]}) {
				return
			}
		}
	}
	err = sessionhost.NewClient(socket).Subscribe(ctx, last, func() { send(serverMsg{Type: msgLive}) }, func(f sessionhost.Frame) error {
		m := serverMsg{Type: msgDelta, Delta: f.Delta}
		if f.Entry != nil {
			m = serverMsg{Type: msgEntry, Entry: f.Entry}
		}
		if !send(m) {
			return ctx.Err()
		}
		return nil
	})
	c.mgr.log.Debug("chat host stream ended", "session", id, "err", err)
	end(serverMsg{Type: msgOffline})
}

func (m *Manager) chatPaths(ctx context.Context, id string) (logPath, socketPath string, err error) {
	if m.chat == nil {
		return "", "", errors.New("chat sessions are not served on this connection")
	}
	rec, ok, err := m.chat.sessions.GetSession(ctx, domain.SessionID(id))
	switch {
	case err != nil:
		return "", "", err
	case !ok:
		return "", "", fmt.Errorf("unknown session %s", id)
	case rec.Transport.WithDefault() != domain.TransportChat:
		return "", "", fmt.Errorf("session %s is not a chat session", id)
	}
	logPath, socketPath = sessionhost.Paths(m.chat.dataDir, id)
	return logPath, socketPath, nil
}

// readChatLog returns the whole lines of the log, leaving out a line the host
// is still writing. A log not written yet is empty.
func readChatLog(path string) ([]byte, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read chat log: %w", err)
	}
	return data[:bytes.LastIndexByte(data, '\n')+1], nil
}

func decodeAfter(data []byte, fromSeq uint64) ([]chatproto.Entry, error) {
	entries, err := chatproto.DecodeLog(bytes.NewReader(data))
	if err != nil {
		return nil, err
	}
	for i, e := range entries {
		if e.Seq > fromSeq {
			return entries[i:], nil
		}
	}
	return nil, nil
}

func shapeImages(blobs chatblob.Store, e chatproto.Entry, refs bool) (chatproto.Entry, error) {
	var shaped json.RawMessage
	var err error
	if refs {
		shaped, err = blobs.Externalize(e.Payload)
	} else {
		shaped, err = blobs.Inline(e.Payload)
	}
	if err != nil {
		return e, err
	}
	e.Payload = shaped
	return e, nil
}

// tailTurns is how many of the newest human turns a tailed replay sends first.
const tailTurns = 3

// tailStart is the offset of the line that opens the nth-newest human turn
// (its user_origin note, or the user message itself), found from the end of
// the log without decoding the rest of it; 0 when the log holds n turns or
// fewer.
func tailStart(data []byte, n int) int {
	for end := len(data); end > 0; {
		start := bytes.LastIndexByte(data[:end-1], '\n') + 1
		if opensTurn(data[start:end]) {
			n--
			if n == 0 {
				if start > 0 {
					prev := bytes.LastIndexByte(data[:start-1], '\n') + 1
					if isOriginNote(data[prev:start]) {
						return prev
					}
				}
				return start
			}
		}
		end = start
	}
	return 0
}

type logLine struct {
	Dir     chatproto.Direction `json:"dir"`
	Payload struct {
		Type string                 `json:"type"`
		Kind chatproto.HostNoteKind `json:"kind"`
	} `json:"payload"`
}

func opensTurn(line []byte) bool {
	var w logLine
	return json.Unmarshal(line, &w) == nil && w.Dir == chatproto.DirIn && w.Payload.Type == "user"
}

func isOriginNote(line []byte) bool {
	var w logLine
	return json.Unmarshal(line, &w) == nil && w.Dir == chatproto.DirHost && w.Payload.Kind == chatproto.NoteUserOrigin
}
