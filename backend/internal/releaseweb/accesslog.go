package releaseweb

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

// accessEvent is one line kind in access.log.
type accessEvent string

const (
	eventSigninOK              accessEvent = "signin_ok"
	eventSigninFailed          accessEvent = "signin_failed"
	eventSigninLocked          accessEvent = "signin_locked"
	eventSigninLockedRepeated  accessEvent = "signin_locked_repeated"
	eventSignout               accessEvent = "signout"
	eventSessionRejectedRotate accessEvent = "session_rejected_rotated"
)

const (
	// MaxAccessLogBytes caps access.log. Past it the file moves to access.log.1,
	// replacing the one before, so the two never hold more than twice this.
	MaxAccessLogBytes = 10 << 20
	// LockedLogInterval is how often one client's refused attempts are written:
	// the first one, then a count of the rest once the interval is over.
	LockedLogInterval = time.Minute
	// anyClient stands for the clients whose refused attempts were counted
	// together because too many were being counted one by one.
	anyClient = "*"
)

// accessLine is the whole of what access.log records: never the key, a token,
// a form body or a query string.
type accessLine struct {
	Time  time.Time   `json:"time"`
	IP    string      `json:"ip"`
	Event accessEvent `json:"event"`
	// Count is how many refused attempts a signin_locked_repeated line stands for.
	Count int `json:"count,omitempty"`
}

// lockedRun is one client's refused attempts since its last signin_locked line.
type lockedRun struct {
	ip      string
	since   time.Time
	repeats int
}

type accessLog struct {
	mu    sync.Mutex
	path  string
	f     *os.File
	size  int64
	limit int64
	// runs coalesces refused attempts by rate-limit bucket, so a flood against
	// a locked sign-in writes a line per client per LockedLogInterval.
	runs map[string]*lockedRun
	// overflow counts refused attempts from clients runs had no room for.
	overflow int
}

func openAccessLog(path string) (*accessLog, error) {
	a := &accessLog{path: path, limit: MaxAccessLogBytes, runs: map[string]*lockedRun{}}
	if err := a.open(); err != nil {
		return nil, err
	}
	return a, nil
}

func (a *accessLog) open() error {
	f, err := os.OpenFile(a.path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return fmt.Errorf("release web: open access log: %w", err)
	}
	st, err := f.Stat()
	if err == nil {
		err = f.Chmod(0o600)
	}
	if err != nil {
		_ = f.Close()
		return fmt.Errorf("release web: access log: %w", err)
	}
	a.f, a.size = f, st.Size()
	return nil
}

// record appends one line.
func (a *accessLog) record(now time.Time, ip string, ev accessEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writeLocked(accessLine{Time: now.UTC(), IP: ip, Event: ev})
}

// recordLocked notes a refused sign-in. Only the first per client within
// LockedLogInterval is written; the rest are counted and written as one
// signin_locked_repeated line when the interval is over.
func (a *accessLog) recordLocked(now time.Time, c client) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if run, ok := a.runs[c.bucket]; ok {
		if now.Sub(run.since) < LockedLogInterval {
			run.repeats++
			return
		}
		a.endRunLocked(c.bucket, run, now)
	}
	if len(a.runs) >= maxTrackedClients {
		a.overflow++
		return
	}
	a.writeLocked(accessLine{Time: now.UTC(), IP: c.ip, Event: eventSigninLocked})
	a.runs[c.bucket] = &lockedRun{ip: c.ip, since: now}
}

// flush writes the counts of the runs whose interval is over. The server calls
// it periodically, so the count of a flood that stopped still reaches the log.
func (a *accessLog) flush(now time.Time) {
	a.mu.Lock()
	defer a.mu.Unlock()
	for bucket, run := range a.runs {
		if now.Sub(run.since) >= LockedLogInterval {
			a.endRunLocked(bucket, run, now)
		}
	}
	if a.overflow > 0 {
		a.writeLocked(accessLine{Time: now.UTC(), IP: anyClient, Event: eventSigninLockedRepeated, Count: a.overflow})
		a.overflow = 0
	}
}

func (a *accessLog) endRunLocked(bucket string, run *lockedRun, now time.Time) {
	if run.repeats > 0 {
		a.writeLocked(accessLine{Time: now.UTC(), IP: run.ip, Event: eventSigninLockedRepeated, Count: run.repeats})
	}
	delete(a.runs, bucket)
}

// writeLocked appends one line, first moving a full log to access.log.1. A
// failed write is dropped: the log must never take sign-in down with it.
func (a *accessLog) writeLocked(l accessLine) {
	b, err := json.Marshal(l)
	if err != nil {
		return
	}
	b = append(b, '\n')
	if a.f != nil && a.size > 0 && a.size+int64(len(b)) > a.limit {
		_ = a.f.Close()
		a.f = nil
		_ = os.Rename(a.path, a.path+".1")
	}
	if a.f == nil && a.open() != nil {
		return
	}
	n, _ := a.f.Write(b)
	a.size += int64(n)
}

func (a *accessLog) close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	if a.f == nil {
		return nil
	}
	return a.f.Close()
}
