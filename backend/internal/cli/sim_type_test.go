package cli

import (
	"encoding/json"
	"net/http"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// What the daemon answers when the XCTest runner typed and proved it.
const simTypedBody = `{"udid":"087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1","app":"com.apple.SafariViewService",` +
	`"field":{"type":"TextField","label":"อีเมล","placeholder":"example@email.com"},` +
	`"landed":{"field":"อีเมล","path":"0.1.1.0.2.2","shown":"สวัสดี qa@a.co","evidence":"exact"},` +
	`"detail":"the field \"อีเมล\" [0.1.1.0.2.2] now reads \"สวัสดี qa@a.co\"","keyboard":true,"typingMs":420}`

func typeError(code, message string, details map[string]any) string {
	body, _ := json.Marshal(map[string]any{"error": "unprocessable", "code": code, "message": message, "details": details})
	return string(body)
}

func TestSimType_GoesThroughXCTestAndSaysWhereItLanded(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	daemon.typeStatus, daemon.typeBody = http.StatusOK, simTypedBody

	out, errOut, err := executeCLI(t, deps, "sim", "type", "สวัสดี qa@a.co")
	if err != nil {
		t.Fatalf("sim type: %v\nstderr=%s", err, errOut)
	}
	for _, want := range []string{
		`Typed 14 characters into TextField "อีเมล"`,
		`Checked on screen: the field "อีเมล" [0.1.1.0.2.2] now reads "สวัสดี qa@a.co"`,
		"Route: XCTest",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("output lacks %q:\n%s", want, out)
		}
	}
	if strings.Contains(out, "Fallback") {
		t.Fatalf("no fallback was taken:\n%s", out)
	}
	if len(driver.calls()) != 0 {
		t.Fatal("the XCTest route must not send key presses or touches of its own")
	}
	if len(daemon.typeRequests) != 1 || !strings.Contains(daemon.typeRequests[0], `"waitMs":15000`) {
		t.Fatalf("type requests = %q, want one that waits for a starting runner", daemon.typeRequests)
	}
	// The CLI takes no hold of its own: the daemon's route takes it for the
	// whole type and its proof.
	for _, call := range daemon.calls {
		if strings.HasPrefix(call, "POST ") && strings.HasSuffix(call, "/hold") {
			t.Fatal("the CLI took a gesture hold for an XCTest type; the daemon route holds it")
		}
	}
}

func TestSimType_JSONSaysTheRoute(t *testing.T) {
	deps, daemon := touchDeps(t, &fakeSimDriver{})
	daemon.typeStatus, daemon.typeBody = http.StatusOK, simTypedBody

	out, _, err := executeCLI(t, deps, "sim", "type", "สวัสดี qa@a.co", "--json")
	if err != nil {
		t.Fatalf("sim type --json: %v", err)
	}
	var got simGestureResult
	if err := json.Unmarshal([]byte(out), &got); err != nil {
		t.Fatalf("decode: %v\n%s", err, out)
	}
	if got.Via != "xctest" || got.Landed == nil || got.Landed.Evidence != "exact" || got.App != "com.apple.SafariViewService" {
		t.Fatalf("json = %+v", got)
	}
}

func TestSimType_ARunnerThatIsNotReadyFallsBackAndSaysSo(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, _ := touchDeps(t, driver) // the fake daemon's runner is "off"
	deps = withSimKeyboard(deps, simKeyboardUS, nil)

	out, _, err := executeCLI(t, deps, "sim", "type", "Hi 42")
	if err != nil {
		t.Fatalf("sim type: %v", err)
	}
	if !strings.Contains(out, "Fallback: XCTest typing was not available") || !strings.Contains(out, "claim it") {
		t.Fatalf("a fallback must say it happened and why:\n%s", out)
	}
	if len(driver.calls()) != 1 {
		t.Fatal("the fallback must still type")
	}
}

