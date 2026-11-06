package sessionhost

import (
	"bytes"
	"fmt"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

type fakeClock struct {
	mu     sync.Mutex
	timers []*fakeTimer
}

type fakeTimer struct {
	d       time.Duration
	f       func()
	stopped bool
}

func (c *fakeClock) afterFunc(d time.Duration, f func()) func() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	t := &fakeTimer{d: d, f: f}
	c.timers = append(c.timers, t)
	return func() bool {
		c.mu.Lock()
		defer c.mu.Unlock()
		was := !t.stopped
		t.stopped = true
		return was
	}
}

func (c *fakeClock) fire(d time.Duration) {
	c.mu.Lock()
	var due []func()
	for _, t := range c.timers {
		if !t.stopped && t.d <= d {
			t.stopped = true
			due = append(due, t.f)
		}
	}
	c.mu.Unlock()
	for _, f := range due {
		f()
	}
}

type lockedBuffer struct {
	mu sync.Mutex
	b  bytes.Buffer
}

func (l *lockedBuffer) Write(p []byte) (int, error) {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.b.Write(p)
}

func (l *lockedBuffer) compacts() int {
	l.mu.Lock()
	defer l.mu.Unlock()
	return strings.Count(l.b.String(), `"content":"/compact"`)
}

func idleHost(t *testing.T) (*host, *fakeClock, *lockedBuffer) {
	t.Helper()
	h := bareHost(t)
	clock := &fakeClock{}
	stdin := &lockedBuffer{}
	h.stdin, h.afterFunc, h.contextTimeout = stdin, clock.afterFunc, time.Hour
	return h, clock, stdin
}

func assistantLine(contextTokens int, ttl string) string {
	created, oneHour, fiveMin := 1000, 0, 0
	if ttl == "1h" {
		oneHour = created
	} else {
		fiveMin = created
	}
	return fmt.Sprintf(`{"type":"assistant","session_id":"s","parent_tool_use_id":null,"message":{"id":"m1","role":"assistant","content":[{"type":"text","text":"ok"}],"usage":{"input_tokens":2,"cache_creation_input_tokens":%d,"cache_read_input_tokens":%d,"cache_creation":{"ephemeral_5m_input_tokens":%d,"ephemeral_1h_input_tokens":%d}}}}`,
		created, contextTokens-created-2, fiveMin, oneHour)
}

const turnResult = `{"type":"result","subtype":"success","session_id":"s","is_error":false,"num_turns":1,"total_cost_usd":0.1,"duration_ms":10}`

func turn(h *host, contextTokens int, ttl string) {
	h.handleLine([]byte(assistantLine(contextTokens, ttl)))
	h.handleLine([]byte(turnResult))
}

