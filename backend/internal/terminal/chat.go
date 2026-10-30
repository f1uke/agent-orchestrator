package terminal

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"

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
		c.openChat(msg.ID, msg.FromSeq)
	case msgClose:
		c.closeChat(msg.ID)
	}
}

func (c *connState) openChat(id string, fromSeq uint64) {
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
		c.streamChat(ctx, s, id, fromSeq)
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

func (c *connState) streamChat(ctx context.Context, s *chatStream, id string, fromSeq uint64) {
	send := func(m serverMsg) bool {
		m.Ch, m.ID = chChat, id
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
	entries, err := chatLogAfter(logPath, fromSeq)
	if err != nil {
		end(serverMsg{Type: msgError, Error: err.Error()})
		return
	}
	last := fromSeq
	for i := range entries {
		if !send(serverMsg{Type: msgEntry, Entry: &entries[i]}) {
			return
		}
		last = entries[i].Seq
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

func chatLogAfter(path string, fromSeq uint64) ([]chatproto.Entry, error) {
	data, err := os.ReadFile(path)
	if errors.Is(err, fs.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("read chat log: %w", err)
	}
	entries, err := chatproto.DecodeLog(bytes.NewReader(completeLines(data)))
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

func completeLines(data []byte) []byte {
	return data[:bytes.LastIndexByte(data, '\n')+1]
}
