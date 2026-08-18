package controllers_test

import (
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrecord"
)

// flowView mirrors controllers.SimFlowView for decoding. It is spelled out
// rather than imported so a field quietly disappearing from the wire fails
// here rather than compiling away.
type flowView struct {
	Name             string    `json:"name"`
	FileName         string    `json:"fileName"`
	Path             string    `json:"path"`
	RecordedAt       time.Time `json:"recordedAt"`
	TimeFromFileName bool      `json:"timeFromFileName"`
	Steps            int       `json:"steps"`
	Review           int       `json:"review"`
	CountsKnown      bool      `json:"countsKnown"`
	Bytes            int64     `json:"bytes"`
}

type stopResponse struct {
	Recording domain.SimRecording `json:"recording"`
	StepCount int                 `json:"stepCount"`
	Steps     []struct {
		Seq int64 `json:"seq"`
	} `json:"steps"`
	Flow *flowView `json:"flow"`
}

// tapStep is a step that resolved to a unique label - nothing to review.
func tapStep(seq int64, label string) domain.SimRecordingStep {
	return domain.SimRecordingStep{
		Seq: seq, Kind: "tap", Selector: label, SelectorRung: 1, Ambiguity: 1,
		X: 0.5, Y: 0.5, ToX: 0.5, ToY: 0.5,
	}
}

// guessedStep fell through to an index: several elements share the label and
// nothing pinned down which. This is the shape the review count exists for.
func guessedStep(seq int64, label string) domain.SimRecordingStep {
	return domain.SimRecordingStep{
		Seq: seq, Kind: "tap", Selector: label, SelectorRung: 2, SelectorIndex: 1, Ambiguity: 3,
		X: 0.5, Y: 0.5, ToX: 0.5, ToY: 0.5,
	}
}

func stoppedRecording(name string, at time.Time) domain.SimRecording {
	return domain.SimRecording{
		UDID: testSimUDID, SessionID: "mer-1", Name: name,
		StartedAt: at.Add(-time.Minute), StoppedAt: &at, UpdatedAt: at,
	}
}

// Stopping writes the flow, and says where it went and what is in it. This is
// the whole point of the route: a caller with no Go in it - the Device tab -
// gets a file and three numbers, not an array of steps it would have to
// interpret.
func TestStopSimRecording_WritesTheFlowAndReportsIt(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	svc := newFakeSimServiceWithRecording()
	svc.stopRec = stoppedRecording("Login to Portfolio", at)
	svc.stopSteps = []domain.SimRecordingStep{tapStep(1, "Home"), guessedStep(2, "Buy"), tapStep(3, "Done")}
	srv := newSimTestServerIn(t, svc, dataDir)

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d, want 200: %s", status, body)
	}
	var res stopResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v: %s", err, body)
	}
	if res.Flow == nil {
		t.Fatalf("stopping must report the flow it wrote: %s", body)
	}
	if res.StepCount != 3 {
		t.Errorf("stepCount = %d, want 3", res.StepCount)
	}
	if res.Flow.Steps != 3 || res.Flow.Review != 1 || !res.Flow.CountsKnown {
		t.Errorf("flow counts = %d steps / %d review / known=%v, want 3 / 1 / true",
			res.Flow.Steps, res.Flow.Review, res.Flow.CountsKnown)
	}
	// The name the recording was started with becomes the file name, so a
	// recording named from the CLI and one named in the app land the same way.
	if res.Flow.FileName != "login-to-portfolio-20260818-045722.711Z.yaml" {
		t.Errorf("fileName = %q", res.Flow.FileName)
	}
	if !filepath.IsAbs(res.Flow.Path) {
		t.Errorf("path = %q, want absolute", res.Flow.Path)
	}
	if want := simrecord.FlowsDir(dataDir, "mer-1"); filepath.Dir(res.Flow.Path) != want {
		t.Errorf("path %q is not in the session's flows directory %q", res.Flow.Path, want)
	}

	written, err := os.ReadFile(res.Flow.Path)
	if err != nil {
		t.Fatalf("the reported path must exist: %v", err)
	}
	content := string(written)
	if !strings.Contains(content, "appId: ${APP_ID}") {
		t.Errorf("flow missing the literal appId placeholder:\n%s", content)
	}
	if !strings.Contains(content, `tapOn: "Home"`) {
		t.Errorf("flow missing the recorded tap:\n%s", content)
	}
	if strings.Contains(content, "launchApp") {
		t.Errorf("flow must never fabricate launchApp:\n%s", content)
	}
	// The number reported and the number in the file are the same number.
	if !strings.Contains(content, "REVIEW REQUIRED: 1 of 3 steps") {
		t.Errorf("the flow's own banner must agree with the reported review count:\n%s", content)
	}
}

