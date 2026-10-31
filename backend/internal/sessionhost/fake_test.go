package sessionhost

import (
	"bufio"
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"os/signal"
	"strconv"
	"syscall"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/chatproto"
	"github.com/aoagents/agent-orchestrator/backend/internal/ports"
)

const (
	envFakeFixture  = "SESSIONHOST_FAKE_CLAUDE"
	envFakeArgv     = "SESSIONHOST_FAKE_ARGV"
	envFakeStubborn = "SESSIONHOST_FAKE_STUBBORN"
	envFakeChildPID = "SESSIONHOST_FAKE_CHILD"
	envFakeSleeper  = "SESSIONHOST_FAKE_SLEEPER"
	envFakeParent   = "SESSIONHOST_FAKE_PARENT"
	envFakeContext  = "SESSIONHOST_FAKE_CONTEXT"
)

func TestMain(m *testing.M) {
	switch {
	case os.Getenv(envFakeSleeper) == "1":
		time.Sleep(time.Hour)
		os.Exit(0)
	case os.Getenv(envFakeFixture) != "":
		os.Exit(fakeClaude(os.Getenv(envFakeFixture)))
	}
	os.Exit(m.Run())
}

func fakeClaude(fixture string) int {
	fail := func(format string, args ...any) int {
		fmt.Fprintf(os.Stderr, "fake claude: "+format+"\n", args...)
		return 3
	}
	if p := os.Getenv(envFakeArgv); p != "" {
		b, _ := json.Marshal(os.Args[1:])
		if err := os.WriteFile(p, b, 0o600); err != nil {
			return fail("write argv: %v", err)
		}
	}
	if p := os.Getenv(envFakeParent); p != "" {
		line := os.Getenv(ports.EnvAgentParentPID) + " " + strconv.Itoa(os.Getppid())
		if err := os.WriteFile(p, []byte(line), 0o600); err != nil {
			return fail("write parent: %v", err)
		}
	}
	stubborn := os.Getenv(envFakeStubborn) == "1"
	if stubborn {
		signal.Ignore(syscall.SIGTERM)
	}
	if p := os.Getenv(envFakeChildPID); p != "" {
		child := exec.Command(os.Args[0])
		child.Env = append(os.Environ(), envFakeSleeper+"=1")
		if err := child.Start(); err != nil {
			return fail("start child: %v", err)
		}
		if err := os.WriteFile(p, []byte(strconv.Itoa(child.Process.Pid)), 0o600); err != nil {
			return fail("write child pid: %v", err)
		}
	}
	f, err := os.Open(fixture)
	if err != nil {
		return fail("%v", err)
	}
	entries, err := chatproto.DecodeLog(f)
	_ = f.Close()
	if err != nil {
		return fail("%v", err)
	}

	contextBody, err := fakeContextBody()
	if err != nil {
		return fail("%v", err)
	}
	stdin := bufio.NewReader(os.Stdin)
	var swaps [][2][]byte
	for _, e := range entries {
		switch e.Dir {
		case chatproto.DirOut:
			line := bytes.Clone(e.Payload)
			for _, s := range swaps {
				line = bytes.ReplaceAll(line, s[0], s[1])
			}
			if _, err := os.Stdout.Write(append(line, '\n')); err != nil {
				return fail("write stdout: %v", err)
			}
		case chatproto.DirIn:
			got, err := readSkippingContextRequests(stdin, contextBody)
			if err != nil {
				if stubborn {
					time.Sleep(time.Hour)
				}
				return 0
			}
			want := messageKind(e.Payload)
			if k := messageKind(got); k != want {
				return fail("seq %d: got %s on stdin, want %s", e.Seq, k, want)
			}
			if from, to := messageID(e.Payload), messageID(got); from != "" && from != to {
				swaps = append(swaps, [2][]byte{[]byte(from), []byte(to)})
			}
		case chatproto.DirHost:
			if note, err := chatproto.ParseHostNote(e.Payload); err == nil && note.Kind == chatproto.NoteExit {
				if note.Code != nil {
					return *note.Code
				}
				return 0
			}
		}
	}
	return 0
}

func fakeContextBody() ([]byte, error) {
	p := os.Getenv(envFakeContext)
	if p == "" {
		return nil, nil
	}
	return os.ReadFile(p)
}

func readSkippingContextRequests(stdin *bufio.Reader, body []byte) ([]byte, error) {
	for {
		got, err := stdin.ReadBytes('\n')
		if err != nil || messageKind(got) != "control_request/get_context_usage" {
			return got, err
		}
		if body == nil {
			continue
		}
		reply, err := json.Marshal(map[string]any{
			"type":     "control_response",
			"response": map[string]any{"subtype": "success", "request_id": messageID(got), "response": json.RawMessage(body)},
		})
		if err != nil {
			return nil, err
		}
		if _, err := os.Stdout.Write(append(reply, '\n')); err != nil {
			return nil, err
		}
	}
}

type wireMessage struct {
	Type    string `json:"type"`
	UUID    string `json:"uuid"`
	ReqID   string `json:"request_id"`
	Request struct {
		Subtype string `json:"subtype"`
	} `json:"request"`
	Response struct {
		Subtype   string `json:"subtype"`
		RequestID string `json:"request_id"`
	} `json:"response"`
}

func messageKind(b []byte) string {
	var m wireMessage
	if json.Unmarshal(b, &m) != nil {
		return "invalid JSON"
	}
	return m.Type + "/" + m.Request.Subtype + m.Response.Subtype
}

func messageID(b []byte) string {
	var m wireMessage
	_ = json.Unmarshal(b, &m)
	switch {
	case m.UUID != "":
		return m.UUID
	case m.ReqID != "":
		return m.ReqID
	}
	return m.Response.RequestID
}
