// Package chatusage keeps the newest Claude subscription usage any chat
// session reported. Usage is per account, so one value serves every session.
// It lives in memory only: a daemon restart forgets it until the next event.
package chatusage

import (
	"sync"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

// Usage is a rate_limit_event's windows and the time the event was logged.
type Usage struct {
	chatproto.RateLimit
	UpdatedAt time.Time
}

// Store holds the newest Usage. The zero value is empty and ready to use.
type Store struct {
	mu     sync.Mutex
	latest Usage
	set    bool
}

// Record keeps rl unless the store already holds a newer event. Sessions
// replay their logs in any order, so an older event must not win.
func (s *Store) Record(rl chatproto.RateLimit, at time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.set && at.Before(s.latest.UpdatedAt) {
		return
	}
	s.latest, s.set = Usage{RateLimit: rl, UpdatedAt: at}, true
}

// Latest returns the newest Usage, and false before any event.
func (s *Store) Latest() (Usage, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.latest, s.set
}