// An unnamed recording still produces a working file. Naming happens
// afterwards, from the list, so refusing to write one until it has a name
// would lose gestures somebody performed by hand.
func TestStopSimRecording_UnnamedRecordingStillWritesAFlow(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	svc := newFakeSimServiceWithRecording()
	svc.stopRec = stoppedRecording("", at)
	svc.stopSteps = []domain.SimRecordingStep{tapStep(1, "Home")}
	srv := newSimTestServerIn(t, svc, dataDir)

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var res stopResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Flow == nil || res.Flow.FileName != "20260818-045722.711Z.yaml" {
		t.Fatalf("flow = %+v, want a timestamp-only file name", res.Flow)
	}
	if res.Flow.Name != "" {
		t.Errorf("name = %q, want empty until a human names it", res.Flow.Name)
	}
}

// A recording with no steps is a real outcome - a human pressed record and
// then thought better of it - and it must still close cleanly.
func TestStopSimRecording_EmptyRecordingWritesAHeaderOnlyFlow(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	svc := newFakeSimServiceWithRecording()
	svc.stopRec = stoppedRecording("", at)
	srv := newSimTestServerIn(t, svc, dataDir)

	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var res stopResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Flow == nil || res.Flow.Steps != 0 || !res.Flow.CountsKnown {
		t.Fatalf("flow = %+v, want a measured zero rather than an unmeasured one", res.Flow)
	}
}

func TestStopSimRecording_EntryAndOutAreHonoured(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	out := filepath.Join(t.TempDir(), "nested", "flow.yaml")
	svc := newFakeSimServiceWithRecording()
	svc.stopRec = stoppedRecording("named", at)
	svc.stopSteps = []domain.SimRecordingStep{tapStep(1, "Home")}
	srv := newSimTestServerIn(t, svc, dataDir)

	url := "/api/v1/sessions/mer-1/sim-recordings/" + testSimUDID +
		"?entry=" + "../flows/sign-in.yaml" + "&out=" + out
	body, status, _ := doRequest(t, srv, "DELETE", url, "")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var res stopResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Flow == nil || res.Flow.Path != out {
		t.Fatalf("flow = %+v, want it written to %q", res.Flow, out)
	}
	written, err := os.ReadFile(out)
	if err != nil {
		t.Fatalf("read %s: %v", out, err)
	}
	if !strings.Contains(string(written), `- runFlow: "../flows/sign-in.yaml"`) {
		t.Errorf("entry point missing:\n%s", written)
	}
}

// A relative --out is refused rather than resolved against whatever directory
// the daemon happens to be running in, which is never the one the caller meant.
func TestStopSimRecording_RelativeOutIsRefusedAndSaysTheRecordingStopped(t *testing.T) {
	svc := newFakeSimServiceWithRecording()
	svc.stopRec = stoppedRecording("", time.Now().UTC())
	svc.stopSteps = []domain.SimRecordingStep{tapStep(1, "Home")}
	srv := newSimTestServerIn(t, svc, t.TempDir())

	body, status, _ := doRequest(t, srv, "DELETE",
		"/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID+"?out=relative/flow.yaml", "")
	if status != http.StatusInternalServerError {
		t.Fatalf("status = %d, want 500: %s", status, body)
	}
	// The recording DID stop. Saying otherwise would invite a retry that finds
	// no open recording and reads as though the gestures were never captured.
	if !strings.Contains(string(body), "the recording stopped") {
		t.Errorf("the failure must say the recording still stopped: %s", body)
	}
}

// Reading a recording answers how many steps it has without being asked for
// the steps themselves. The Device tab's counter asks once a second while
// somebody drags; every one of those answers would otherwise carry every
// selector captured so far.
func TestGetSimRecording_StepsNoneReturnsTheCountAlone(t *testing.T) {
	svc := newFakeSimServiceWithRecording()
	svc.getFound = true
	svc.getRec = domain.SimRecording{UDID: testSimUDID, SessionID: "mer-1"}
	svc.getSteps = []domain.SimRecordingStep{tapStep(1, "Home"), tapStep(2, "Next"), tapStep(3, "Done")}
	srv := newSimTestServer(t, svc)

	for _, tc := range []struct {
		query     string
		wantSteps int
	}{
		{"", 3},
		{"?steps=all", 3},
		{"?steps=none", 0},
	} {
		body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID+tc.query, "")
		if status != http.StatusOK {
			t.Fatalf("status = %d: %s", status, body)
		}
		var res stopResponse
		if err := json.Unmarshal(body, &res); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if res.StepCount != 3 {
			t.Errorf("%q: stepCount = %d, want 3 whatever the steps are", tc.query, res.StepCount)
		}
		if len(res.Steps) != tc.wantSteps {
			t.Errorf("%q: returned %d steps, want %d", tc.query, len(res.Steps), tc.wantSteps)
		}
	}
}

