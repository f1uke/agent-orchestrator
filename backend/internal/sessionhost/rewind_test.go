package sessionhost

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
)

func rewindLine(t *testing.T, id string) fixtureLine {
	return fixtureLine{chatproto.DirIn, encoded(t, chatproto.RewindConversation{RequestID: id, TargetUUID: "u-2"})}
}

func answer(id, body string) fixtureLine {
	return fixtureLine{chatproto.DirOut, `{"type":"control_response","response":{"subtype":"success","request_id":"` + id + `","response":` + body + `}}`}
}

func TestClientRewindRetriesWhileTheTurnSettles(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir,
		rewindLine(t, "rw-1"),
		answer("rw-1", `{"rewound":false,"error":"turn running","reason":"turn_running"}`),
		rewindLine(t, "rw-2"),
		answer("rw-2", `{"rewound":true,"targetMessageUuid":"u-2","prefillText":"x"}`),
	))
	startHost(t, cfg)

	if err := NewClient(cfg.SocketPath).Rewind(context.Background(), "u-2", false); err != nil {
		t.Fatalf("Rewind = %v, want success after one turn_running answer", err)
	}
	got := loggedInputs(t, cfg.LogPath, "control_request/rewind_conversation")
	if len(got) != 2 || !strings.Contains(got[1], `"target_message_uuid":"u-2"`) {
		t.Errorf("logged rewind requests = %v, want two for u-2", got)
	}
	if files := loggedInputs(t, cfg.LogPath, "control_request/rewind_files"); len(files) != 0 {
		t.Errorf("logged rewind_files = %v, want none without restore", files)
	}
}

func TestClientRewindRestoresFilesAfterTheConversation(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir,
		rewindLine(t, "rw-1"),
		answer("rw-1", `{"rewound":true}`),
		fixtureLine{chatproto.DirIn, encoded(t, chatproto.RewindFiles{RequestID: "rf-1", UserMessageID: "u-2"})},
		answer("rf-1", `{"canRewind":true}`),
	))
	startHost(t, cfg)

	if err := NewClient(cfg.SocketPath).Rewind(context.Background(), "u-2", true); err != nil {
		t.Fatalf("Rewind(restore) = %v", err)
	}
	got := loggedInputs(t, cfg.LogPath, "control_request/rewind_files")
	if len(got) != 1 || strings.Contains(got[0], "dry_run") {
		t.Errorf("logged rewind_files = %v, want one real restore", got)
	}
}

func TestClientRewindSaysWhyClaudeRefused(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir,
		rewindLine(t, "rw-1"),
		answer("rw-1", `{"rewound":false,"error":"the target is not a prompt","reason":"not_a_prompt"}`),
	))
	startHost(t, cfg)

	err := NewClient(cfg.SocketPath).Rewind(context.Background(), "u-2", false)
	var refused *RefusedError
	if !errors.As(err, &refused) || refused.Reason != "the target is not a prompt" {
		t.Fatalf("err = %v, want claude's reason as a refusal", err)
	}
}

func TestClientRewindPreview(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir,
		fixtureLine{chatproto.DirIn, encoded(t, chatproto.RewindFiles{RequestID: "rf-1", UserMessageID: "u-2", DryRun: true})},
		answer("rf-1", `{"canRewind":true,"filesChanged":["/w/a.txt"],"insertions":1,"deletions":2}`),
		fixtureLine{chatproto.DirIn, encoded(t, chatproto.RewindFiles{RequestID: "rf-2", UserMessageID: "u-1", DryRun: true})},
		answer("rf-2", `{"canRewind":false}`),
	))
	startHost(t, cfg)
	c := NewClient(cfg.SocketPath)

	p, err := c.RewindPreview(context.Background(), "u-2")
	if err != nil || !p.CanRewind || !slices.Equal(p.FilesChanged, []string{"/w/a.txt"}) || p.Insertions != 1 || p.Deletions != 2 {
		t.Fatalf("RewindPreview(u-2) = %+v, %v", p, err)
	}
	p, err = c.RewindPreview(context.Background(), "u-1")
	if err != nil || p.CanRewind || p.Error == "" || p.FilesChanged == nil {
		t.Fatalf("RewindPreview(u-1) = %+v, %v; want cannot rewind with a reason", p, err)
	}
}

func TestClaudeRunsWithFileCheckpoints(t *testing.T) {
	dir := shortDir(t)
	cfg, _ := fakeConfig(t, dir, waitingFixture(t, dir))
	envFile := filepath.Join(dir, "env")
	t.Setenv(envFakeEnv, envFile)
	startHost(t, cfg)
	eventually(t, "the fake claude's env", func() bool {
		b, err := os.ReadFile(envFile)
		return err == nil && slices.Contains(strings.Split(string(b), "\n"), envFileCheckpointing+"=1")
	})
}
