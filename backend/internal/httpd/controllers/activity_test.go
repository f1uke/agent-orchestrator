package controllers_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/config"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd"
)

// Credential-shaped fixtures, assembled from fragments on purpose. They are
// fabricated, but a verbatim token literal in the source trips the repo's secret
// scanner (gitleaks) and a test fixture is not worth a false positive in CI.
// The split must fall inside the token's PREFIX, not its payload: some rules
// (Slack) make the payload optional and match the bare prefix alone. The
// assembled value is unchanged, so it still exercises the real redaction rule.
const fakeGitHubToken = "gh" + "p_ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789"

type fakeActivityStream struct {
	gotSession domain.SessionID
	ch         chan domain.ActivityEvent
}

func (f *fakeActivityStream) Subscribe(sessionID domain.SessionID) (<-chan domain.ActivityEvent, func()) {
	f.gotSession = sessionID
	if f.ch == nil {
		f.ch = make(chan domain.ActivityEvent, 4)
	}
	return f.ch, func() {}
}

type fakeActivityFeed struct {
	events []domain.ActivityEvent
}

func (f *fakeActivityFeed) Publish(_ context.Context, ev domain.ActivityEvent) error {
	f.events = append(f.events, ev)
	return nil
}

func newActivityFeedServer(t *testing.T, deps httpd.APIDeps) *httptest.Server {
	t.Helper()
	log := slog.New(slog.NewTextHandler(io.Discard, nil))
	srv := httptest.NewServer(httpd.NewRouterWithControl(config.Config{}, log, nil, deps, httpd.ControlDeps{}))
	t.Cleanup(srv.Close)
	return srv
}

func TestActivityAPI_StreamsCuratedEvents(t *testing.T) {
	stream := &fakeActivityStream{ch: make(chan domain.ActivityEvent, 1)}
	srv := newActivityFeedServer(t, httpd.APIDeps{ActivityStream: stream})

	resp, err := srv.Client().Get(srv.URL + "/api/v1/activity/stream?sessionId=ao-7")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("status = %d, want 200", resp.StatusCode)
	}
	if ct := resp.Header.Get("Content-Type"); !strings.HasPrefix(ct, "text/event-stream") {
		t.Fatalf("content-type = %q", ct)
	}
	if stream.gotSession != "ao-7" {
		t.Fatalf("session filter = %q, want ao-7", stream.gotSession)
	}

	stream.ch <- domain.ActivityEvent{
		SessionID: "ao-7", Kind: domain.ActivityEventToolStart,
		At:   time.Date(2026, 7, 22, 9, 14, 3, 421_000_000, time.UTC),
		Tool: "Bash", Text: "Running the test suite",
		TTLMs:  domain.DurationMs(domain.ToolStartDetailTTL),
		Coarse: domain.CoarseWorking, CoarseTTLMs: domain.DurationMs(domain.CoarseWorkingTTL),
	}
	reader := bufio.NewReader(resp.Body)
	eventLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	dataLine, err := reader.ReadString('\n')
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(eventLine) != "event: activity" {
		t.Fatalf("eventLine = %q", eventLine)
	}
	for _, want := range []string{
		`"sessionId":"ao-7"`, `"kind":"tool_start"`, `"at":"2026-07-22T09:14:03.421Z"`,
		`"tool":"Bash"`, `"text":"Running the test suite"`,
		`"ttlMs":20000`, `"coarse":"working"`, `"coarseTtlMs":600000`,
	} {
		if !strings.Contains(dataLine, want) {
			t.Errorf("frame missing %s:\n%s", want, dataLine)
		}
	}
}

// An overlay watching every session subscribes with no filter.
func TestActivityAPI_StreamDefaultsToAllSessions(t *testing.T) {
	stream := &fakeActivityStream{ch: make(chan domain.ActivityEvent, 1)}
	srv := newActivityFeedServer(t, httpd.APIDeps{ActivityStream: stream})
	resp, err := srv.Client().Get(srv.URL + "/api/v1/activity/stream")
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = resp.Body.Close() }()
	if stream.gotSession != "" {
		t.Errorf("session filter = %q, want the all-sessions subscription", stream.gotSession)
	}
}

func TestActivityAPI_StreamWithoutPublisherIs501(t *testing.T) {
	srv := newActivityFeedServer(t, httpd.APIDeps{})
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/activity/stream", "")
	assertErrorCode(t, body, status, http.StatusNotImplemented, "NOT_IMPLEMENTED")
}

