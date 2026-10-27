package releaseweb

import (
	"log/slog"
	"os"
	"sync"
)

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
	st, _ := os.Stat(p.KeyFile())
	k, err := ReadKey(p)
	if err != nil {
		return nil, err
	}
	w.seen, w.key, w.ok = st, k, true
	return w, nil
}

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
	if !w.ok || k.Generation != w.key.Generation || k.id != w.key.id {
		w.key, w.ok = k, true
		w.onChange()
	}
	return w.key, true
}
