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
	eventSignout               accessEvent = "signout"
	eventSessionRejectedRotate accessEvent = "session_rejected_rotated"
)

// accessLine is the whole of what access.log records: never the key, a token,
// a form body or a query string.
type accessLine struct {
	Time  time.Time   `json:"time"`
	IP    string      `json:"ip"`
	Event accessEvent `json:"event"`
}

type accessLog struct {
	mu sync.Mutex
	f  *os.File
}

func openAccessLog(path string) (*accessLog, error) {
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND|os.O_CREATE, 0o600)
	if err != nil {
		return nil, fmt.Errorf("release web: open access log: %w", err)
	}
	if err := f.Chmod(0o600); err != nil {
		_ = f.Close()
		return nil, fmt.Errorf("release web: chmod access log: %w", err)
	}
	return &accessLog{f: f}, nil
}

// record appends one line. A failed write is dropped: the log must never take
// sign-in down with it.
func (a *accessLog) record(now time.Time, ip string, ev accessEvent) {
	b, err := json.Marshal(accessLine{Time: now.UTC(), IP: ip, Event: ev})
	if err != nil {
		return
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	_, _ = a.f.Write(append(b, '\n'))
}

func (a *accessLog) close() error {
	a.mu.Lock()
	defer a.mu.Unlock()
	return a.f.Close()
}