// --- the flows list ---------------------------------------------------------

// writeFlowVia seeds a flow on disk through the production path - stopping a
// recording - so the list is never tested against a file a test hand-wrote to
// agree with it.
func writeFlowVia(t *testing.T, dataDir, name string, at time.Time, steps []domain.SimRecordingStep) flowView {
	t.Helper()
	svc := newFakeSimServiceWithRecording()
	svc.stopRec = stoppedRecording(name, at)
	svc.stopSteps = steps
	server := newSimTestServerIn(t, svc, dataDir)
	body, status, _ := doRequest(t, server, "DELETE", "/api/v1/sessions/mer-1/sim-recordings/"+testSimUDID, "")
	if status != http.StatusOK {
		t.Fatalf("stopping to seed a flow failed: %d %s", status, body)
	}
	var res stopResponse
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Flow == nil {
		t.Fatalf("no flow written: %s", body)
	}
	return *res.Flow
}

func TestListSimFlows_ReportsWhatWasRecorded(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	first := writeFlowVia(t, dataDir, "first take", at, []domain.SimRecordingStep{tapStep(1, "Home"), guessedStep(2, "Buy")})
	second := writeFlowVia(t, dataDir, "second take", at.Add(time.Minute), []domain.SimRecordingStep{tapStep(1, "Home")})

	svc := newFakeSimServiceWithRecording()
	srv := newSimTestServerIn(t, svc, dataDir)
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/sim-flows", "")
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var res struct {
		Flows []flowView `json:"flows"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Flows) != 2 {
		t.Fatalf("listed %d flows, want 2: %+v", len(res.Flows), res.Flows)
	}
	// Newest first: the recording somebody wants is nearly always the one they
	// just made.
	if res.Flows[0].FileName != second.FileName || res.Flows[1].FileName != first.FileName {
		t.Errorf("order = %q, %q, want newest first", res.Flows[0].FileName, res.Flows[1].FileName)
	}
	if res.Flows[1].Steps != 2 || res.Flows[1].Review != 1 {
		t.Errorf("first take listed as %d steps / %d review, want 2 / 1", res.Flows[1].Steps, res.Flows[1].Review)
	}
	if res.Flows[0].Name != "second-take" {
		t.Errorf("name = %q, want the name read back out of the file name", res.Flows[0].Name)
	}
}

func TestListSimFlows_EmptyWhenNothingRecorded(t *testing.T) {
	srv := newSimTestServerIn(t, newFakeSimServiceWithRecording(), t.TempDir())
	body, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/sim-flows", "")
	if status != http.StatusOK {
		t.Fatalf("a session that recorded nothing is not an error: %d %s", status, body)
	}
	var res struct {
		Flows []flowView `json:"flows"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Flows) != 0 {
		t.Errorf("flows = %+v, want empty", res.Flows)
	}
}

