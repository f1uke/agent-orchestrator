package releaseweb

import (
	"sync"
	"time"
)

const (
	// SigninFailureWindow is how far back failed sign-ins count.
	SigninFailureWindow = 15 * time.Minute
	// SigninFailuresPerClient failures from one client within the window lock that client out.
	SigninFailuresPerClient = 5
	// SigninClientLockout is a client's first lockout; each repeat doubles it.
	SigninClientLockout = 15 * time.Minute
	// SigninMaxClientLockout caps the doubling, and is how long a client must
	// stay out of lockout before the doubling starts over.
	SigninMaxClientLockout = 24 * time.Hour
	// SigninGlobalFailures failures from all clients within the window lock sign-in for everyone.
	SigninGlobalFailures = 30
	// SigninGlobalLockout is how long the global lock lasts. Existing sessions keep working.
	SigninGlobalLockout = 15 * time.Minute
	// maxTrackedClients bounds the client table. Entries appear only on a
	// failure, and the global lock caps failures, so the bound is rarely reached.
	maxTrackedClients = 10000
)

// failureWindow keeps the failure times inside SigninFailureWindow.
type failureWindow []time.Time

// add records a failure and reports whether the window now holds limit
// failures, emptying it when it does.
func (f *failureWindow) add(now time.Time, limit int) bool {
	kept := (*f)[:0]
	for _, t := range *f {
		if now.Sub(t) < SigninFailureWindow {
			kept = append(kept, t)
		}
	}
	kept = append(kept, now)
	if len(kept) >= limit {
		*f = kept[:0]
		return true
	}
	*f = kept
	return false
}

type clientBucket struct {
	failures    failureWindow
	lockedUntil time.Time
	lockouts    int
	lastSeen    time.Time
}

func (b *clientBucket) idle(now time.Time) bool {
	return !now.Before(b.lockedUntil) && now.Sub(b.lastSeen) >= SigninMaxClientLockout
}

// signinLimiter throttles failed sign-ins per client and across all clients.
type signinLimiter struct {
	mu                sync.Mutex
	clients           map[string]*clientBucket
	global            failureWindow
	globalLockedUntil time.Time
}

func newSigninLimiter() *signinLimiter {
	return &signinLimiter{clients: map[string]*clientBucket{}}
}

// attempt runs check unless client is locked out, and records its outcome. The
// check runs under the limiter's lock, so parallel guesses cannot slip past the
// limit between the lockout test and the failure count. check must be fast.
func (l *signinLimiter) attempt(client string, now time.Time, check func() bool) (ok bool, retryAfter time.Duration) {
	l.mu.Lock()
	defer l.mu.Unlock()
	if wait := l.retryAfterLocked(client, now); wait > 0 {
		return false, wait
	}
	if check() {
		// Forget recent failures but keep the lockout history, so a correct
		// key between lockouts does not reset the doubling.
		if b, ok := l.clients[client]; ok {
			b.failures = b.failures[:0]
		}
		return true, 0
	}
	l.recordFailureLocked(client, now)
	return false, 0
}

func (l *signinLimiter) retryAfterLocked(client string, now time.Time) time.Duration {
	wait := l.globalLockedUntil.Sub(now)
	if b, ok := l.clients[client]; ok {
		wait = max(wait, b.lockedUntil.Sub(now))
	}
	return max(wait, 0)
}

func (l *signinLimiter) recordFailureLocked(client string, now time.Time) {
	if l.global.add(now, SigninGlobalFailures) {
		l.globalLockedUntil = now.Add(SigninGlobalLockout)
	}
	b, ok := l.clients[client]
	if !ok {
		l.makeRoomLocked(now)
		b = &clientBucket{}
		l.clients[client] = b
	}
	b.lastSeen = now
	if !b.failures.add(now, SigninFailuresPerClient) {
		return
	}
	if now.Sub(b.lockedUntil) >= SigninMaxClientLockout {
		b.lockouts = 0
	}
	lockout := SigninMaxClientLockout
	if b.lockouts < 7 { // 15m << 7 already exceeds 24h
		lockout = min(SigninClientLockout<<b.lockouts, SigninMaxClientLockout)
	}
	b.lockouts++
	b.lockedUntil = now.Add(lockout)
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
	// Evict the least recently seen client, preferring one not locked out.
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
