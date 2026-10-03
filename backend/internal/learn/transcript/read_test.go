package transcript

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
)

// The fixtures below reproduce the record shapes Claude Code 2.1.x writes, as
// measured on real transcripts: compact JSON, one record per line, the turn's
// author on origin.kind, and how a typed prompt was submitted on promptSource.

type fx map[string]any

func line(t *testing.T, rec fx) string {
	t.Helper()
	b, err := json.Marshal(rec)
	if err != nil {
		t.Fatal(err)
	}
	return string(b) + "\n"
}

func typed(uuid, ts, text string) fx {
	return fx{
		"type": "user", "uuid": uuid, "timestamp": ts, "isSidechain": false,
		"origin": fx{"kind": "human"}, "promptSource": "typed",
		"cwd": "/work/tree", "gitBranch": "feature/x",
		"message": fx{"role": "user", "content": text},
	}
}

func withSource(r fx, source string) fx {
	r["promptSource"] = source
	return r
}

func peer(uuid, ts, body string) fx {
	return fx{
		"type": "user", "uuid": uuid, "timestamp": ts, "isMeta": true,
		"origin":       fx{"kind": "peer", "from": "unknown", "name": "agent-orchestrator", "body": body},
		"promptSource": "system",
		"message": fx{"role": "user", "content": "Another Claude session sent a message:\n" +
			"<cross-session-message from-name=\"agent-orchestrator\">\n" + body + "\n</cross-session-message>" +
			"\n\nThis came from another Claude session - not typed by your user."},
	}
}

func notification(uuid, ts string) fx {
	return fx{
		"type": "user", "uuid": uuid, "timestamp": ts,
		"origin":       fx{"kind": "task-notification", "producer": "session-task"},
		"promptSource": "system",
		"message":      fx{"role": "user", "content": "<task-notification>\n<task-id>b1</task-id>\n</task-notification>"},
	}
}

func toolResult(uuid, ts string) fx {
	return fx{
		"type": "user", "uuid": uuid, "timestamp": ts,
		"message": fx{"role": "user", "content": []fx{{"tool_use_id": "toolu_1", "type": "tool_result", "content": "output nobody keeps", "is_error": false}}},
	}
}

func assistantText(uuid, ts, text string) fx {
	return fx{
		"type": "assistant", "uuid": uuid, "timestamp": ts,
		"message": fx{"role": "assistant", "content": []fx{{"type": "text", "text": text}}},
	}
}

func assistantTool(uuid, ts, name string, input fx) fx {
	return fx{
		"type": "assistant", "uuid": uuid, "timestamp": ts,
		"message": fx{"role": "assistant", "content": []fx{{"type": "tool_use", "id": "toolu_1", "name": name, "input": input}}},
	}
}

func writeTranscript(t *testing.T, lines ...string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "s.jsonl")
	if err := os.WriteFile(p, []byte(strings.Join(lines, "")), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func appendTranscript(t *testing.T, path string, lines ...string) {
	t.Helper()
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = f.Close() }()
	if _, err := f.WriteString(strings.Join(lines, "")); err != nil {
		t.Fatal(err)
	}
}

func texts(turns []Turn) []string {
	out := make([]string, 0, len(turns))
	for _, tr := range turns {
		out = append(out, tr.Text)
	}
	return out
}

func deliveredOf(entries map[string]Delivery) map[string]Delivery {
	out := map[string]Delivery{}
	for body, d := range entries {
		fp, _ := Fingerprint(body)
		out[fp] = d
	}
	return out
}