// One session's recordings are its own. The route is scoped by session both in
// the URL and on disk, and this pins that they agree.
func TestListSimFlows_IsScopedToTheSession(t *testing.T) {
	dataDir := t.TempDir()
	writeFlowVia(t, dataDir, "mine", time.Now().UTC(), []domain.SimRecordingStep{tapStep(1, "Home")})

	srv := newSimTestServerIn(t, newFakeSimServiceWithRecording(), dataDir)
	body, _, _ := doRequest(t, srv, "GET", "/api/v1/sessions/someone-else/sim-flows", "")
	var res struct {
		Flows []flowView `json:"flows"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if len(res.Flows) != 0 {
		t.Errorf("another session sees %+v, want nothing", res.Flows)
	}
}

func TestRenameSimFlow_NamesItAndKeepsTheRecordingTimestamp(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	original := writeFlowVia(t, dataDir, "", at, []domain.SimRecordingStep{tapStep(1, "Home"), guessedStep(2, "Buy")})

	srv := newSimTestServerIn(t, newFakeSimServiceWithRecording(), dataDir)
	body, status, _ := doRequest(t, srv, "PATCH",
		"/api/v1/sessions/mer-1/sim-flows/"+original.FileName, `{"name":"Login to Portfolio"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var res struct {
		Flow flowView `json:"flow"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Flow.FileName != "login-to-portfolio-20260818-045722.711Z.yaml" {
		t.Errorf("fileName = %q", res.Flow.FileName)
	}
	if !res.Flow.RecordedAt.Equal(at) {
		t.Errorf("recordedAt = %s, want the recording's own time %s", res.Flow.RecordedAt, at)
	}
	if res.Flow.Steps != 2 || res.Flow.Review != 1 {
		t.Errorf("renaming changed the counts: %+v", res.Flow)
	}
	if _, err := os.Stat(original.Path); !os.IsNotExist(err) {
		t.Errorf("the old file is still there: %v", err)
	}
}

// A name in Thai is the realistic case for this human, and it has to survive
// being written to disk and read back.
func TestRenameSimFlow_KeepsANonASCIIName(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	original := writeFlowVia(t, dataDir, "", at, []domain.SimRecordingStep{tapStep(1, "Home")})

	srv := newSimTestServerIn(t, newFakeSimServiceWithRecording(), dataDir)
	body, status, _ := doRequest(t, srv, "PATCH",
		"/api/v1/sessions/mer-1/sim-flows/"+original.FileName, `{"name":"เข้าสู่ระบบ แล้วไปหน้าแรก"}`)
	if status != http.StatusOK {
		t.Fatalf("status = %d: %s", status, body)
	}
	var res struct {
		Flow flowView `json:"flow"`
	}
	if err := json.Unmarshal(body, &res); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if res.Flow.Name != "เข้าสู่ระบบ-แล้วไปหน้าแรก" {
		t.Errorf("name = %q, want the Thai name intact", res.Flow.Name)
	}
	if _, err := os.Stat(res.Flow.Path); err != nil {
		t.Errorf("the renamed file must exist at the reported path: %v", err)
	}
}

func TestDeleteSimFlow_RemovesExactlyOne(t *testing.T) {
	dataDir := t.TempDir()
	at := time.Date(2026, 8, 18, 4, 57, 22, 711_000_000, time.UTC)
	keep := writeFlowVia(t, dataDir, "keep", at, []domain.SimRecordingStep{tapStep(1, "Home")})
	drop := writeFlowVia(t, dataDir, "drop", at.Add(time.Minute), []domain.SimRecordingStep{tapStep(1, "Home")})

	srv := newSimTestServerIn(t, newFakeSimServiceWithRecording(), dataDir)
	body, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-flows/"+drop.FileName, "")
	if status != http.StatusNoContent {
		t.Fatalf("status = %d, want 204: %s", status, body)
	}
	if _, err := os.Stat(drop.Path); !os.IsNotExist(err) {
		t.Errorf("the deleted flow is still on disk: %v", err)
	}
	if _, err := os.Stat(keep.Path); err != nil {
		t.Errorf("deleting one must not touch the other: %v", err)
	}
}

func TestDeleteSimFlow_UnknownIs404(t *testing.T) {
	srv := newSimTestServerIn(t, newFakeSimServiceWithRecording(), t.TempDir())
	_, status, _ := doRequest(t, srv, "DELETE",
		"/api/v1/sessions/mer-1/sim-flows/never-recorded-20260818-045722.711Z.yaml", "")
	if status != http.StatusNotFound {
		t.Errorf("status = %d, want 404", status)
	}
}

// A recording cannot be regenerated without replaying the whole path by hand,
// so a name that addresses something other than one of this session's flows is
// refused rather than interpreted.
func TestDeleteSimFlow_RefusesAPathRatherThanFollowingIt(t *testing.T) {
	dataDir := t.TempDir()
	outside := filepath.Join(dataDir, "outside.yaml")
	if err := os.WriteFile(outside, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	srv := newSimTestServerIn(t, newFakeSimServiceWithRecording(), dataDir)

	for _, name := range []string{"..%2F..%2Foutside.yaml", "..%5Coutside.yaml", "shot.png"} {
		_, status, _ := doRequest(t, srv, "DELETE", "/api/v1/sessions/mer-1/sim-flows/"+name, "")
		if status == http.StatusNoContent {
			t.Errorf("deleting %q was allowed", name)
		}
	}
	if _, err := os.Stat(outside); err != nil {
		t.Errorf("a file outside the flows directory was removed: %v", err)
	}
}

// A daemon with no data directory has nowhere to look, and says so rather than
// reporting an empty list as though the session had recorded nothing.
func TestSimFlows_WithoutADataDirectoryAre501(t *testing.T) {
	srv := newSimTestServer(t, newFakeSimServiceWithRecording())
	if _, status, _ := doRequest(t, srv, "GET", "/api/v1/sessions/mer-1/sim-flows", ""); status != http.StatusNotImplemented {
		t.Errorf("status = %d, want 501", status)
	}
}

// --- what the emitted flow contains -----------------------------------------
//
// These assertions used to live in the CLI, which built the flow itself. They
// follow the emitter to the daemon rather than being dropped: the Device tab
// stops recordings through this same route, so this is now the only path that
// turns a recorded step into YAML.

// A step whose selector matched more than one element on screen must re-emit
// at the SAME index the human actually tapped - here the second of three -
// not index 0. A flow whose selector text reads correctly while taking the
// first matching element is precisely the failure the selector ladder exists
// to prevent.
func TestStopSimRecording_DuplicateLabelTapKeepsTheRecordedIndex(t *testing.T) {
	dataDir := t.TempDir()
	flow := writeFlowVia(t, dataDir, "", time.Now().UTC(), []domain.SimRecordingStep{{
		Seq: 1, Kind: "tap", Selector: "Continue", SelectorRung: 2, SelectorIndex: 2, Ambiguity: 3,
		X: 0.5, Y: 0.7, ToX: 0.5, ToY: 0.7,
	}})

	content := readFlowFile(t, flow.Path)
	if !strings.Contains(content, "index: 2") {
		t.Errorf("flow must tap the element actually recorded, not default to index 0:\n%s", content)
	}
	if strings.Contains(content, "index: 0") {
		t.Errorf("flow fell back to index 0 - the wrong element:\n%s", content)
	}
	// And a guessed step is the kind a human is asked to check.
	if flow.Review != 1 {
		t.Errorf("review = %d, want 1: an index is a guess", flow.Review)
	}
}

// A recorded selector is STORED escaped, because Maestro matches text as a
// regex. Re-emitting has to keep it escaped where it is matched on and
// unescape it where a human reads it.
func TestStopSimRecording_EscapedSelectorKeepsItsEscapingAndExplainsIt(t *testing.T) {
	dataDir := t.TempDir()
	flow := writeFlowVia(t, dataDir, "", time.Now().UTC(), []domain.SimRecordingStep{{
		Seq: 1, Kind: "tap", Selector: `See all \(12\)`, SelectorRung: 1, Ambiguity: 1, X: 0.5, Y: 0.7,
	}})

	content := readFlowFile(t, flow.Path)
	if !strings.Contains(content, `- tapOn: "See all \\(12\\)"`) {
		t.Errorf("the matcher must keep the escaping it was recorded with:\n%s", content)
	}
	if !strings.Contains(content, "# escaped: the label contains regex characters") {
		t.Errorf("an escaped selector must carry its explanation:\n%s", content)
	}
}

// The other half: scrollUntilVisible matches on the label a human reads, so
// feeding it the stored (escaped) text would search for backslashes that are
// not in the label.
func TestStopSimRecording_OffScreenEscapedSelectorScrollsToThePlainLabel(t *testing.T) {
	dataDir := t.TempDir()
	flow := writeFlowVia(t, dataDir, "", time.Now().UTC(), []domain.SimRecordingStep{{
		Seq: 1, Kind: "tap", Selector: `See all \(12\)`, SelectorRung: 1, Ambiguity: 1, OffScreen: true, X: 0.5, Y: 0.7,
	}})

	content := readFlowFile(t, flow.Path)
	if !strings.Contains(content, `    element: "See all (12)"`) {
		t.Errorf("scrollUntilVisible must search for the label a human reads:\n%s", content)
	}
	if strings.Contains(content, `element: "See all \\(12\\)"`) {
		t.Errorf("the escaped pattern reached the scroll target:\n%s", content)
	}
}

func readFlowFile(t *testing.T, path string) string {
	t.Helper()
	data, err := os.ReadFile(path) //nolint:gosec // a path this test just created
	if err != nil {
		t.Fatalf("read %s: %v", path, err)
	}
	return string(data)
}
