package releaseweb

import (
	"sync"
	"time"
)

const (
	// SigninWindow is how far back sign-ins count.
	SigninWindow = 15 * time.Minute
	// SigninFailuresPerClient failures within the window lock a client out.
	SigninFailuresPerClient = 10
	// SigninClientLockout is a client's first lockout; each repeat doubles it.
	SigninClientLockout = 5 * time.Minute
	// SigninMaxClientLockout caps the doubling.
	SigninMaxClientLockout = 24 * time.Hour
	// SigninSuccessesPerClient successes within the window refuse the next.
	SigninSuccessesPerClient = 20
	maxTrackedClients        = 10000
)

type window []time.Time

func (w *window) prune(now time.Time) {
	kept := (*w)[:0]
	for _, t := range *w {
		if now.Sub(t) < SigninWindow {
			kept = append(kept, t)
		}
	}
	*w = kept
}

func (w *window) add(now time.Time, limit int) bool {
	w.prune(now)
	*w = append(*w, now)
	if len(*w) >= limit {
		*w = (*w)[:0]
		return true
	}
	return false
}

func (w *window) waitBelow(now time.Time, limit int) time.Duration {
	w.prune(now)
	if len(*w) < limit {
		return 0
	}
	return (*w)[len(*w)-limit].Add(SigninWindow).Sub(now)
}

type clientBucket struct {
	failures    window
	successes   window
	lockedUntil time.Time
	lockouts    int
	lastSeen    time.Time
}

func (b *clientBucket) idle(now time.Time) bool {
	return !now.Before(b.lockedUntil) && now.Sub(b.lastSeen) >= SigninMaxClientLockout
}

type signinLimiter struct {
	mu      sync.Mutex
	clients map[string]*clientBucket
}

func newSigninLimiter() *signinLimiter {
	return &signinLimiter{clients: map[string]*clientBucket{}}
}

func (l *signinLimiter) attempt(client string, now time.Time, check func() bool) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if wait := l.retryAfterLocked(client, now); wait > 0 {
		return false, wait
	}
	if !check() {
		l.recordFailureLocked(client, now)
		return false, 0
	}
	// between lockouts does not reset the doubling.
	b := l.bucketLocked(client, now)
	b.failures = b.failures[:0]
	b.successes = append(b.successes, now)
	return true, 0
}

func (l *signinLimiter) retryAfterLocked(client string, now time.Time) time.Duration {
	b, ok := l.clients[client]
	if !ok {
		return 0
	}
	return max(b.lockedUntil.Sub(now), b.successes.waitBelow(now, SigninSuccessesPerClient), 0)
}

func (l *signinLimiter) recordFailureLocked(client string, now time.Time) {
	b := l.bucketLocked(client, now)
	if !b.failures.add(now, SigninFailuresPerClient) {
		return
	}
	if now.Sub(b.lockedUntil) >= SigninMaxClientLockout {
		b.lockouts = 0
	}
	lockout := SigninClientLockout
	for range b.lockouts {
		lockout = min(2*lockout, SigninMaxClientLockout)
	}
	b.lockouts++
	b.lockedUntil = now.Add(lockout)
}

func (l *signinLimiter) bucketLocked(client string, now time.Time) *clientBucket {
	b, ok := l.clients[client]
	if !ok {
		l.makeRoomLocked(now)
		b = &clientBucket{}
		l.clients[client] = b
	}
	b.lastSeen = now
	return b
}

func (l *signinLimiter) makeRoomLocked(now time.Time) {
	if len(l.clients) < maxTrackedClients {
		return
	}
	for c, b := range l.clients {
		if b.idle(now) {
			delete(l.clients, c)
		}
	}
	if len(l.clients) < maxTrackedClients {
		return
	}
	var victim string
	var victimSeen time.Time
	victimLocked := true
	for c, b := range l.clients {
		locked := now.Before(b.lockedUntil)
		better := victim == "" ||
			(victimLocked && !locked) ||
			(locked == victimLocked && b.lastSeen.Before(victimSeen))
		if better {
			victim, victimSeen, victimLocked = c, b.lastSeen, locked
		}
	}
	delete(l.clients, victim)
}
