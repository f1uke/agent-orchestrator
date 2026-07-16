// Package looptelemetry is an in-memory registry that records the last-run time
// of each fixed-interval daemon background loop, so the API can surface a live
// countdown to each loop's next run. It holds no persisted state: everything is
// rebuilt from scratch on daemon boot and forgotten on shutdown.
package looptelemetry

import (
	"sort"
	"sync"
	"time"
)

// Spec declares a loop at registration time. Name is the stable machine id used
// as the map key and API field; Display and Description are human-facing copy.
type Spec struct {
	Name        string        // stable machine id, e.g. "scm-observer"
	Display     string        // human label, e.g. "PR / CI polling"
	Description string        // one-line hover copy: what this loop does
	Interval    time.Duration // fixed tick interval; <=0 means disabled
}

// LoopStatus is one loop's point-in-time timing, safe to serialize. LastRunAt
// and NextRunAt are nil until the loop has ticked at least once.
type LoopStatus struct {
	Name        string
	Display     string
	Description string
	IntervalMs  int64
	LastRunAt   *time.Time
	NextRunAt   *time.Time
	Running     bool
}

type loopState struct {
	spec      Spec
	lastRunAt time.Time
	hasRun    bool
}

// Registry is safe for concurrent Register/Tick/Snapshot.
type Registry struct {
	clock func() time.Time
	mu    sync.Mutex
	loops map[string]*loopState
}

// New builds a registry. clock defaults to time.Now().UTC when nil.
func New(clock func() time.Time) *Registry {
	if clock == nil {
		clock = func() time.Time { return time.Now().UTC() }
	}
	return &Registry{clock: clock, loops: map[string]*loopState{}}
}

// Register declares (or re-declares) a loop by Name and returns a Recorder the
// loop calls once per tick. Re-registering the same Name updates its spec while
// keeping any prior run history.
func (r *Registry) Register(s Spec) *Recorder {
	r.mu.Lock()
	defer r.mu.Unlock()
	st, ok := r.loops[s.Name]
	if !ok {
		st = &loopState{}
		r.loops[s.Name] = st
	}
	st.spec = s
	return &Recorder{reg: r, name: s.Name}
}

// Snapshot returns every loop's current timing, sorted by Name so the API
// response is stable across calls.
func (r *Registry) Snapshot() []LoopStatus {
	r.mu.Lock()
	defer r.mu.Unlock()
	out := make([]LoopStatus, 0, len(r.loops))
	for _, st := range r.loops {
		ls := LoopStatus{
			Name:        st.spec.Name,
			Display:     st.spec.Display,
			Description: st.spec.Description,
			IntervalMs:  st.spec.Interval.Milliseconds(),
			Running:     st.spec.Interval > 0,
		}
		if st.hasRun {
			last := st.lastRunAt
			ls.LastRunAt = &last
			next := last.Add(st.spec.Interval)
			ls.NextRunAt = &next
		}
		out = append(out, ls)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

// Recorder is the per-loop handle handed to a loop; Tick marks one run.
type Recorder struct {
	reg  *Registry
	name string
}

// Tick records that the loop ran now. Nil-safe so a caller can hold a nil
// *Recorder (telemetry disabled) and call Tick unconditionally.
func (rec *Recorder) Tick() {
	if rec == nil || rec.reg == nil {
		return
	}
	rec.reg.mu.Lock()
	defer rec.reg.mu.Unlock()
	if st, ok := rec.reg.loops[rec.name]; ok {
		st.lastRunAt = rec.reg.clock()
		st.hasRun = true
	}
}
