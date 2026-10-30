package chat

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"net"
	"os"
	"sync"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

type fakeSessions struct {
	recs map[domain.SessionID]domain.SessionRecord
	err  error
}

func (f fakeSessions) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := f.recs[id]
	return rec, ok, f.err
}

type stubHost struct {
	mu   sync.Mutex
	reqs []map[string]any
}

func startStubHost(t *testing.T, dataDir string, id domain.SessionID, reply string) *stubHost {
	t.Helper()
	_, socket, err := sessionhost.PreparePaths(dataDir, string(id))
	if err != nil {
		t.Fatal(err)
	}
	ln, err := net.Listen("unix", socket)
	if err != nil {
		t.Fatal(err)
	}
	h := &stubHost{}
	done := make(chan struct{})
	go func() {
		defer close(done)
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			line, _ := bufio.NewReader(conn).ReadBytes('\n')
			var req map[string]any
			_ = json.Unmarshal(line, &req)
			h.mu.Lock()
			h.reqs = append(h.reqs, req)
			h.mu.Unlock()
			_, _ = conn.Write([]byte(reply + "\n"))
			_ = conn.Close()
		}
	}()
	t.Cleanup(func() {
		_ = ln.Close()
		<-done
	})
	return h
}

func (h *stubHost) requests() []map[string]any {
	h.mu.Lock()
	defer h.mu.Unlock()
	return append([]map[string]any(nil), h.reqs...)
}

func shortDataDir(t *testing.T) string {
	t.Helper()
	dir, err := os.MkdirTemp("", "chat")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	return dir
}

func sessions(transport domain.Transport) fakeSessions {
	return fakeSessions{recs: map[domain.SessionID]domain.SessionRecord{"s-1": {ID: "s-1", Transport: transport}}}
}

type op struct {
	name string
	do   func(*Service) error
	want map[string]any
}

var ops = []op{
	{"send", func(s *Service) error {
		_, err := s.Send(context.Background(), "s-1", chatproto.UserInput{Text: "hi", Images: []chatproto.Image{{MediaType: "image/png", Base64: "aGk="}}})
		return err
	}, map[string]any{"op": "send", "text": "hi", "images": []any{map[string]any{"media_type": "image/png", "base64": "aGk="}}}},
	{"answer", func(s *Service) error {
		return s.Answer(context.Background(), "s-1", "r-1", chatproto.Deny{Message: "no", Interrupt: true})
	}, map[string]any{"op": "answer", "request_id": "r-1", "behavior": "deny", "message": "no", "interrupt": true}},
	{"interrupt", func(s *Service) error { return s.Interrupt(context.Background(), "s-1") }, map[string]any{"op": "interrupt"}},
	{"set mode", func(s *Service) error { return s.SetMode(context.Background(), "s-1", chatproto.ModePlan) }, map[string]any{"op": "set_mode", "mode": "plan"}},
	{"set model", func(s *Service) error { return s.SetModel(context.Background(), "s-1", "haiku") }, map[string]any{"op": "set_model", "model": "haiku"}},
}

func wantAPIError(t *testing.T, err error, kind apierr.Kind, code string) *apierr.Error {
	t.Helper()
	var e *apierr.Error
	if !errors.As(err, &e) || e.Kind != kind || e.Code != code {
		t.Fatalf("err = %v, want kind %d code %s", err, kind, code)
	}
	return e
}

func TestEachOperationReachesTheHostWithTheHumansRequest(t *testing.T) {
	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			dir := shortDataDir(t)
			host := startStubHost(t, dir, "s-1", `{"ok":true,"uuid":"u-1"}`)
			if err := tc.do(New(sessions(domain.TransportChat), dir)); err != nil {
				t.Fatal(err)
			}
			reqs := host.requests()
			if len(reqs) != 1 {
				t.Fatalf("host got %d requests, want 1", len(reqs))
			}
			want, _ := json.Marshal(tc.want)
			got, _ := json.Marshal(reqs[0])
			if string(got) != string(want) {
				t.Errorf("host got %s, want %s (a send from the chat carries no origin)", got, want)
			}
		})
	}
}

func TestSendReturnsTheHostsUUID(t *testing.T) {
	dir := shortDataDir(t)
	startStubHost(t, dir, "s-1", `{"ok":true,"uuid":"u-7"}`)
	uuid, err := New(sessions(domain.TransportChat), dir).Send(context.Background(), "s-1", chatproto.UserInput{Text: "hi"})
	if err != nil || uuid != "u-7" {
		t.Fatalf("Send = %q, %v; want u-7", uuid, err)
	}
}

func TestUnknownSession(t *testing.T) {
	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			svc := New(fakeSessions{}, shortDataDir(t))
			wantAPIError(t, tc.do(svc), apierr.KindNotFound, "SESSION_NOT_FOUND")
		})
	}
}

func TestNotAChatSessionNeverReachesAHost(t *testing.T) {
	for _, transport := range []domain.Transport{domain.TransportTerminal, ""} {
		for _, tc := range ops {
			t.Run(string(transport)+"/"+tc.name, func(t *testing.T) {
				dir := shortDataDir(t)
				host := startStubHost(t, dir, "s-1", `{"ok":true}`)
				wantAPIError(t, tc.do(New(sessions(transport), dir)), apierr.KindConflict, "NOT_CHAT_SESSION")
				if n := len(host.requests()); n != 0 {
					t.Errorf("host got %d requests for a session that is not a chat session", n)
				}
			})
		}
	}
}

func TestHostNotRunning(t *testing.T) {
	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			e := wantAPIError(t, tc.do(New(sessions(domain.TransportChat), shortDataDir(t))), apierr.KindConflict, "SESSION_HOST_NOT_RUNNING")
			if e.Message == "" {
				t.Error("no message for the person to read")
			}
		})
	}
}

func TestHostRefusalCarriesTheHostsReason(t *testing.T) {
	for _, tc := range ops {
		t.Run(tc.name, func(t *testing.T) {
			dir := shortDataDir(t)
			startStubHost(t, dir, "s-1", `{"ok":false,"error":"request r-1 is not pending"}`)
			e := wantAPIError(t, tc.do(New(sessions(domain.TransportChat), dir)), apierr.KindConflict, "CHAT_REQUEST_REJECTED")
			if e.Message != "request r-1 is not pending" {
				t.Errorf("message = %q, want the host's own text", e.Message)
			}
		})
	}
}

func TestStoreFailureIsNotAConflict(t *testing.T) {
	boom := errors.New("db down")
	_, err := New(fakeSessions{err: boom}, shortDataDir(t)).Send(context.Background(), "s-1", chatproto.UserInput{Text: "hi"})
	var e *apierr.Error
	if !errors.Is(err, boom) || errors.As(err, &e) {
		t.Fatalf("err = %v, want the store's error left to surface as a 500", err)
	}
}