// A tool hook's curated detail becomes a feed frame carrying BOTH the detail TTL
// and the coarse level it decays to.
func TestActivityAPI_ToolDetailBecomesAFeedFrame(t *testing.T) {
	cases := []struct {
		body        string
		wantKind    domain.ActivityEventKind
		wantTTL     int64
		wantCoarse  domain.ActivityCoarse
		wantCoarseT int64
	}{
		{
			body:     `{"state":"active","detail":{"kind":"tool_start","tool":"Bash","text":"Running the test suite"}}`,
			wantKind: domain.ActivityEventToolStart, wantTTL: domain.DurationMs(domain.ToolStartDetailTTL),
			wantCoarse: domain.CoarseWorking, wantCoarseT: domain.DurationMs(domain.CoarseWorkingTTL),
		},
		{
			body:     `{"state":"active","detail":{"kind":"tool_failed","tool":"Bash","text":"Running the test suite"}}`,
			wantKind: domain.ActivityEventToolFailed, wantTTL: domain.DurationMs(domain.ToolFailedDetailTTL),
			wantCoarse: domain.CoarseWorking, wantCoarseT: domain.DurationMs(domain.CoarseWorkingTTL),
		},
	}
	for _, tc := range cases {
		t.Run(string(tc.wantKind), func(t *testing.T) {
			feed := &fakeActivityFeed{}
			srv := newActivityFeedServer(t, httpd.APIDeps{Activity: &fakeActivityRecorder{}, ActivityFeed: feed})

			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-7/activity", tc.body)
			if status != http.StatusOK {
				t.Fatalf("status = %d, body = %s", status, body)
			}
			if len(feed.events) != 1 {
				t.Fatalf("published %d events, want 1", len(feed.events))
			}
			ev := feed.events[0]
			if ev.SessionID != "ao-7" || ev.Kind != tc.wantKind || ev.Tool != "Bash" || ev.Text != "Running the test suite" {
				t.Errorf("event = %+v", ev)
			}
			if ev.TTLMs != tc.wantTTL || ev.Coarse != tc.wantCoarse || ev.CoarseTTLMs != tc.wantCoarseT {
				t.Errorf("decay contract wrong: ttl=%d coarse=%q coarseTtl=%d", ev.TTLMs, ev.Coarse, ev.CoarseTTLMs)
			}
			if ev.At.IsZero() {
				t.Error("every event must be timestamped or the consumer cannot decay it")
			}
		})
	}
}

// A harness with no per-tool hook posts a bare state: the frame carries the
// coarse level and no detail at all, so nothing can read as a live action.
func TestActivityAPI_BareStateBecomesACoarseOnlyFrame(t *testing.T) {
	cases := []struct {
		state       string
		wantCoarse  domain.ActivityCoarse
		wantCoarseT int64
	}{
		{"active", domain.CoarseWorking, domain.DurationMs(domain.CoarseWorkingTTL)},
		{"idle", domain.CoarseIdle, domain.DurationMs(domain.CoarseIdleTTL)},
		{"waiting_input", domain.CoarseWaiting, 0},
		{"exited", domain.CoarseExited, 0},
	}
	for _, tc := range cases {
		t.Run(tc.state, func(t *testing.T) {
			feed := &fakeActivityFeed{}
			srv := newActivityFeedServer(t, httpd.APIDeps{Activity: &fakeActivityRecorder{}, ActivityFeed: feed})

			body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-7/activity", `{"state":"`+tc.state+`"}`)
			if status != http.StatusOK {
				t.Fatalf("status = %d, body = %s", status, body)
			}
			if len(feed.events) != 1 {
				t.Fatalf("published %d events, want 1", len(feed.events))
			}
			ev := feed.events[0]
			if ev.Kind != domain.ActivityEventActivity {
				t.Errorf("Kind = %q, want activity", ev.Kind)
			}
			if ev.TTLMs != 0 || ev.Tool != "" || ev.Target != "" || ev.Text != "" {
				t.Errorf("a status-only frame must carry no detail: %+v", ev)
			}
			if ev.Coarse != tc.wantCoarse || ev.CoarseTTLMs != tc.wantCoarseT {
				t.Errorf("coarse = (%q, %d), want (%q, %d)", ev.Coarse, ev.CoarseTTLMs, tc.wantCoarse, tc.wantCoarseT)
			}
		})
	}
}

