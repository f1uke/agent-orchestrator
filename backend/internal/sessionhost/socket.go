package sessionhost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"sync"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

const (
	subscriberBuffer = 256
	requestTimeout   = 30 * time.Second
	writeTimeout     = 10 * time.Second
)

type request interface{ isRequest() }

type sendRequest struct {
	Input  chatproto.UserInput
	Origin *chatproto.Origin
}

type answerRequest struct {
	RequestID string
	Decision  chatproto.Decision
}

type interruptRequest struct{}

type setModeRequest struct{ Mode chatproto.PermissionMode }

type stateRequest struct{}

type subscribeRequest struct{ FromSeq uint64 }

func (sendRequest) isRequest()      {}
func (answerRequest) isRequest()    {}
func (interruptRequest) isRequest() {}
func (setModeRequest) isRequest()   {}
func (stateRequest) isRequest()     {}
func (subscribeRequest) isRequest() {}

func parseRequest(line []byte) (request, error) {
	var w struct {
		Op     string `json:"op"`
		Text   string `json:"text"`
		Images []struct {
			MediaType string `json:"media_type"`
			Base64    string `json:"base64"`
		} `json:"images"`
		Origin             *chatproto.Origin `json:"origin"`
		RequestID          string            `json:"request_id"`
		Behavior           string            `json:"behavior"`
		UpdatedInput       json.RawMessage   `json:"updated_input"`
		UpdatedPermissions []json.RawMessage `json:"updated_permissions"`
		Message            string            `json:"message"`
		Interrupt          bool              `json:"interrupt"`
		Mode               string            `json:"mode"`
		FromSeq            uint64            `json:"from_seq"`
	}
	if err := json.Unmarshal(line, &w); err != nil {
		return nil, fmt.Errorf("bad request: %w", err)
	}
	switch w.Op {
	case "send":
		if w.Text == "" && len(w.Images) == 0 {
			return nil, errors.New("send needs text or images")
		}
		if w.Origin != nil && w.Origin.Via == "" {
			return nil, errors.New("send origin needs via")
		}
		in := chatproto.UserInput{Text: w.Text}
		for _, img := range w.Images {
			if img.MediaType == "" || img.Base64 == "" {
				return nil, errors.New("send image needs media_type and base64")
			}
			in.Images = append(in.Images, chatproto.Image{MediaType: img.MediaType, Base64: img.Base64})
		}
		return sendRequest{Input: in, Origin: w.Origin}, nil
	case "answer":
		if w.RequestID == "" {
			return nil, errors.New("answer needs request_id")
		}
		switch w.Behavior {
		case "allow":
			input := w.UpdatedInput
			if bytes.Equal(bytes.TrimSpace(input), []byte("null")) {
				input = nil
			}
			return answerRequest{RequestID: w.RequestID, Decision: chatproto.Allow{UpdatedInput: input, UpdatedPermissions: w.UpdatedPermissions}}, nil
		case "deny":
			return answerRequest{RequestID: w.RequestID, Decision: chatproto.Deny{Message: w.Message, Interrupt: w.Interrupt}}, nil
		}
		return nil, fmt.Errorf("answer behavior must be allow or deny, not %q", w.Behavior)
	case "interrupt":
		return interruptRequest{}, nil
	case "set_mode":
		mode, err := chatproto.ParsePermissionMode(w.Mode)
		if err != nil {
			return nil, err
		}
		return setModeRequest{Mode: mode}, nil
	case "state":
		return stateRequest{}, nil
	case "subscribe":
		return subscribeRequest{FromSeq: w.FromSeq}, nil
	}
	return nil, fmt.Errorf("unknown op %q", w.Op)
}

type errorResponse struct {
	OK    bool   `json:"ok"`
	Error string `json:"error"`
}

type okResponse struct {
	OK bool `json:"ok"`
}

type sendResponse struct {
	OK   bool   `json:"ok"`
	UUID string `json:"uuid"`
}