func TestRead_KeepsOnlyTheHumansTurns(t *testing.T) {
	brief := "# Task: fix the thing\n\nDo it carefully."
	appSend := "use the script store, never tap by hand"
	path := writeTranscript(t,
		line(t, typed("u1", "2026-10-01T10:00:00Z", brief)),
		line(t, assistantText("a1", "2026-10-01T10:00:05Z", "Starting.")),
		line(t, toolResult("r1", "2026-10-01T10:00:06Z")),
		line(t, typed("u2", "2026-10-01T10:01:00Z", "[from @proj-3] qa is done, see 63fe968")),
		line(t, notification("n1", "2026-10-01T10:02:00Z")),
		line(t, typed("u3", "2026-10-01T10:03:00Z", "CI is failing on PR #12. Review the output below and push a fix.")),
		line(t, typed("u4", "2026-10-01T10:04:00Z", "<command-message>open-mr</command-message> <command-name>/open-mr</command-name>")),
		line(t, peer("p1", "2026-10-01T10:05:00Z", appSend)),
		line(t, peer("p2", "2026-10-01T10:06:00Z", "There are merge conflicts on PR #12.")),
		line(t, typed("u5", "2026-10-01T10:07:00Z", "no - run the script instead")),
		line(t, withSource(typed("u6", "2026-10-01T10:08:00Z", "go ahead"), "suggestion_accepted")),
		line(t, typed("u7", "2026-10-01T10:09:00Z", "[smoke results]\n\nSmoke test results for this session: 1 of 1 checked")),
	)
	res, err := Read(path, 0, nil, Options{
		FileQuiet: true,
		Delivered: deliveredOf(map[string]Delivery{
			brief:   {Author: domain.DeliveryAuthorAO, Trigger: TriggerBrief},
			appSend: {Author: domain.DeliveryAuthorHuman, Trigger: "send"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	got := texts(res.Turns)
	want := []string{appSend, "no - run the script instead", "go ahead", "[smoke results]\n\nSmoke test results for this session: 1 of 1 checked"}
	if strings.Join(got, "|") != strings.Join(want, "|") {
		t.Fatalf("human turns = %q, want %q", got, want)
	}
	sources := []domain.LearnSourceClass{res.Turns[0].Source, res.Turns[1].Source, res.Turns[2].Source, res.Turns[3].Source}
	wantSources := []domain.LearnSourceClass{domain.LearnSourceAppSend, domain.LearnSourceTyped, domain.LearnSourceSuggestionAccepted, domain.LearnSourceSmokeReport}
	for i := range sources {
		if sources[i] != wantSources[i] {
			t.Errorf("turn %d source = %s, want %s", i, sources[i], wantSources[i])
		}
	}
	// brief, [from @], notification, CI nudge, slash command, merge-conflict peer.
	if res.HumanTurns != 4 || res.MachineTurns != 6 {
		t.Errorf("counts = %d human, %d machine; want 4, 6", res.HumanTurns, res.MachineTurns)
	}
}

func TestRead_SplitsAHumanTurnFromAQueuedAgentMessage(t *testing.T) {
	// Seen on a real transcript: the human typed while the agent was busy, an
	// `ao send` landed in the same queue, and both arrived as one turn.
	path := writeTranscript(t,
		line(t, withSource(typed("u1", "2026-10-01T10:00:00Z", "qa should retest after a fix[from @app-10] retest done, all green"), "queued")),
	)
	res, err := Read(path, 0, nil, Options{FileQuiet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 1 || res.Turns[0].Text != "qa should retest after a fix" || res.Turns[0].Source != domain.LearnSourceQueued {
		t.Fatalf("turns = %+v", res.Turns)
	}
}

func TestRead_RecognisesAQueuedDeliveryAfterItsDecoration(t *testing.T) {
	body := "[from @proj-2] please rebase"
	path := writeTranscript(t,
		line(t, typed("u1", "2026-10-01T10:00:00Z", "[AO queued 2026-10-01T09:00:00Z (1h ago), held while this session was asleep]\n"+body)),
		line(t, typed("u2", "2026-10-01T10:01:00Z", "[AO queued 2026-10-01T09:00:00Z (1h ago), held while this session was asleep]\nfrom the human, via the app")),
	)
	res, err := Read(path, 0, nil, Options{
		FileQuiet: true,
		Delivered: deliveredOf(map[string]Delivery{
			"from the human, via the app": {Author: domain.DeliveryAuthorHuman, Trigger: "send"},
		}),
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(res.Turns); len(got) != 1 || got[0] != "from the human, via the app" {
		t.Fatalf("turns = %q", got)
	}
	if res.Turns[0].Source != domain.LearnSourceAppSend {
		t.Errorf("source = %s, want app_send", res.Turns[0].Source)
	}
}

func TestRead_WindowsCarryTheAgentAroundTheTurn(t *testing.T) {
	path := writeTranscript(t,
		line(t, typed("u1", "2026-10-01T10:00:00Z", "check the screen")),
		line(t, assistantTool("a1", "2026-10-01T10:00:05Z", "Bash", fx{"command": "ao sim tap 0.5 0.5 TOKEN=abc", "description": "Tap the button"})),
		line(t, assistantTool("a2", "2026-10-01T10:00:06Z", "Skill", fx{"skill": "finnomena-app-e2e"})),
		line(t, toolResult("r1", "2026-10-01T10:00:07Z")),
		line(t, assistantText("a3", "2026-10-01T10:00:08Z", "I tapped through it by hand.")),
		line(t, typed("u2", "2026-10-01T10:01:00Z", "no, drive it only through a script")),
		line(t, assistantText("a4", "2026-10-01T10:01:05Z", "Understood, switching to the script store.")),
		line(t, assistantTool("a5", "2026-10-01T10:01:06Z", "Read", fx{"file_path": "/x/y/INDEX.md"})),
	)
	res, err := Read(path, 0, nil, Options{FileQuiet: true})
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Turns) != 2 {
		t.Fatalf("turns = %d", len(res.Turns))
	}
	second := res.Turns[1]
	if second.Before.AgentText != "I tapped through it by hand." {
		t.Errorf("before text = %q", second.Before.AgentText)
	}
	if got := strings.Join(second.Before.Actions, ","); got != "Bash(Tap the button),Skill(finnomena-app-e2e)" {
		t.Errorf("before actions = %q (a Bash command line must never be kept)", got)
	}
	if second.After.AgentText != "Understood, switching to the script store." || strings.Join(second.After.Actions, ",") != "Read(INDEX.md)" {
		t.Errorf("after = %+v", second.After)
	}
}

func TestRead_StopsAtAnOpenTurnAndResumesWithItsCarry(t *testing.T) {
	path := writeTranscript(t,
		line(t, assistantText("a0", "2026-10-01T09:59:00Z", "Should I spawn it?")),
		line(t, typed("u1", "2026-10-01T10:00:00Z", "yes, and keep it small")),
		line(t, assistantText("a1", "2026-10-01T10:00:05Z", "Spawning now.")),
	)
	first, err := Read(path, 0, nil, Options{FileQuiet: false})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Turns) != 0 || first.Carry == nil {
		t.Fatalf("an open turn must not be finalized: %+v", first)
	}
	if first.Carry.Before.AgentText != "Should I spawn it?" {
		t.Errorf("carry before = %q", first.Carry.Before.AgentText)
	}
	if first.HumanTurns != 0 {
		t.Errorf("the open turn is re-read next pass, so it must not be counted yet")
	}

	appendTranscript(t, path,
		line(t, assistantTool("a2", "2026-10-01T10:00:06Z", "Bash", fx{"description": "Spawn the worker"})),
		line(t, typed("u2", "2026-10-01T10:02:00Z", "thanks")),
	)
	second, err := Read(path, first.NextOffset, first.Carry, Options{FileQuiet: true})
	if err != nil {
		t.Fatal(err)
	}
	if got := texts(second.Turns); strings.Join(got, "|") != "yes, and keep it small|thanks" {
		t.Fatalf("turns = %q", got)
	}
	if second.Turns[0].Before.AgentText != "Should I spawn it?" {
		t.Errorf("resumed turn lost its before: %q", second.Turns[0].Before.AgentText)
	}
	if second.Turns[0].After.AgentText != "Spawning now." || strings.Join(second.Turns[0].After.Actions, ",") != "Bash(Spawn the worker)" {
		t.Errorf("resumed turn after = %+v", second.Turns[0].After)
	}
}

func TestRead_LeavesAPartialLineForTheNextPass(t *testing.T) {
	full := line(t, typed("u1", "2026-10-01T10:00:00Z", "first"))
	partial := strings.TrimSuffix(line(t, typed("u2", "2026-10-01T10:01:00Z", "second")), "\n")
	path := writeTranscript(t, full, partial[:len(partial)/2])
	res, err := Read(path, 0, nil, Options{FileQuiet: true})
	if err != nil {
		t.Fatal(err)
	}
	if res.NextOffset != int64(len(full)) {
		t.Errorf("next offset = %d, want %d (the end of the last complete line)", res.NextOffset, len(full))
	}
	if got := texts(res.Turns); len(got) != 1 || got[0] != "first" {
		t.Errorf("turns = %q", got)
	}
}

func TestRead_RedactsEverythingItKeeps(t *testing.T) {
	r := redact.New([]redact.Value{{Value: "uat.tester@example.com", Placeholder: "[account:nter-uat-1]", Kind: "account"}})
	path := writeTranscript(t,
		line(t, assistantText("a1", "2026-10-01T10:00:00Z", "Logged in as uat.tester@example.com.")),
		line(t, typed("u1", "2026-10-01T10:01:00Z", "use uat.tester@example.com, password: Hunter22!")),
	)
	res, err := Read(path, 0, nil, Options{FileQuiet: true, Redactor: r})
	if err != nil {
		t.Fatal(err)
	}
	tr := res.Turns[0]
	if strings.Contains(tr.Text, "uat.tester") || strings.Contains(tr.Text, "Hunter22") || strings.Contains(tr.Before.AgentText, "uat.tester") {
		t.Fatalf("a secret survived: %q / %q", tr.Text, tr.Before.AgentText)
	}
	if !strings.Contains(tr.Text, "[account:nter-uat-1]") {
		t.Errorf("account placeholder missing: %q", tr.Text)
	}
	// "password: ..." is caught by the secret-assignment rule or the password
	// rule, whichever runs first; what matters is that exactly one value went.
	if tr.Redactions["account"] != 2 || tr.Redactions["secret"]+tr.Redactions["password"] != 1 {
		t.Errorf("redactions = %v", tr.Redactions)
	}
}

func TestRead_PaneFingerprintsCoverEverySubmittedPrompt(t *testing.T) {
	brief := "the brief"
	path := writeTranscript(t,
		line(t, typed("u1", "2026-10-01T10:00:00Z", brief)),
		line(t, typed("u2", "2026-10-01T10:01:00Z", "a human  turn\n")),
		line(t, peer("p1", "2026-10-01T10:02:00Z", "socket message")),
	)
	res, err := Read(path, 0, nil, Options{
		FileQuiet: true,
		Delivered: deliveredOf(map[string]Delivery{brief: {Author: domain.DeliveryAuthorAO, Trigger: TriggerBrief}}),
	})
	if err != nil {
		t.Fatal(err)
	}
	fpBrief, _ := Fingerprint(brief)
	fpHuman, _ := Fingerprint("a human turn")
	// A socket delivery is submitted as a prompt too, and the submit hook fires
	// for it with the envelope alone - not the line the harness writes before
	// it nor the note after it (measured on a live session).
	fpPeer, _ := Fingerprint("<cross-session-message from-name=\"agent-orchestrator\">\nsocket message\n</cross-session-message>")
	if strings.Join(res.PaneFingerprints, ",") != fpBrief+","+fpHuman+","+fpPeer {
		t.Errorf("pane fingerprints = %v; the hook sees every submitted prompt, socket deliveries included", res.PaneFingerprints)
	}
}

func TestReadHead(t *testing.T) {
	path := writeTranscript(t,
		`{"type":"permission-mode","permissionMode":"auto"}`+"\n",
		line(t, typed("u1", "2026-10-01T10:00:00Z", "hi")),
	)
	h, err := ReadHead(path)
	if err != nil {
		t.Fatal(err)
	}
	if h.CWD != "/work/tree" || h.FirstAt.Format("15:04") != "10:00" {
		t.Errorf("head = %+v", h)
	}
}
