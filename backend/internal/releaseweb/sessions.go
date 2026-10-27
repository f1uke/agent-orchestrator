package releaseweb

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"sync"
	"time"
)

const (
	// SessionIdleTTL signs a browser out after this long without a request.
	SessionIdleTTL = 12 * time.Hour
	// SessionAbsoluteTTL signs a browser out this long after sign-in, however active.
	SessionAbsoluteTTL = 7 * 24 * time.Hour
	// MaxSessions bounds the store. A full store drops the least recently seen
	// session of the client signing in, else the least recently seen of all.
	MaxSessions = 1000
)

type tokenHash [sha256.Size]byte

func hashToken(token string) tokenHash { return sha256.Sum256([]byte(token)) }

type session struct {
	key keyID
	// client is the rate-limit bucket that signed in, so one key holder signing
	// in over and over evicts his own sessions before anyone else's.
	client    string
	createdAt time.Time
	lastSeen  time.Time
}

func (s *session) expired(now time.Time) bool {
	return now.Sub(s.lastSeen) >= SessionIdleTTL || now.Sub(s.createdAt) >= SessionAbsoluteTTL
}

// lookupResult says why a cookie did or did not name a live session.
type lookupResult int

const (
	sessionMissing lookupResult = iota
	sessionLive
	// sessionRotated means the token was live until the key rotated.
	sessionRotated
)

// sessionStore keeps sessions in memory, keyed by the SHA-256 of the token, so
// a heap dump does not hand out cookies. A restart signs everyone out.
type sessionStore struct {
	mu       sync.Mutex
	sessions map[tokenHash]*session
	// rotated holds the hashes the last key rotation dropped, only so the
	// access log can tell a rotated cookie from a forged one.
	rotated map[tokenHash]struct{}
}

func newSessionStore() *sessionStore {
	return &sessionStore{sessions: map[tokenHash]*session{}, rotated: map[tokenHash]struct{}{}}
}

func (s *sessionStore) create(key keyID, client string, now time.Time) (string, error) {
	raw := make([]byte, 32)
	if _, err := rand.Read(raw); err != nil {
		return "", err
	}
	token := base64.RawURLEncoding.EncodeToString(raw)
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked(now)
	if len(s.sessions) >= MaxSessions {
		s.evictLeastRecentLocked(client)
	}
	s.sessions[hashToken(token)] = &session{key: key, client: client, createdAt: now, lastSeen: now}
	return token, nil
}

// lookup refreshes a live session's lastSeen. A session minted under another
// key is dropped: it can only exist when a sign-in raced a rotation.
func (s *sessionStore) lookup(token string, key keyID, now time.Time) lookupResult {
	h := hashToken(token)
	s.mu.Lock()
	defer s.mu.Unlock()
	sess, ok := s.sessions[h]
	if !ok {
		if _, was := s.rotated[h]; was {
			delete(s.rotated, h)
			return sessionRotated
		}
		return sessionMissing
	}
	if sess.key != key {
		delete(s.sessions, h)
		return sessionRotated
	}
	if sess.expired(now) {
		delete(s.sessions, h)
		return sessionMissing
	}
	sess.lastSeen = now
	return sessionLive
}

func (s *sessionStore) remove(token string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, hashToken(token))
}

// rotate signs every session out.
func (s *sessionStore) rotate() {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.rotated = make(map[tokenHash]struct{}, len(s.sessions))
	for h := range s.sessions {
		s.rotated[h] = struct{}{}
	}
	s.sessions = map[tokenHash]*session{}
}

func (s *sessionStore) count(now time.Time) int {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.purgeLocked(now)
	return len(s.sessions)
}

func (s *sessionStore) purgeLocked(now time.Time) {
	for h, sess := range s.sessions {
		if sess.expired(now) {
			delete(s.sessions, h)
		}
	}
}

// evictLeastRecentLocked drops client's least recently seen session, or the
// least recently seen of all when client has none.
func (s *sessionStore) evictLeastRecentLocked(client string) {
	var oldest tokenHash
	var oldestSeen time.Time
	oldestOwn, first := false, true
	for h, sess := range s.sessions {
		own := sess.client == client
		better := first || (own && !oldestOwn) || (own == oldestOwn && sess.lastSeen.Before(oldestSeen))
		if better {
			oldest, oldestSeen, oldestOwn, first = h, sess.lastSeen, own, false
		}
	}
	delete(s.sessions, oldest)
}
