package releaseweb

import (
	"io"
	"os"
	"sync"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
)

// MaxWebLogBytes caps web.log; past it, its content moves to web.log.1.
const MaxWebLogBytes = 10 << 20

// WebLogWriter is where the web writes its log. When out is web.log, as it is for a
// web that start spawned, it keeps the file under MaxWebLogBytes: once at start and
// before every write that would pass the cap. Any other out comes back unchanged.
func WebLogWriter(p Paths, out io.Writer) io.Writer {
	return newWebLogWriter(p, out, MaxWebLogBytes)
}

func newWebLogWriter(p Paths, out io.Writer, limit int64) io.Writer {
	f, ok := out.(*os.File)
	if !ok {
		return out
	}
	held, err := f.Stat()
	if err != nil {
		return out
	}
	named, err := os.Stat(p.WebLog())
	if err != nil || !os.SameFile(held, named) {
		return out
	}
	w := &cappedLog{f: f, path: p.WebLog(), limit: limit}
	w.rotateIfOver(0)
	return w
}

type cappedLog struct {
	mu    sync.Mutex
	f     *os.File
	path  string
	limit int64
}

func (w *cappedLog) Write(b []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.rotateIfOver(int64(len(b)))
	return w.f.Write(b)
}

// rotateIfOver copies the log to .1 and truncates it in place: the file is also the
// process's stdout and stderr, which a rename would leave writing into .1.
func (w *cappedLog) rotateIfOver(next int64) {
	st, err := w.f.Stat()
	if err != nil || st.Size() == 0 || st.Size()+next <= w.limit {
		return
	}
	b, err := os.ReadFile(w.path)
	if err != nil || fsatomic.WriteFile(w.path+".1", b, 0o600) != nil {
		return
	}
	_ = w.f.Truncate(0)
}
