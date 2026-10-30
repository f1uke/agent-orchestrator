package controllers_test

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apierr"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/controllers"
	chatsvc "github.com/aoagents/agent-orchestrator/backend/internal/service/chat"
)

type chatCall struct {
	op        string
	id        domain.SessionID
	input     chatproto.UserInput
	requestID string
	decision  chatproto.Decision
	mode      chatproto.PermissionMode
}

type fakeChatService struct {
	calls []chatCall
	uuid  string
	err   error
}

func (f *fakeChatService) Send(_ context.Context, id domain.SessionID, in chatproto.UserInput) (string, error) {
	f.calls = append(f.calls, chatCall{op: "send", id: id, input: in})
	return f.uuid, f.err
}

func (f *fakeChatService) Answer(_ context.Context, id domain.SessionID, requestID string, d chatproto.Decision) error {
	f.calls = append(f.calls, chatCall{op: "answer", id: id, requestID: requestID, decision: d})
	return f.err
}

func (f *fakeChatService) Interrupt(_ context.Context, id domain.SessionID) error {
	f.calls = append(f.calls, chatCall{op: "interrupt", id: id})
	return f.err
}

func (f *fakeChatService) SetMode(_ context.Context, id domain.SessionID, mode chatproto.PermissionMode) error {
	f.calls = append(f.calls, chatCall{op: "set_mode", id: id, mode: mode})
	return f.err
}

func newChatTestServer(t *testing.T, svc controllers.ChatService) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, httpd.APIDeps{Chat: svc}, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

type chatRoute struct {
	name, method, path, body string
}

var chatRoutes = []chatRoute{
	{"send", "POST", "/api/v1/sessions/s-1/chat/messages", `{"text":"hi"}`},
	{"answer", "POST", "/api/v1/sessions/s-1/chat/answers", `{"requestId":"r-1","behavior":"allow"}`},
	{"interrupt", "POST", "/api/v1/sessions/s-1/chat/interrupt", ``},
	{"mode", "PUT", "/api/v1/sessions/s-1/chat/mode", `{"mode":"plan"}`},
}