type stateResponse struct {
	OK              bool          `json:"ok"`
	ClaudeSessionID string        `json:"claude_session_id"`
	PID             int           `json:"pid"`
	Busy            bool          `json:"busy"`
	Queued          []string      `json:"queued"`
	Pending         []pendingJSON `json:"pending"`
	PermissionMode  string        `json:"permission_mode"`
	CostUSD         float64       `json:"cost_usd"`
	LastSeq         uint64        `json:"last_seq"`
}

type pendingJSON struct {
	RequestID   string                `json:"request_id"`
	ToolName    string                `json:"tool_name"`
	ToolUseID   string                `json:"tool_use_id"`
	Kind        chatproto.PendingKind `json:"kind"`
	Input       json.RawMessage       `json:"input"`
	Suggestions []json.RawMessage     `json:"suggestions"`
}

type server struct {
	h  *host
	ln net.Listener
	wg sync.WaitGroup

	mu     sync.Mutex
	conns  map[net.Conn]struct{}
	closed bool
}

func newServer(h *host, ln net.Listener) *server {
	return &server{h: h, ln: ln, conns: map[net.Conn]struct{}{}}
}

func (s *server) serve() {
	for {
		conn, err := s.ln.Accept()
		if err != nil {
			return
		}
		if !s.track(conn) {
			_ = conn.Close()
			return
		}
		s.wg.Add(1)
		go func() {
			defer s.wg.Done()
			defer s.untrack(conn)
			s.h.handleConn(conn)
		}()
	}
}

func (s *server) track(c net.Conn) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return false
	}
	s.conns[c] = struct{}{}
	return true
}

func (s *server) untrack(c net.Conn) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.conns, c)
	_ = c.Close()
}

func (s *server) close() {
	_ = s.ln.Close()
	s.mu.Lock()
	s.closed = true
	for c := range s.conns {
		_ = c.Close()
	}
	s.mu.Unlock()
	s.wg.Wait()
}

func (h *host) handleConn(conn net.Conn) {
	_ = conn.SetReadDeadline(time.Now().Add(requestTimeout))
	br := bufio.NewReader(conn)
	line, err := br.ReadBytes('\n')
	if err != nil && (!errors.Is(err, io.EOF) || len(bytes.TrimSpace(line)) == 0) {
		return
	}
	_ = conn.SetReadDeadline(time.Time{})
	req, err := parseRequest(line)
	if err != nil {
		respond(conn, errorResponse{Error: err.Error()})
		return
	}
	if sub, ok := req.(subscribeRequest); ok {
		h.serveSubscriber(conn, br, sub.FromSeq)
		return
	}
	resp, err := h.do(req)
	if err != nil {
		respond(conn, errorResponse{Error: err.Error()})
		return
	}
	respond(conn, resp)
}

func respond(conn net.Conn, v any) {
	b, err := marshal(v)
	if err != nil {
		b, _ = marshal(errorResponse{Error: err.Error()})
	}
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, _ = conn.Write(append(b, '\n'))
}

func (h *host) do(req request) (any, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	switch r := req.(type) {
	case sendRequest:
		in := r.Input
		in.UUID = uuid.NewString()
		if h.stdin == nil {
			return nil, errNotRunning
		}
		if r.Origin != nil {
			h.noteLocked(originNote{Kind: chatproto.NoteUserOrigin, UUID: in.UUID, Origin: *r.Origin})
		}
		if err := h.writeThenLogLocked(in); err != nil {
			return nil, err
		}
		return sendResponse{OK: true, UUID: in.UUID}, nil
	case answerRequest:
		return okResponse{OK: true}, h.answerLocked(r)
	case interruptRequest:
		return okResponse{OK: true}, h.writeThenLogLocked(chatproto.Interrupt{RequestID: uuid.NewString()})
	case setModeRequest:
		return okResponse{OK: true}, h.writeThenLogLocked(chatproto.SetPermissionMode{RequestID: uuid.NewString(), Mode: r.Mode})
	case stateRequest:
		return h.stateLocked(), nil
	}
	return nil, fmt.Errorf("unhandled request %T", req)
}

