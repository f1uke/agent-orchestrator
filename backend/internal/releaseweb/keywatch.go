package releaseweb

import (
	"log/slog"
	"os"
	"sync"
)

// keyWatcher follows key.json so a RotateKey from another process reaches a
// running server. One stat per request is the whole cost while nothing changes.
type keyWatcher struct {
	p        Paths
	log      *slog.Logger
	onChange func()

	mu   sync.Mutex
	seen os.FileInfo
	key  Key
	ok   bool
}

func newKeyWatcher(p Paths, log *slog.Logger, onChange func()) (*keyWatcher, error) {
	w := &keyWatcher{p: p, log: log, onChange: onChange}
	st, _ := os.Stat(p.KeyFile()) // nil re-reads on the next request
	k, err := ReadKey(p)
	if err != nil {
		return nil, err
	}
	w.seen, w.key, w.ok = st, k, true
	return w, nil
}

// current is the key to check sign-ins and sessions against; false while
// key.json is missing or invalid, which signs everyone out and refuses
// sign-in, so deleting the file is a kill switch.
func (w *keyWatcher) current() (Key, bool) {
	w.mu.Lock()
	defer w.mu.Unlock()
	st, err := os.Stat(w.p.KeyFile())
	if err == nil && w.seen != nil && os.SameFile(st, w.seen) &&
		st.ModTime().Equal(w.seen.ModTime()) && st.Size() == w.seen.Size() {
		return w.key, w.ok
	}
	if err != nil && w.seen == nil && !w.ok {
		return Key{}, false
	}
	w.seen = st
	if err != nil {
		w.seen = nil
	}
	k, readErr := ReadKey(w.p)
	if readErr != nil {
		w.log.Error("release web: key unusable, refusing sign-in", "err", readErr)
		if w.ok {
			w.key, w.ok = Key{}, false
			w.onChange()
		}
		return Key{}, false
	}
	if !w.ok || k.Generation != w.key.Generation || !k.sameSecret(w.key) {
		w.key, w.ok = k, true
		w.onChange()
	}
	return w.key, true
}
