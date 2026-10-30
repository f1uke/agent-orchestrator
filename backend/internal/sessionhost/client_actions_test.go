package sessionhost

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

func encoded(t *testing.T, o chatproto.Outbound) string {
	t.Helper()
	b, err := chatproto.Encode(o)
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func loggedInputs(t *testing.T, logPath, kind string) []string {
	t.Helper()
	var out []string
	for _, e := range loadEntries(t, logPath) {
		if e.Dir == chatproto.DirIn && messageKind(e.Payload) == kind {
			out = append(out, string(e.Payload))
		}
	}
	return out
}

func startPendingWrite(t *testing.T) (Config, Client, PendingRequest) {
	t.Helper()
	dir := shortDir(t)
	fixture := loadEntries(t, fixturePath(t, "first_session.jsonl"))
	cfg, _ := fakeConfig(t, dir, fixturePath(t, "first_session.jsonl"))
	cfg.PromptFile = writePrompt(t, dir, fixture)
	startHost(t, cfg)
	c := NewClient(cfg.SocketPath)
	var pending PendingRequest
	eventually(t, "the Write permission request", func() bool {
		st, err := c.State(context.Background())
		if err == nil && len(st.Pending) == 1 {
			pending = st.Pending[0]
		}
		return err == nil && len(st.Pending) == 1
	})
	return cfg, c, pending
}

func TestClientAnswer(t *testing.T) {
	cfg, c, pending := startPendingWrite(t)
	ctx := context.Background()

	if err := c.Answer(ctx, pending.RequestID, chatproto.Allow{}); err != nil {
		t.Fatal(err)
	}
	st, err := c.State(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Pending) != 0 {
		t.Errorf("pending after the answer = %+v, want none", st.Pending)
	}
	if got := loggedInputs(t, cfg.LogPath, "control_response/success"); len(got) != 1 || !strings.Contains(got[0], pending.RequestID) {
		t.Errorf("logged answers = %v, want one for %s", got, pending.RequestID)
	}

	err = c.Answer(ctx, pending.RequestID, chatproto.Allow{})
	var refused *RefusedError
	if !errors.Is(err, ErrRefused) || !errors.As(err, &refused) || refused.Reason != "request "+pending.RequestID+" is not pending" {
		t.Fatalf("second answer err = %v, want a refusal saying the request is not pending", err)
	}
	if errors.Is(err, ErrNotReachable) {
		t.Error("a refusal reads as not reachable")
	}
	if got := loggedInputs(t, cfg.LogPath, "control_response/success"); len(got) != 1 {
		t.Errorf("a refused answer reached claude: %d answers logged, want 1", len(got))
	}
}

func TestClientAnswerDeny(t *testing.T) {
	cfg, c, pending := startPendingWrite(t)

	if err := c.Answer(context.Background(), pending.RequestID, chatproto.Deny{Message: "not that file", Interrupt: true}); err != nil {
		t.Fatal(err)
	}
	got := loggedInputs(t, cfg.LogPath, "control_response/success")
	if len(got) != 1 || !strings.Contains(got[0], `"behavior":"deny"`) || !strings.Contains(got[0], "not that file") || !strings.Contains(got[0], `"interrupt":true`) {
		t.Errorf("logged answers = %v, want a deny carrying the message and interrupt", got)
	}
}

func TestClientInterruptSetModeAndImages(t *testing.T) {
	dir := shortDir(t)
	images := []chatproto.Image{{MediaType: "image/png", Base64: "aGk="}}
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir,
		fixtureLine{chatproto.DirIn, encoded(t, chatproto.Interrupt{RequestID: "i"})},
		fixtureLine{chatproto.DirIn, encoded(t, chatproto.SetPermissionMode{RequestID: "m", Mode: chatproto.ModePlan})},
		fixtureLine{chatproto.DirIn, encoded(t, chatproto.UserInput{UUID: "u", Text: "look", Images: images})},
	))
	startHost(t, cfg)
	c := NewClient(cfg.SocketPath)
	ctx := context.Background()

	if err := c.Interrupt(ctx); err != nil {
		t.Fatal(err)
	}
	if got := loggedInputs(t, cfg.LogPath, "control_request/interrupt"); len(got) != 1 {
		t.Errorf("logged interrupts = %v, want 1", got)
	}

	if err := c.SetMode(ctx, chatproto.ModePlan); err != nil {
		t.Fatal(err)
	}
	if got := loggedInputs(t, cfg.LogPath, "control_request/set_permission_mode"); len(got) != 1 || !strings.Contains(got[0], `"mode":"plan"`) {
		t.Errorf("logged mode changes = %v, want one to plan", got)
	}

	if _, err := c.Send(ctx, chatproto.UserInput{Text: "look", Images: images}, nil); err != nil {
		t.Fatal(err)
	}
	if got := loggedInputs(t, cfg.LogPath, "user/"); len(got) != 1 || !strings.Contains(got[0], `"media_type":"image/png"`) || !strings.Contains(got[0], `"data":"aGk="`) {
		t.Errorf("logged user messages = %v, want one carrying the image", got)
	}
}

func TestClientSetModeRefusal(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
	startHost(t, cfg)

	err := NewClient(cfg.SocketPath).SetMode(context.Background(), "yolo")
	var refused *RefusedError
	if !errors.Is(err, ErrRefused) || !errors.As(err, &refused) || !strings.Contains(refused.Reason, "unknown permission mode") {
		t.Fatalf("SetMode(yolo) err = %v, want the host's refusal", err)
	}
}

func TestClientWithNoHostIsNotReachable(t *testing.T) {
	c := NewClient(filepath.Join(shortDir(t), "none.sock"))
	ctx := context.Background()
	_, sendErr := c.Send(ctx, chatproto.UserInput{Text: "hi"}, nil)
	for name, err := range map[string]error{
		"Send":      sendErr,
		"Answer":    c.Answer(ctx, "r", chatproto.Allow{}),
		"Interrupt": c.Interrupt(ctx),
		"SetMode":   c.SetMode(ctx, chatproto.ModePlan),
	} {
		if !errors.Is(err, ErrNotReachable) {
			t.Errorf("%s err = %v, want ErrNotReachable", name, err)
		}
		if errors.Is(err, ErrRefused) {
			t.Errorf("%s: a missing host reads as a refusal", name)
		}
	}
}

func TestClientCancelledContextIsNotUnreachable(t *testing.T) {
	c := NewClient(filepath.Join(shortDir(t), "none.sock"))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	err := c.Interrupt(ctx)
	if !errors.Is(err, context.Canceled) || errors.Is(err, ErrNotReachable) {
		t.Fatalf("err = %v, want context.Canceled and not ErrNotReachable", err)
	}
}
