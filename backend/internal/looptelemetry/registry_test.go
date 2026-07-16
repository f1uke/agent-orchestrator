package looptelemetry

import (
	"testing"
	"time"
)

func fixedClock(t time.Time) func() time.Time { return func() time.Time { return t } }

func TestRegisterThenNeverRun_NextRunNil(t *testing.T) {
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	r := New(fixedClock(base))
	r.Register(Spec{Name: "scm", Display: "PR / CI polling", Description: "d", Interval: 30 * time.Second})
	got := r.Snapshot()
	if len(got) != 1 {
		t.Fatalf("want 1 loop, got %d", len(got))
	}
	l := got[0]
	if l.LastRunAt != nil || l.NextRunAt != nil {
		t.Fatalf("never-run loop must have nil timestamps, got last=%v next=%v", l.LastRunAt, l.NextRunAt)
	}
	if !l.Running || l.IntervalMs != 30_000 {
		t.Fatalf("want running interval 30000ms, got running=%v ms=%d", l.Running, l.IntervalMs)
	}
	if l.Display != "PR / CI polling" || l.Description != "d" {
		t.Fatalf("display/description not carried: %+v", l)
	}
}

func TestTick_SetsLastAndDerivesNext(t *testing.T) {
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	now := base
	r := New(func() time.Time { return now })
	rec := r.Register(Spec{Name: "scm", Interval: 30 * time.Second})
	now = base.Add(5 * time.Second)
	rec.Tick()
	l := r.Snapshot()[0]
	if l.LastRunAt == nil || !l.LastRunAt.Equal(base.Add(5*time.Second)) {
		t.Fatalf("lastRunAt wrong: %v", l.LastRunAt)
	}
	if l.NextRunAt == nil || !l.NextRunAt.Equal(base.Add(35*time.Second)) {
		t.Fatalf("nextRunAt should be last+interval, got %v", l.NextRunAt)
	}
}

func TestDisabledInterval_NotRunning(t *testing.T) {
	r := New(fixedClock(time.Now().UTC()))
	r.Register(Spec{Name: "idle", Interval: 0})
	if r.Snapshot()[0].Running {
		t.Fatal("interval<=0 must be Running=false")
	}
}

func TestReRegisterKeepsHistory(t *testing.T) {
	base := time.Date(2026, 7, 16, 12, 0, 0, 0, time.UTC)
	r := New(fixedClock(base))
	rec := r.Register(Spec{Name: "scm", Interval: 30 * time.Second})
	rec.Tick()
	r.Register(Spec{Name: "scm", Display: "renamed", Interval: 30 * time.Second})
	l := r.Snapshot()[0]
	if l.Display != "renamed" {
		t.Fatalf("re-register should update spec, got %q", l.Display)
	}
	if l.LastRunAt == nil {
		t.Fatal("re-register must keep prior run history")
	}
}

func TestNilRecorderTick_NoPanic(t *testing.T) {
	var rec *Recorder
	rec.Tick() // must not panic
}

func TestSnapshotStableOrder(t *testing.T) {
	r := New(fixedClock(time.Now().UTC()))
	r.Register(Spec{Name: "zeta", Interval: time.Second})
	r.Register(Spec{Name: "alpha", Interval: time.Second})
	s := r.Snapshot()
	if s[0].Name != "alpha" || s[1].Name != "zeta" {
		t.Fatalf("snapshot not name-sorted: %v", s)
	}
}
