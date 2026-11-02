package sessionhost

import (
	"encoding/json"
	"time"

	"github.com/google/uuid"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

// claude compacts an idle conversation itself only when it runs interactively
// or under a desktop entrypoint, never under `claude -p`, so the host does it:
// before the 1-hour prompt cache expires, a large idle context is compacted
// while the cache is still warm.
const (
	idleCompactAfter     = 50 * time.Minute
	idleCompactMinTokens = 200_000
	compactCommand       = "/compact"
)

// idleState is what the host has read from claude's output that decides an
// idle compaction. It is guarded by host.mu.
type idleState struct {
	contextTokens int
	cacheTTL      string
	tasks         map[string]struct{}
	rateLimited   bool
	stop          func() bool
	generation    int
}

type afterFunc func(d time.Duration, f func()) (stop func() bool)

func realAfterFunc(d time.Duration, f func()) func() bool { return time.AfterFunc(d, f).Stop }

// observeIdleLocked updates the idle state from one line claude wrote, arms
// the timer at the end of a turn, and disarms it when a request starts.
func (h *host) observeIdleLocked(ev chatproto.Event, line []byte) {
	if h.idleOff {
		return
	}
	switch ev := ev.(type) {
	case chatproto.Assistant:
		h.disarmIdleLocked()
		if ev.ParentToolUseID == "" {
			h.readUsageLocked(line)
		}
	case chatproto.System:
		h.readTaskLocked(ev)
	case chatproto.Result:
		h.armIdleLocked()
	case chatproto.Unknown:
		if ev.Type == "rate_limit_event" {
			var w struct {
				Info struct {
					Status string `json:"status"`
				} `json:"rate_limit_info"`
			}
			if json.Unmarshal(line, &w) == nil && w.Info.Status != "" {
				h.idle.rateLimited = w.Info.Status != "allowed"
			}
		}
	}
}

func (h *host) readUsageLocked(line []byte) {
	var w struct {
		Message struct {
			Usage *struct {
				InputTokens         int `json:"input_tokens"`
				CacheCreationTokens int `json:"cache_creation_input_tokens"`
				CacheReadTokens     int `json:"cache_read_input_tokens"`
				CacheCreation       struct {
					FiveMinute int `json:"ephemeral_5m_input_tokens"`
					OneHour    int `json:"ephemeral_1h_input_tokens"`
				} `json:"cache_creation"`
			} `json:"usage"`
		} `json:"message"`
	}
	if json.Unmarshal(line, &w) != nil || w.Message.Usage == nil {
		return
	}
	u := w.Message.Usage
	h.idle.contextTokens = u.InputTokens + u.CacheCreationTokens + u.CacheReadTokens
	// A request that only reads the cache keeps the TTL its prefix was written with.
	switch {
	case u.CacheCreation.OneHour > 0:
		h.idle.cacheTTL = "1h"
	case u.CacheCreation.FiveMinute > 0:
		h.idle.cacheTTL = "5m"
	}
}

func (h *host) readTaskLocked(ev chatproto.System) {
	var w struct {
		TaskID string `json:"task_id"`
		Patch  struct {
			Status string `json:"status"`
		} `json:"patch"`
	}
	if json.Unmarshal(ev.Raw, &w) != nil || w.TaskID == "" {
		return
	}
	switch {
	case ev.Subtype == "task_started":
		if h.idle.tasks == nil {
			h.idle.tasks = map[string]struct{}{}
		}
		h.idle.tasks[w.TaskID] = struct{}{}
	case ev.Subtype == "task_notification",
		ev.Subtype == "task_updated" && w.Patch.Status != "" && w.Patch.Status != "running" && w.Patch.Status != "pending":
		delete(h.idle.tasks, w.TaskID)
	}
}

func (h *host) armIdleLocked() {
	h.disarmIdleLocked()
	gen := h.idle.generation
	h.idle.stop = h.afterFunc(idleCompactAfter, func() {
		h.mu.Lock()
		defer h.mu.Unlock()
		if h.idle.generation == gen {
			h.fireIdleLocked()
		}
	})
}

func (h *host) disarmIdleLocked() {
	h.idle.generation++
	if h.idle.stop != nil {
		h.idle.stop()
		h.idle.stop = nil
	}
}

// fireIdleLocked sends /compact when every gate still holds. A compaction ends
// in a result like any turn, which arms the timer again over a small context.
func (h *host) fireIdleLocked() {
	h.idle.stop = nil
	if h.closed || h.idle.contextTokens < idleCompactMinTokens || h.idle.cacheTTL != "1h" ||
		len(h.idle.tasks) > 0 || h.idle.rateLimited {
		return
	}
	t := h.folder.Transcript()
	if t.Busy || len(t.Pending) > 0 || len(t.Queued) > 0 {
		return
	}
	if err := h.writeThenLogLocked(chatproto.UserInput{UUID: uuid.NewString(), Text: compactCommand}); err != nil {
		h.pane.line(dim("sessionhost: idle compact: " + err.Error()))
	}
}
