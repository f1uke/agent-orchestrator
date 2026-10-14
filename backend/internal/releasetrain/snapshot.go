package releasetrain

import (
	"sort"
	"time"
)

// Scope names one resource of a kind, e.g. "p:1001" or "v:5001".
type Scope string

// ErrorClass is why the last fetch of a resource failed.
type ErrorClass string

// Error classes a source can report.
const (
	ErrAuth         ErrorClass = "auth"
	ErrRateLimited  ErrorClass = "rate_limited"
	ErrUnavailable  ErrorClass = "unavailable"
	ErrNotFound     ErrorClass = "not_found"
	ErrDecode       ErrorClass = "decode"
	ErrTruncated    ErrorClass = "truncated"
	ErrUnconfigured ErrorClass = "unconfigured"
)

// ResourceStatus is what the core knows about one fetched resource besides its items.
type ResourceStatus struct {
	Kind          KindName      `json:"kind"`
	Scope         Scope         `json:"scope"`
	App           AppID         `json:"app,omitempty"`
	CompleteAt    *time.Time    `json:"completeAt,omitempty"`
	LastAttemptAt *time.Time    `json:"lastAttemptAt,omitempty"`
	Failures      int           `json:"failures"`
	ErrorClass    ErrorClass    `json:"errorClass,omitempty"`
	Budget        time.Duration `json:"budget,omitempty"`
}

func (st ResourceStatus) budget() time.Duration {
	if st.Budget > 0 {
		return st.Budget
	}
	return kinds[st.Kind].Budget
}

type resKey struct {
	kind  KindName
	scope Scope
}

type resource struct {
	status ResourceStatus
	items  []any
}

// Snapshot is every observed resource with its status and decoded items.
type Snapshot struct {
	res map[resKey]resource
}

// Put stores one resource's status and items, replacing what was there.
func Put[T any](s *Snapshot, k Kind[T], st ResourceStatus, items []T) {
	if s.res == nil {
		s.res = map[resKey]resource{}
	}
	st.Kind = k.Name
	boxed := make([]any, len(items))
	for i, it := range items {
		boxed[i] = it
	}
	s.res[resKey{k.Name, st.Scope}] = resource{status: st, items: boxed}
}

// Items returns the decoded items of one resource.
func Items[T any](s Snapshot, k Kind[T], scope Scope) []T {
	r, ok := s.res[resKey{k.Name, scope}]
	if !ok {
		return nil
	}
	return unbox[T](r.items)
}

// AllItems returns the items of every resource of a kind, ordered by scope.
func AllItems[T any](s Snapshot, k Kind[T]) []T {
	var out []T
	for _, st := range s.Statuses(k.Name) {
		out = append(out, unbox[T](s.res[resKey{k.Name, st.Scope}].items)...)
	}
	return out
}

func unbox[T any](items []any) []T {
	out := make([]T, 0, len(items))
	for _, it := range items {
		if v, ok := it.(T); ok {
			out = append(out, v)
		}
	}
	return out
}

// Status returns one resource's status.
func (s Snapshot) Status(kind KindName, scope Scope) (ResourceStatus, bool) {
	r, ok := s.res[resKey{kind, scope}]
	return r.status, ok
}

// Statuses returns the status of every resource of a kind, ordered by scope.
func (s Snapshot) Statuses(kind KindName) []ResourceStatus {
	var out []ResourceStatus
	for k, r := range s.res {
		if k.kind == kind {
			out = append(out, r.status)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Scope < out[j].Scope })
	return out
}

func (s Snapshot) withoutItems(kind KindName) Snapshot {
	out := Snapshot{res: make(map[resKey]resource, len(s.res))}
	for k, r := range s.res {
		if k.kind == kind {
			r.items = nil
		}
		out.res[k] = r
	}
	return out
}
