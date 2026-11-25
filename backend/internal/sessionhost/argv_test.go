package sessionhost

import (
	"path/filepath"
	"slices"
	"testing"
)

func TestClaudeArgv(t *testing.T) {
	withChrome := append(slices.Clone(protocolFlags), "--chrome")
	cases := []struct {
		name     string
		command  []string
		resume   string
		model    string
		effort   string
		attach   string
		suggest  bool
		noChrome bool
		want     []string
	}{
		{
			name:    "fresh keeps the pinned session id",
			command: []string{"claude", "--session-id", "pin", "--model", "opus"},
			want:    append([]string{"claude", "--session-id", "pin", "--model", "opus"}, withChrome...),
		},
		{
			name:    "resume drops the session id claude refuses beside --resume",
			command: []string{"claude", "--session-id", "pin", "--model", "opus"},
			resume:  "pin",
			want:    append(append([]string{"claude", "--model", "opus"}, withChrome...), "--resume", "pin"),
		},
		{
			name:    "resume drops the joined form too",
			command: []string{"claude", "--session-id=pin", "--model", "opus"},
			resume:  "other",
			want:    append(append([]string{"claude", "--model", "opus"}, withChrome...), "--resume", "other"),
		},
		{
			name:    "a chosen model replaces the launch model",
			command: []string{"claude", "--model", "opus", "--effort", "high"},
			model:   "claude-sonnet-5-5",
			want:    append([]string{"claude", "--effort", "high", "--model", "claude-sonnet-5-5"}, withChrome...),
		},
		{
			name:    "a chosen model replaces the joined form too",
			command: []string{"claude", "--model=opus", "--", "--model", "x"},
			model:   "claude-sonnet-5-5",
			want:    append(append([]string{"claude", "--model", "claude-sonnet-5-5"}, withChrome...), "--", "--model", "x"),
		},
		{
			name:    "a chosen effort replaces the launch effort",
			command: []string{"claude", "--effort", "high", "--model", "opus"},
			effort:  "low",
			want:    append([]string{"claude", "--model", "opus", "--effort", "low"}, withChrome...),
		},
		{
			name:    "a chosen effort replaces the joined form too, and leaves prompt text alone",
			command: []string{"claude", "--effort=high", "--", "--effort", "x"},
			effort:  "low",
			want:    append(append([]string{"claude", "--effort", "low"}, withChrome...), "--", "--effort", "x"),
		},
		{
			name:    "a chosen effort is added when the command has none, beside the chosen model",
			command: []string{"claude"},
			model:   "claude-sonnet-5-5",
			effort:  "max",
			want:    append([]string{"claude", "--model", "claude-sonnet-5-5", "--effort", "max"}, withChrome...),
		},
		{
			name:    "no chosen effort leaves the command's alone",
			command: []string{"claude", "--effort=high"},
			want:    append([]string{"claude", "--effort=high"}, withChrome...),
		},
		{
			name:    "a session id after -- is prompt text, not a flag",
			command: []string{"claude", "--model", "opus", "--", "--session-id", "x"},
			resume:  "pin",
			want:    append(append([]string{"claude", "--model", "opus"}, withChrome...), "--resume", "pin", "--", "--session-id", "x"),
		},
		{
			name:    "the attachments folder is added as a readable directory before any prompt text",
			command: []string{"claude", "--model", "opus", "--", "hello"},
			attach:  "/data/sessions/s1/attachments",
			want: append(append([]string{"claude", "--model", "opus"}, withChrome...),
				"--add-dir", "/data/sessions/s1/attachments", "--", "hello"),
		},
		{
			name:    "--resume stays last after the attachments folder",
			command: []string{"claude"},
			resume:  "pin",
			attach:  "/a",
			want:    append(append([]string{"claude"}, withChrome...), "--add-dir", "/a", "--resume", "pin"),
		},
		{
			name:    "prompt suggestions are asked for beside the protocol flags, before the attachments folder",
			command: []string{"claude", "--model", "opus", "--", "hello"},
			attach:  "/a",
			suggest: true,
			want: append(append(append([]string{"claude", "--model", "opus"}, withChrome...), "--prompt-suggestions"),
				"--add-dir", "/a", "--", "hello"),
		},
		{
			name:    "a resume keeps asking for prompt suggestions",
			command: []string{"claude"},
			resume:  "pin",
			suggest: true,
			want:    append(append(append([]string{"claude"}, withChrome...), "--prompt-suggestions"), "--resume", "pin"),
		},
		{
			name:    "chrome stays on through a resume",
			command: []string{"claude", "--model", "opus"},
			resume:  "pin",
			want:    append(append([]string{"claude", "--model", "opus"}, withChrome...), "--resume", "pin"),
		},
		{
			name:     "chrome turned off is asked for explicitly, so a later fold does not read the default",
			command:  []string{"claude"},
			resume:   "pin",
			noChrome: true,
			want:     append(append(append([]string{"claude"}, protocolFlags...), "--no-chrome"), "--resume", "pin"),
		},
		{
			name:    "the session's chrome choice replaces one the agent command carries",
			command: []string{"claude", "--no-chrome", "--chrome", "--", "--chrome"},
			want:    append(append([]string{"claude"}, withChrome...), "--", "--chrome"),
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			l := launch{resume: c.resume, model: c.model, effort: c.effort, attachDir: c.attach, suggestions: c.suggest, chrome: !c.noChrome}
			if got := claudeArgv(c.command, l); !slices.Equal(got, c.want) {
				t.Errorf("claudeArgv(%v, %+v) =\n %v\nwant\n %v", c.command, l, got, c.want)
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
