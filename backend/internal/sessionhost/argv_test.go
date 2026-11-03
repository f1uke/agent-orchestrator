package sessionhost

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestClaudeArgv(t *testing.T) {
	cases := []struct {
		name    string
		command []string
		resume  string
		model   string
		attach  string
		suggest bool
		want    []string
	}{
		{
			name:    "fresh keeps the pinned session id",
			command: []string{"claude", "--session-id", "pin", "--model", "opus"},
			want:    append([]string{"claude", "--session-id", "pin", "--model", "opus"}, protocolFlags...),
		},
		{
			name:    "resume drops the session id claude refuses beside --resume",
			command: []string{"claude", "--session-id", "pin", "--model", "opus"},
			resume:  "pin",
			want:    append(append([]string{"claude", "--model", "opus"}, protocolFlags...), "--resume", "pin"),
		},
		{
			name:    "resume drops the joined form too",
			command: []string{"claude", "--session-id=pin", "--model", "opus"},
			resume:  "other",
			want:    append(append([]string{"claude", "--model", "opus"}, protocolFlags...), "--resume", "other"),
		},
		{
			name:    "a chosen model replaces the launch model",
			command: []string{"claude", "--model", "opus", "--effort", "high"},
			model:   "claude-sonnet-5-5",
			want:    append([]string{"claude", "--effort", "high", "--model", "claude-sonnet-5-5"}, protocolFlags...),
		},
		{
			name:    "a chosen model replaces the joined form too",
			command: []string{"claude", "--model=opus", "--", "--model", "x"},
			model:   "claude-sonnet-5-5",
			want:    append(append([]string{"claude", "--model", "claude-sonnet-5-5"}, protocolFlags...), "--", "--model", "x"),
		},
		{
			name:    "a session id after -- is prompt text, not a flag",
			command: []string{"claude", "--model", "opus", "--", "--session-id", "x"},
			resume:  "pin",
			want:    append(append([]string{"claude", "--model", "opus"}, protocolFlags...), "--resume", "pin", "--", "--session-id", "x"),
		},
		{
			name:    "the attachments folder is added as a readable directory before any prompt text",
			command: []string{"claude", "--model", "opus", "--", "hello"},
			attach:  "/data/sessions/s1/attachments",
			want: append(append([]string{"claude", "--model", "opus"}, protocolFlags...),
				"--add-dir", "/data/sessions/s1/attachments", "--", "hello"),
		},
		{
			name:    "--resume stays last after the attachments folder",
			command: []string{"claude"},
			resume:  "pin",
			attach:  "/a",
			want:    append(append([]string{"claude"}, protocolFlags...), "--add-dir", "/a", "--resume", "pin"),
		},
		{
			name:    "prompt suggestions are asked for beside the protocol flags, before the attachments folder",
			command: []string{"claude", "--model", "opus", "--", "hello"},
			attach:  "/a",
			suggest: true,
			want: append(append(append([]string{"claude", "--model", "opus"}, protocolFlags...), "--prompt-suggestions"),
				"--add-dir", "/a", "--", "hello"),
		},
		{
			name:    "a resume keeps asking for prompt suggestions",
			command: []string{"claude"},
			resume:  "pin",
			suggest: true,
			want:    append(append(append([]string{"claude"}, protocolFlags...), "--prompt-suggestions"), "--resume", "pin"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := claudeArgv(c.command, c.resume, c.model, c.attach, c.suggest); !slices.Equal(got, c.want) {
				t.Errorf("claudeArgv(%v, %q, %q, %q, %v) =\n %v\nwant\n %v", c.command, c.resume, c.model, c.attach, c.suggest, got, c.want)
			}
		})
	}
}

func TestAttachmentsDirSitsBesideTheEventLog(t *testing.T) {
	logPath, _ := Paths("/data", "s1")
	if got, want := AttachmentsDir("/data", "s1"), filepath.Join(filepath.Dir(logPath), "attachments"); got != want {
		t.Fatalf("AttachmentsDir = %q, want %q", got, want)
	}
}
