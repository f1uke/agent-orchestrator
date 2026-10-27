package releaseweb

import (
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"time"
)

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
	// MaxAccessLogBytes caps access.log before it rotates.
	MaxAccessLogBytes = 10 << 20
	// LockedLogInterval is how often one client's refused attempts are written.
	LockedLogInterval = time.Minute
	anyClient         = "*"
)

type accessLine struct {
	Time  time.Time   `json:"time"`
	IP    string      `json:"ip"`
	Event accessEvent `json:"event"`
	Count int         `json:"count,omitempty"`
}

type lockedRun struct {
	ip      string
	since   time.Time
	repeats int
}

type accessLog struct {
	mu       sync.Mutex
	path     string
	f        *os.File
	size     int64
	limit    int64
	runs     map[string]*lockedRun
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

func (a *accessLog) record(now time.Time, ip string, ev accessEvent) {
	a.mu.Lock()
	defer a.mu.Unlock()
	a.writeLocked(accessLine{Time: now.UTC(), IP: ip, Event: ev})
}

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