func TestIdleCompactionGates(t *testing.T) {
	cases := []struct {
		name  string
		setup func(h *host)
		fire  time.Duration
		want  int
	}{
		{"a large idle conversation on a 1h cache is compacted at 50 minutes", func(h *host) { turn(h, 250_000, "1h") }, 50 * time.Minute, 1},
		{"it is not compacted before 50 minutes", func(h *host) { turn(h, 250_000, "1h") }, 49 * time.Minute, 0},
		{"a context just under 200k is left alone", func(h *host) { turn(h, 199_999, "1h") }, time.Hour, 0},
		{"a context of exactly 200k is compacted", func(h *host) { turn(h, 200_000, "1h") }, time.Hour, 1},
		{"a 5-minute cache is left alone", func(h *host) { turn(h, 250_000, "5m") }, time.Hour, 0},
		{"a running background task holds it", func(h *host) {
			h.handleLine([]byte(`{"type":"system","subtype":"task_started","task_id":"b1","tool_use_id":"t1","session_id":"s"}`))
			turn(h, 250_000, "1h")
		}, time.Hour, 0},
		{"a background task that ended does not hold it", func(h *host) {
			h.handleLine([]byte(`{"type":"system","subtype":"task_started","task_id":"b1","tool_use_id":"t1","session_id":"s"}`))
			h.handleLine([]byte(`{"type":"system","subtype":"task_notification","task_id":"b1","status":"completed","session_id":"s"}`))
			turn(h, 250_000, "1h")
		}, time.Hour, 1},
		{"a pending permission request holds it", func(h *host) {
			turn(h, 250_000, "1h")
			h.handleLine([]byte(`{"type":"control_request","request_id":"r1","request":{"subtype":"can_use_tool","tool_name":"Bash","tool_use_id":"t1","input":{"command":"ls"}}}`))
		}, time.Hour, 0},
		{"a rate limit that is not allowed holds it", func(h *host) {
			h.handleLine([]byte(`{"type":"rate_limit_event","rate_limit_info":{"status":"allowed_warning","unifiedWindows":{}},"session_id":"s"}`))
			turn(h, 250_000, "1h")
		}, time.Hour, 0},
		{"user input after the turn cancels it", func(h *host) {
			turn(h, 250_000, "1h")
			if _, err := h.do(sendRequest{Input: chatproto.UserInput{Text: "one more thing"}}); err != nil {
				panic(err)
			}
		}, time.Hour, 0},
		{"a \"!\" command that has not settled holds it", func(h *host) {
			turn(h, 250_000, "1h")
			h.mu.Lock()
			h.noteLocked(bashStartNote{Kind: chatproto.NoteBashStart, ID: "b1", Command: "sleep 9", CWD: "/w"})
			h.mu.Unlock()
		}, time.Hour, 0},
		{"the project setting turns it off", func(h *host) {
			h.idleOff = true
			turn(h, 250_000, "1h")
		}, time.Hour, 0},
		{"a sub-agent's usage does not count as the context", func(h *host) {
			h.handleLine([]byte(strings.Replace(assistantLine(250_000, "1h"), `"parent_tool_use_id":null`, `"parent_tool_use_id":"t9"`, 1)))
			h.handleLine([]byte(turnResult))
		}, time.Hour, 0},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			h, clock, stdin := idleHost(t)
			tc.setup(h)
			clock.fire(tc.fire)
			if got := stdin.compacts(); got != tc.want {
				t.Fatalf("sent /compact %d times, want %d", got, tc.want)
			}
		})
	}
}

func TestIdleCompactionWaitsFromTheLastTurn(t *testing.T) {
	h, clock, stdin := idleHost(t)
	turn(h, 250_000, "1h")
	turn(h, 260_000, "1h")
	clock.fire(time.Hour)
	if got := stdin.compacts(); got != 1 {
		t.Fatalf("sent /compact %d times after two turns, want 1: only the last turn's timer may fire", got)
	}
}

func TestIdleCompactionStopsWhenClaudeExits(t *testing.T) {
	h, clock, stdin := idleHost(t)
	turn(h, 250_000, "1h")
	h.finish(nil, nil)
	clock.fire(time.Hour)
	if got := stdin.compacts(); got != 0 {
		t.Fatalf("sent /compact %d times after claude exited, want 0", got)
	}
}

func TestIdleCompactionDoesNotRepeatOverTheCompactedContext(t *testing.T) {
	h, clock, stdin := idleHost(t)
	turn(h, 250_000, "1h")
	clock.fire(time.Hour)
	for _, id := range h.folder.Transcript().Queued {
		h.handleLine([]byte(`{"type":"command_lifecycle","command_uuid":"` + id + `","state":"completed"}`))
	}
	h.handleLine([]byte(`{"type":"system","subtype":"compact_boundary","session_id":"s","compact_metadata":{"trigger":"manual","pre_tokens":250000,"post_tokens":30000}}`))
	h.handleLine([]byte(turnResult))
	clock.fire(time.Hour)
	if got := stdin.compacts(); got != 1 {
		t.Fatalf("sent /compact %d times, want 1: the compacted context is under the threshold", got)
	}
}