func TestSimType_ASecureFieldTheKeyboardCannotServeIsPasted(t *testing.T) {
	driver := &fakeSimDriver{}
	// A US guest: left to the planner this would go by key presses. The
	// runner said a keyboard route drops characters in this field, so it
	// must be the pasteboard.
	deps, daemon, pasteboard := pasteDeps(t, driver, simKeyboardUS, "รหัส")
	daemon.typeStatus = http.StatusUnprocessableEntity
	daemon.typeBody = typeError("SIM_TYPE_UNSUITABLE",
		"the focused field is a secure field, which takes only what the keyboard on screen can type, and \"ร\" is not plain ASCII", nil)

	out, _, err := executeCLI(t, deps, "sim", "type", "รหัส")
	if err != nil {
		t.Fatalf("sim type: %v", err)
	}
	if len(*pasteboard) == 0 || (*pasteboard)[0] != "รหัส" {
		t.Fatalf("pasteboard writes = %q, want the text pasted", *pasteboard)
	}
	if !strings.Contains(out, "Fallback: the focused field is a secure field") || !strings.Contains(out, "Pasted") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestSimType_NoFocusFailsWithoutAFallback(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	daemon.typeStatus = http.StatusUnprocessableEntity
	daemon.typeBody = typeError("SIM_TYPE_NO_FOCUS", "no element in com.example.app has keyboard focus (the keyboard is not up either)", nil)

	_, _, err := executeCLI(t, deps, "sim", "type", "hello")
	if err == nil || ExitCode(err) != 1 {
		t.Fatalf("err = %v, want exit 1", err)
	}
	if !strings.Contains(err.Error(), "nothing was typed") || !strings.Contains(err.Error(), "Tap the field first") {
		t.Fatalf("message: %v", err)
	}
	if len(driver.calls()) != 0 {
		t.Fatal("no field has focus: another route would type into nothing too")
	}
}

func TestSimType_UnprovenIsNeverRetriedAnotherWay(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	daemon.typeStatus = http.StatusUnprocessableEntity
	daemon.typeBody = typeError("SIM_TYPE_NOT_PROVEN", "it was sent, but nothing on screen can be shown to hold it", nil)

	_, _, err := executeCLI(t, deps, "sim", "type", "1234567890")
	if err == nil || !strings.Contains(err.Error(), "may be in the field") {
		t.Fatalf("err = %v, want the may-be-there failure", err)
	}
	if len(driver.calls()) != 0 || len(daemon.typeRequests) != 1 {
		t.Fatal("text that may have landed was sent again")
	}
}

func TestSimType_LongTextGoesInChunks(t *testing.T) {
	deps, daemon := touchDeps(t, &fakeSimDriver{})
	daemon.typeStatus, daemon.typeBody = http.StatusOK, simTypedBody
	text := strings.Repeat("abcd ", 50) // 250 characters

	if _, _, err := executeCLI(t, deps, "sim", "type", text); err != nil {
		t.Fatalf("sim type: %v", err)
	}
	if len(daemon.typeRequests) != 3 {
		t.Fatalf("sent %d requests, want 3 chunks of at most 100", len(daemon.typeRequests))
	}
	var sent strings.Builder
	for i, raw := range daemon.typeRequests {
		var in simTypeInput
		_ = json.Unmarshal([]byte(raw), &in)
		if (i == 0) != (in.WaitMs > 0) {
			t.Fatalf("request %d waitMs = %d: only the first waits for a starting runner", i, in.WaitMs)
		}
		sent.WriteString(in.Text)
	}
	if sent.String() != text {
		t.Fatal("the chunks do not add up to the text")
	}
}

func TestSimType_ControlCharactersTakeTheKeyRoute(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)
	deps = withSimKeyboard(deps, simKeyboardUS, nil)

	out, _, err := executeCLI(t, deps, "sim", "type", "hi\n")
	if err != nil {
		t.Fatalf("sim type: %v", err)
	}
	if len(daemon.typeRequests) != 0 {
		t.Fatal("text holding a Return was sent to XCTest")
	}
	if !strings.Contains(out, "Fallback: the text holds the control character") || !strings.Contains(out, "ao sim key") {
		t.Fatalf("output:\n%s", out)
	}
}

func TestSimType_RawKeysAndPasteSkipXCTest(t *testing.T) {
	for _, flag := range []string{"--raw-keys", "--paste"} {
		driver := &fakeSimDriver{}
		deps, daemon, _ := pasteDeps(t, driver, simKeyboardUS, "hello")
		if _, _, err := executeCLI(t, deps, "sim", "type", "hello", flag); err != nil {
			t.Fatalf("%s: %v", flag, err)
		}
		if len(daemon.typeRequests) != 0 {
			t.Fatalf("%s still asked XCTest to type", flag)
		}
	}
}

func TestSimKey_PressesTheKeyTheGivenNumberOfTimes(t *testing.T) {
	driver := &fakeSimDriver{}
	deps, daemon := touchDeps(t, driver)

	out, _, err := executeCLI(t, deps, "sim", "key", "backspace", "--times", "3")
	if err != nil {
		t.Fatalf("sim key: %v", err)
	}
	events := driver.calls()[0]
	press, _ := simbridge.Key("backspace")
	if len(events) != 3*len(press) {
		t.Fatalf("sent %d events, want three presses", len(events))
	}
	if !strings.Contains(out, "Pressed backspace x3") {
		t.Fatalf("output:\n%s", out)
	}
	if !strings.Contains(daemon.holdRequest, `"kind":"key"`) || !strings.Contains(daemon.holdRequest, `"name":"backspace"`) {
		t.Fatalf("hold intent = %s, want a key press a recording can keep", daemon.holdRequest)
	}
	assertHoldTakenAndReleased(t, daemon)
}

func TestSimKey_RefusesAnUnknownKeyAndABadCount(t *testing.T) {
	deps, _ := touchDeps(t, &fakeSimDriver{})
	for _, args := range [][]string{{"sim", "key", "escape"}, {"sim", "key", "enter", "--times", "0"}} {
		if _, _, err := executeCLI(t, deps, args...); err == nil || ExitCode(err) != 2 {
			t.Fatalf("%v: err = %v, want exit 2", args, err)
		}
	}
}
