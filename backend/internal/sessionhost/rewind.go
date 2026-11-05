package sessionhost

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

// Claude answers turn_running for a moment after a turn's result while it
// settles the turn, so the host asks again until rewindSettle has passed.
const (
	rewindSettle = 5 * time.Second
	rewindRetry  = 250 * time.Millisecond
)

// RewindPreview is what restoring files to before a user message would change.
// CanRewind is false when claude holds no checkpoint for that message, as in a
// session started before file checkpointing was on; Error says why.
type RewindPreview struct {
	CanRewind    bool     `json:"can_rewind"`
	Error        string   `json:"error,omitempty"`
	FilesChanged []string `json:"files_changed"`
	Insertions   int      `json:"insertions"`
	Deletions    int      `json:"deletions"`
}

type rewindPreviewResponse struct {
	OK      bool          `json:"ok"`
	Preview RewindPreview `json:"preview"`
}

func (h *host) rewind(r rewindRequest) (any, error) {
	if err := h.rewindConversation(r.UUID); err != nil {
		return nil, err
	}
	if !r.RestoreFiles {
		return okResponse{OK: true}, nil
	}
	body, err := h.controlBody("rewind_files", func(id string) chatproto.Outbound {
		return chatproto.RewindFiles{RequestID: id, UserMessageID: r.UUID}
	})
	if err != nil {
		return nil, fmt.Errorf("the conversation was rewound but the files were not restored: %w", err)
	}
	if p := parsePreview(body); !p.CanRewind {
		return nil, fmt.Errorf("the conversation was rewound but the files were not restored: %s", p.Error)
	}
	return okResponse{OK: true}, nil
}

func (h *host) rewindConversation(target string) error {
	deadline := time.Now().Add(rewindSettle)
	for {
		body, err := h.controlBody("rewind_conversation", func(id string) chatproto.Outbound {
			return chatproto.RewindConversation{RequestID: id, TargetUUID: target}
		})
		if err != nil {
			return err
		}
		var w struct {
			Rewound bool   `json:"rewound"`
			Error   string `json:"error"`
			Reason  string `json:"reason"`
		}
		if err := json.Unmarshal(body, &w); err != nil {
			return fmt.Errorf("rewind_conversation answer: %w", err)
		}
		switch {
		case w.Rewound:
			return nil
		case w.Reason == "turn_running" && time.Now().Add(rewindRetry).Before(deadline):
			time.Sleep(rewindRetry)
		case w.Error != "":
			return errors.New(w.Error)
		default:
			return errors.New("claude did not rewind the conversation")
		}
	}
}

func (h *host) rewindPreview(target string) (any, error) {
	body, err := h.controlBody("rewind_files", func(id string) chatproto.Outbound {
		return chatproto.RewindFiles{RequestID: id, UserMessageID: target, DryRun: true}
	})
	if err != nil {
		return nil, err
	}
	return rewindPreviewResponse{OK: true, Preview: parsePreview(body)}, nil
}

func parsePreview(body json.RawMessage) RewindPreview {
	var w struct {
		CanRewind    bool     `json:"canRewind"`
		Error        string   `json:"error"`
		FilesChanged []string `json:"filesChanged"`
		Insertions   int      `json:"insertions"`
		Deletions    int      `json:"deletions"`
	}
	if err := json.Unmarshal(body, &w); err != nil {
		return RewindPreview{Error: "claude's answer was not readable"}
	}
	p := RewindPreview{CanRewind: w.CanRewind, Error: w.Error, FilesChanged: w.FilesChanged, Insertions: w.Insertions, Deletions: w.Deletions}
	if p.FilesChanged == nil {
		p.FilesChanged = []string{}
	}
	if !p.CanRewind && p.Error == "" {
		p.Error = "claude has no checkpoint for that message"
	}
	return p
}
