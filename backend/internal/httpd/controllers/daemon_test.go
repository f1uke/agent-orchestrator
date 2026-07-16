package controllers

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/aoagents/agent-orchestrator/backend/internal/looptelemetry"
)

func TestListDaemonLoops_OK(t *testing.T) {
	reg := looptelemetry.New(func() time.Time { return time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC) })
	rec := reg.Register(looptelemetry.Spec{Name: "scm-observer", Display: "PR / CI polling", Description: "d", Interval: 30 * time.Second})
	rec.Tick()

	c := &DaemonController{Loops: reg}
	r := chi.NewRouter()
	c.Register(r)

	req := httptest.NewRequest(http.MethodGet, "/daemon/loops", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Fatalf("want 200, got %d: %s", w.Code, w.Body)
	}
	var got ListDaemonLoopsResponse
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(got.Loops) != 1 {
		t.Fatalf("want 1 loop, got %d", len(got.Loops))
	}
	l := got.Loops[0]
	if l.Name != "scm-observer" || l.DisplayName != "PR / CI polling" || l.IntervalMs != 30_000 {
		t.Fatalf("bad loop: %+v", l)
	}
	if l.NextRunAt == nil || l.LastRunAt == nil {
		t.Fatalf("timestamps should be set after a tick: %+v", l)
	}
	if !l.NextRunAt.Equal(l.LastRunAt.Add(30 * time.Second)) {
		t.Fatalf("nextRunAt should be last+interval, got last=%v next=%v", l.LastRunAt, l.NextRunAt)
	}
}

func TestListDaemonLoops_NeverRunOmitsTimestamps(t *testing.T) {
	reg := looptelemetry.New(func() time.Time { return time.Now().UTC() })
	reg.Register(looptelemetry.Spec{Name: "idle-sweep", Display: "Auto-close idle", Interval: 5 * time.Minute})

	c := &DaemonController{Loops: reg}
	r := chi.NewRouter()
	c.Register(r)
	req := httptest.NewRequest(http.MethodGet, "/daemon/loops", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)

	// omitempty must drop the timestamp keys entirely for a never-run loop.
	body := w.Body.String()
	if want := `"lastRunAt"`; contains(body, want) {
		t.Fatalf("never-run loop must omit lastRunAt, body=%s", body)
	}
}

func TestListDaemonLoops_NotImplementedWhenNilSource(t *testing.T) {
	c := &DaemonController{}
	r := chi.NewRouter()
	c.Register(r)
	req := httptest.NewRequest(http.MethodGet, "/daemon/loops", nil)
	w := httptest.NewRecorder()
	r.ServeHTTP(w, req)
	if w.Code != http.StatusNotImplemented {
		t.Fatalf("want 501, got %d", w.Code)
	}
}

func contains(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