// The daemon is the backstop for the whitelist that already ran in the hook
// process: an over-long or secret-shaped detail is clamped and redacted here
// too, and a bogus kind is dropped to a coarse-only frame.
func TestActivityAPI_ReSanitizesDetailFromTheWire(t *testing.T) {
	feed := &fakeActivityFeed{}
	srv := newActivityFeedServer(t, httpd.APIDeps{Activity: &fakeActivityRecorder{}, ActivityFeed: feed})

	huge, err := json.Marshal(strings.Repeat("z", 4000) + " " + fakeGitHubToken)
	if err != nil {
		t.Fatal(err)
	}
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-7/activity",
		`{"state":"active","detail":{"kind":"tool_start","tool":"Bash","text":`+string(huge)+`}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	ev := feed.events[0]
	if len([]rune(ev.Text)) > domain.ActivityTextMaxRunes {
		t.Errorf("text not clamped: %d runes", len([]rune(ev.Text)))
	}
	if strings.Contains(ev.Text, "ghp_") {
		t.Errorf("secret not redacted daemon-side: %q", ev.Text)
	}

	feed.events = nil
	body, status, _ = doRequest(t, srv, "POST", "/api/v1/sessions/ao-7/activity",
		`{"state":"active","detail":{"kind":"exfiltrate","text":"whatever"}}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	if got := feed.events[0]; got.Kind != domain.ActivityEventActivity || got.Text != "" {
		t.Errorf("an unknown detail kind must degrade to a coarse-only frame, got %+v", got)
	}
}

// A rejected activity signal must publish nothing: the feed reports facts, and a
// 400 is not one.
func TestActivityAPI_InvalidStatePublishesNothing(t *testing.T) {
	feed := &fakeActivityFeed{}
	srv := newActivityFeedServer(t, httpd.APIDeps{Activity: &fakeActivityRecorder{}, ActivityFeed: feed})

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-7/activity", `{"state":"nonsense"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if len(feed.events) != 0 {
		t.Errorf("published %d events for a rejected signal, want 0", len(feed.events))
	}
}

func TestActivityAPI_FailedSignalPublishesNothing(t *testing.T) {
	feed := &fakeActivityFeed{}
	rec := &fakeActivityRecorder{err: context.DeadlineExceeded}
	srv := newActivityFeedServer(t, httpd.APIDeps{Activity: rec, ActivityFeed: feed})

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-7/activity", `{"state":"active"}`)
	if status == http.StatusOK {
		t.Fatalf("status = %d, want a failure", status)
	}
	if len(feed.events) != 0 {
		t.Errorf("published %d events although the signal failed, want 0", len(feed.events))
	}
}

// The ao send tap. Briefs are long and can carry paths and credentials, so only
// a truncated, redacted first line reaches the feed — and a message does NOT
// touch the coarse level: it says something flew by, not how busy the agent is.
// (inputgate holds an injected message for up to 8s, so "sent" is true
// immediately while "received" is not.)
func TestActivityAPI_SendPublishesATruncatedMessageFrame(t *testing.T) {
	feed := &fakeActivityFeed{}
	svc := &fakeSessionService{}
	srv := newActivityFeedServer(t, httpd.APIDeps{Sessions: svc, ActivityFeed: feed})

	brief, err := json.Marshal("[from @ao-1] continue with slice 2\nAPI_KEY=abcdefghijklmnopqrstuvwx\n" + strings.Repeat("detail ", 100))
	if err != nil {
		t.Fatal(err)
	}
	body, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-7/send", `{"message":`+string(brief)+`}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d, body = %s", status, body)
	}
	if len(feed.events) != 1 {
		t.Fatalf("published %d events, want 1", len(feed.events))
	}
	ev := feed.events[0]
	if ev.Kind != domain.ActivityEventMessage || ev.SessionID != "ao-7" {
		t.Errorf("event = %+v", ev)
	}
	if ev.Text != "[from @ao-1] continue with slice 2" {
		t.Errorf("Text = %q, want the first line only", ev.Text)
	}
	if strings.Contains(ev.Text, "abcdefghijklmnopqrstuvwx") || strings.Contains(ev.Text, "detail") {
		t.Errorf("message body leaked into the feed: %q", ev.Text)
	}
	if ev.TTLMs != domain.DurationMs(domain.MessageDetailTTL) {
		t.Errorf("ttlMs = %d, want %d", ev.TTLMs, domain.DurationMs(domain.MessageDetailTTL))
	}
	if ev.Coarse != "" || ev.CoarseTTLMs != 0 {
		t.Errorf("a message must not move the coarse level, got (%q, %d)", ev.Coarse, ev.CoarseTTLMs)
	}
}

func TestActivityAPI_RejectedSendPublishesNothing(t *testing.T) {
	feed := &fakeActivityFeed{}
	srv := newActivityFeedServer(t, httpd.APIDeps{Sessions: &fakeSessionService{}, ActivityFeed: feed})

	_, status, _ := doRequest(t, srv, "POST", "/api/v1/sessions/ao-7/send", `{"message":""}`)
	if status != http.StatusBadRequest {
		t.Fatalf("status = %d, want 400", status)
	}
	if len(feed.events) != 0 {
		t.Errorf("published %d events for a rejected send, want 0", len(feed.events))
	}
}