func (h *host) answerLocked(r answerRequest) error {
	var pending *chatproto.Pending
	for _, p := range h.folder.Transcript().Pending {
		if p.RequestID == r.RequestID {
			pending = &p
			break
		}
	}
	if pending == nil {
		return fmt.Errorf("request %s is not pending", r.RequestID)
	}
	d := r.Decision
	if a, ok := d.(chatproto.Allow); ok && len(a.UpdatedInput) == 0 {
		a.UpdatedInput = pending.Input
		d = a
	}
	return h.writeThenLogLocked(chatproto.PermissionAnswer{RequestID: r.RequestID, Decision: d})
}

func (h *host) stateLocked() stateResponse {
	t := h.folder.Transcript()
	resp := stateResponse{
		OK:             true,
		PID:            h.pid,
		Busy:           t.Busy,
		Queued:         append([]string{}, t.Queued...),
		Pending:        make([]pendingJSON, 0, len(t.Pending)),
		PermissionMode: t.PermissionMode,
		CostUSD:        t.CostUSD,
		LastSeq:        t.LastSeq,
	}
	if n := len(t.ClaudeSessionIDs); n > 0 {
		resp.ClaudeSessionID = t.ClaudeSessionIDs[n-1]
	}
	for _, p := range t.Pending {
		resp.Pending = append(resp.Pending, pendingJSON{
			RequestID: p.RequestID, ToolName: p.ToolName, ToolUseID: p.ToolUseID,
			Kind: p.Kind, Input: p.Input, Suggestions: p.Suggestions,
		})
	}
	return resp
}

type subscriber struct{ ch chan []byte }

func (h *host) replayAndRegister(from uint64) ([][]byte, *subscriber, error) {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.closed {
		return nil, nil, errNotRunning
	}
	lines, err := h.log.after(from)
	if err != nil {
		return nil, nil, err
	}
	replay := make([][]byte, 0, len(lines))
	for _, l := range lines {
		replay = append(replay, frame("entry", l))
	}
	s := &subscriber{ch: make(chan []byte, subscriberBuffer)}
	h.subs[s] = struct{}{}
	return replay, s, nil
}

func (h *host) unsubscribe(s *subscriber) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.dropLocked(s)
}

func (h *host) dropLocked(s *subscriber) {
	if _, ok := h.subs[s]; ok {
		delete(h.subs, s)
		close(s.ch)
	}
}

func (h *host) broadcastLocked(f []byte) {
	for s := range h.subs {
		select {
		case s.ch <- f:
		default:
			h.dropLocked(s)
		}
	}
}

func (h *host) serveSubscriber(conn net.Conn, br *bufio.Reader, from uint64) {
	replay, s, err := h.replayAndRegister(from)
	if err != nil {
		respond(conn, errorResponse{Error: err.Error()})
		return
	}
	defer h.unsubscribe(s)
	clientGone := make(chan struct{})
	go func() {
		defer close(clientGone)
		_, _ = io.Copy(io.Discard, br)
		h.unsubscribe(s)
	}()
	defer func() {
		_ = conn.Close()
		<-clientGone
	}()
	ok, _ := marshal(okResponse{OK: true})
	if !write(conn, append(ok, '\n')) {
		return
	}
	for _, f := range replay {
		if !write(conn, f) {
			return
		}
	}
	for f := range s.ch {
		if !write(conn, f) {
			return
		}
	}
}

func write(conn net.Conn, b []byte) bool {
	_ = conn.SetWriteDeadline(time.Now().Add(writeTimeout))
	_, err := conn.Write(b)
	return err == nil
}

func frame(key string, value []byte) []byte {
	b := make([]byte, 0, len(key)+len(value)+6)
	b = append(b, `{"`...)
	b = append(b, key...)
	b = append(b, `":`...)
	b = append(b, value...)
	return append(b, "}\n"...)
}