func TestChatSend(t *testing.T) {
	svc := &fakeChatService{uuid: "u-1"}
	srv := newChatTestServer(t, svc)

	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/s-1/chat/messages",
		`{"text":"look at this","images":[{"mediaType":"image/png","base64":"aGk="}]}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body=%s", status, body)
	}
	var resp controllers.SendChatMessageResponse
	mustJSON(t, body, &resp)
	if resp.UUID != "u-1" {
		t.Errorf("uuid = %q, want the service's", resp.UUID)
	}
	want := chatCall{op: "send", id: "s-1", input: chatproto.UserInput{
		Text: "look at this", Images: []chatproto.Image{{MediaType: "image/png", Base64: "aGk="}},
	}}
	if len(svc.calls) != 1 || !reflect.DeepEqual(svc.calls[0], want) {
		t.Errorf("service got %+v, want %+v", svc.calls, want)
	}
}

func TestChatAnswer(t *testing.T) {
	for _, tc := range []struct {
		name string
		body string
		want chatproto.Decision
	}{
		{"allow keeps the request's input", `{"requestId":"r-1","behavior":"allow"}`, chatproto.Allow{}},
		{"allow with edits stays raw JSON", `{"requestId":"r-1","behavior":"allow","updatedInput":{"command":"ls"},"updatedPermissions":[{"type":"addRules"}]}`,
			chatproto.Allow{UpdatedInput: json.RawMessage(`{"command":"ls"}`), UpdatedPermissions: []json.RawMessage{json.RawMessage(`{"type":"addRules"}`)}}},
		{"deny", `{"requestId":"r-1","behavior":"deny","message":"not that","interrupt":true}`, chatproto.Deny{Message: "not that", Interrupt: true}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeChatService{}
			srv := newChatTestServer(t, svc)
			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/s-1/chat/answers", tc.body)
			if status != http.StatusOK {
				t.Fatalf("status = %d, body=%s", status, body)
			}
			want := chatCall{op: "answer", id: "s-1", requestID: "r-1", decision: tc.want}
			if len(svc.calls) != 1 || !reflect.DeepEqual(svc.calls[0], want) {
				t.Errorf("service got %+v, want %+v", svc.calls, want)
			}
		})
	}
}

func TestChatInterruptAndMode(t *testing.T) {
	svc := &fakeChatService{}
	srv := newChatTestServer(t, svc)

	if body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/s-1/chat/interrupt", ""); status != http.StatusOK {
		t.Fatalf("interrupt status = %d, body=%s", status, body)
	}
	if body, status, _ := doRequest(t, srv, "PUT", "/api/v1/sessions/s-1/chat/mode", `{"mode":"acceptEdits"}`); status != http.StatusOK {
		t.Fatalf("mode status = %d, body=%s", status, body)
	}
	want := []chatCall{{op: "interrupt", id: "s-1"}, {op: "set_mode", id: "s-1", mode: chatproto.ModeAcceptEdits}}
	if !reflect.DeepEqual(svc.calls, want) {
		t.Errorf("service got %+v, want %+v", svc.calls, want)
	}
}

func TestChatBadRequestsNeverReachTheService(t *testing.T) {
	for _, tc := range []struct {
		name, method, path, body, code string
	}{
		{"send: not JSON", "POST", "messages", `{`, "INVALID_JSON"},
		{"send: no text", "POST", "messages", `{}`, "TEXT_REQUIRED"},
		{"send: blank text", "POST", "messages", `{"text":"  \n "}`, "TEXT_REQUIRED"},
		{"send: image without data", "POST", "messages", `{"text":"x","images":[{"mediaType":"image/png"}]}`, "IMAGE_INVALID"},
		{"send: image without type", "POST", "messages", `{"text":"x","images":[{"base64":"aGk="}]}`, "IMAGE_INVALID"},
		{"answer: not JSON", "POST", "answers", `[`, "INVALID_JSON"},
		{"answer: no request id", "POST", "answers", `{"behavior":"allow"}`, "REQUEST_ID_REQUIRED"},
		{"answer: no behavior", "POST", "answers", `{"requestId":"r-1"}`, "INVALID_BEHAVIOR"},
		{"answer: unknown behavior", "POST", "answers", `{"requestId":"r-1","behavior":"maybe"}`, "INVALID_BEHAVIOR"},
		{"mode: not JSON", "PUT", "mode", `{`, "INVALID_JSON"},
		{"mode: empty", "PUT", "mode", `{}`, "INVALID_MODE"},
		{"mode: unknown", "PUT", "mode", `{"mode":"yolo"}`, "INVALID_MODE"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			svc := &fakeChatService{}
			srv := newChatTestServer(t, svc)
			path := "/api/v1/sessions/s-1/chat/" + tc.path
			body, status, _ := doRequest(t, srv, tc.method, path, tc.body)
			assertErrorCode(t, body, status, http.StatusBadRequest, tc.code)
			if len(svc.calls) != 0 {
				t.Errorf("service was called for an invalid body: %+v", svc.calls)
			}
		})
	}
}

func TestChatServiceErrorsReachTheWire(t *testing.T) {
	for _, tc := range []struct {
		name     string
		err      error
		status   int
		code     string
		contains string
	}{
		{"unknown session", apierr.NotFound("SESSION_NOT_FOUND", "Unknown session"), http.StatusNotFound, "SESSION_NOT_FOUND", ""},
		{"not a chat session", apierr.Conflict("NOT_CHAT_SESSION", "Session is not a chat session", nil), http.StatusConflict, "NOT_CHAT_SESSION", ""},
		{"host down", chatsvc.HostNotRunning(), http.StatusConflict, "SESSION_HOST_NOT_RUNNING", "asleep or restarting"},
		{"host refused", apierr.Conflict("CHAT_REQUEST_REJECTED", "request r-1 is not pending", nil), http.StatusConflict, "CHAT_REQUEST_REJECTED", "request r-1 is not pending"},
	} {
		for _, route := range chatRoutes {
			t.Run(tc.name+"/"+route.name, func(t *testing.T) {
				srv := newChatTestServer(t, &fakeChatService{err: tc.err})
				body, status, _ := doRequest(t, srv, route.method, route.path, route.body)
				assertErrorCode(t, body, status, tc.status, tc.code)
				var got errorBody
				mustJSON(t, body, &got)
				if !strings.Contains(got.Message, tc.contains) {
					t.Errorf("message = %q, want it to contain %q", got.Message, tc.contains)
				}
			})
		}
	}
}

func TestChatRoutesWithoutServiceAnswer501(t *testing.T) {
	srv := newChatTestServer(t, nil)
	for _, route := range chatRoutes {
		body, status, _ := doRequest(t, srv, route.method, route.path, route.body)
		assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
	}
}

type chatStore map[domain.SessionID]domain.SessionRecord

func (s chatStore) GetSession(_ context.Context, id domain.SessionID) (domain.SessionRecord, bool, error) {
	rec, ok := s[id]
	return rec, ok, nil
}

func TestChatRoutesHostDownIs409WithTheRealService(t *testing.T) {
	dir, err := os.MkdirTemp("", "chat")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(dir) })
	store := chatStore{"s-1": {ID: "s-1", Transport: domain.TransportChat}}
	srv := newChatTestServer(t, chatsvc.New(store, dir))

	for _, route := range chatRoutes {
		t.Run(route.name, func(t *testing.T) {
			body, status, _ := doRequest(t, srv, route.method, route.path, route.body)
			assertErrorCode(t, body, status, http.StatusConflict, "SESSION_HOST_NOT_RUNNING")
		})
	}
}

func TestChatModeEnumNamesOnlyModesTheParserAccepts(t *testing.T) {
	field, ok := reflect.TypeOf(controllers.SetChatModeRequest{}).FieldByName("Mode")
	if !ok {
		t.Fatal("no Mode field")
	}
	values := strings.Split(field.Tag.Get("enum"), ",")
	if len(values) != 6 {
		t.Fatalf("enum = %v, want the six permission modes", values)
	}
	for _, v := range values {
		if _, err := chatproto.ParsePermissionMode(v); err != nil {
			t.Errorf("enum value %q is rejected by the parser: %v", v, err)
		}
	}
}
