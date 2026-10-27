package releaseweb

import (
	"sync"
	"time"
)

const (
	// SigninWindow is how far back sign-ins, failed or successful, count.
	SigninWindow = 15 * time.Minute
	// SigninFailuresPerClient failures from one client within the window lock
	// that client out. It is high enough that an office behind one address
	// survives a morning of typos.
	SigninFailuresPerClient = 10
	// SigninClientLockout is a client's first lockout; each repeat doubles it.
	SigninClientLockout = 5 * time.Minute
	// SigninMaxClientLockout caps the doubling, and is how long a client must
	// stay out of lockout before the doubling starts over.
	SigninMaxClientLockout = 24 * time.Hour
	// SigninGlobalFailures failures from all clients within the window lock
	// sign-in for every client that is not known.
	SigninGlobalFailures = 30
	// SigninGlobalLockout is how long the global lock lasts. Existing sessions keep working.
	SigninGlobalLockout = 15 * time.Minute
	// SigninSuccessesPerClient successful sign-ins from one client within the
	// window refuse the next, so one key holder cannot churn the session store.
	SigninSuccessesPerClient = 20
	// maxTrackedClients bounds the client table. Entries appear on a failure or
	// a success; the global lock caps failures and only key holders succeed.
	maxTrackedClients = 10000
	// maxKnownClients bounds the clients the global lock lets through.
	maxKnownClients = 1000
)

// window keeps event times inside SigninWindow, oldest first.
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

// add records an event and reports whether the window now holds limit
// events, emptying it when it does.
func (w *window) add(now time.Time, limit int) bool {
	w.prune(now)
	*w = append(*w, now)
	if len(*w) >= limit {
		*w = (*w)[:0]
		return true
	}
	return false
}

// waitBelow is how long until the window holds fewer than limit events.
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

// signinLimiter throttles sign-ins per client and failed sign-ins across all
// clients.
//
// The global lock stops a guesser who spreads over many addresses, but it
// would also let anyone without the key lock every teammate out. So it spares
// known clients: those that signed in successfully within SessionAbsoluteTTL.
// A guesser never becomes known. A teammate who has never signed in from his
// address still waits out the global lock.
type signinLimiter struct {
	mu                sync.Mutex
	clients           map[string]*clientBucket
	global            window
	globalLockedUntil time.Time
	// known maps a client to its last successful sign-in. It is kept apart
	// from clients so a flood of failures cannot evict it.
	known map[string]time.Time
}

func newSigninLimiter() *signinLimiter {
	return &signinLimiter{clients: map[string]*clientBucket{}, known: map[string]time.Time{}}
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
	if !check() {
		l.recordFailureLocked(client, now)
		return false, 0
	}
	// Forget recent failures but keep the lockout history, so a correct key
	// between lockouts does not reset the doubling.
	b := l.bucketLocked(client, now)
	b.failures = b.failures[:0]
	b.successes = append(b.successes, now)
	l.rememberLocked(client, now)
	return true, 0
}

func (l *signinLimiter) retryAfterLocked(client string, now time.Time) time.Duration {
	var wait time.Duration
	if !l.isKnownLocked(client, now) {
		wait = l.globalLockedUntil.Sub(now)
	}
	if b, ok := l.clients[client]; ok {
		wait = max(wait, b.lockedUntil.Sub(now), b.successes.waitBelow(now, SigninSuccessesPerClient))
	}
	return max(wait, 0)
}

func (l *signinLimiter) recordFailureLocked(client string, now time.Time) {
	if l.global.add(now, SigninGlobalFailures) {
		l.globalLockedUntil = now.Add(SigninGlobalLockout)
	}
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

func (l *signinLimiter) isKnownLocked(client string, now time.Time) bool {
	at, ok := l.known[client]
	return ok && now.Sub(at) < SessionAbsoluteTTL
}

// rememberLocked marks client known, dropping expired entries and then the
// least recently signed-in one when the set is full.
func (l *signinLimiter) rememberLocked(client string, now time.Time) {
	if _, ok := l.known[client]; !ok && len(l.known) >= maxKnownClients {
		var oldest string
		var oldestAt time.Time
		for c, at := range l.known {
			if now.Sub(at) >= SessionAbsoluteTTL {
				delete(l.known, c)
				continue
			}
			if oldest == "" || at.Before(oldestAt) {
				oldest, oldestAt = c, at
			}
		}
		if len(l.known) >= maxKnownClients {
			delete(l.known, oldest)
		}
	}
	l.known[client] = now
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
