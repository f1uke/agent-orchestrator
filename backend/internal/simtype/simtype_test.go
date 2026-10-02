package simtype

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simgesture"
	"github.com/aoagents/agent-orchestrator/backend/internal/simpaste"
	"github.com/aoagents/agent-orchestrator/backend/internal/simrunner"
)

const udid = "087DF306-1FC9-4E5A-B9ED-AD36D6A1A0F1"

// --- fakes -----------------------------------------------------------------

type fakeHolder struct {
	err       error
	ttl       time.Duration
	acquired  int
	released  int
	performed bool
}

func (h *fakeHolder) Acquire(_ context.Context, _ string, ttl time.Duration) (string, error) {
	h.ttl = ttl
	if h.err != nil {
		return "", h.err
	}
	h.acquired++
	return "tok", nil
}

func (h *fakeHolder) Release(_ context.Context, _, _ string, outcome simgesture.Outcome) {
	h.released++
	h.performed = outcome.Performed
}

// fakeReader answers each read with the next screen, the last repeating.
type fakeReader struct {
	screens []simbridge.Snapshot
	reads   int
	err     error
}

func (r *fakeReader) AX(context.Context, string) (simbridge.Snapshot, error) {
	if r.err != nil {
		return simbridge.Snapshot{}, r.err
	}
	i := min(r.reads, len(r.screens)-1)
	r.reads++
	return r.screens[i], nil
}

type fakeTypist struct {
	focus    simrunner.TypeAnswer
	focusErr error
	answer   simrunner.TypeAnswer
	typeErr  error
	typed    []string
	opts     []simrunner.TypeOptions
}

func (f *fakeTypist) Focus(context.Context, string) (simrunner.TypeAnswer, error) {
	return f.focus, f.focusErr
}

func (f *fakeTypist) Type(_ context.Context, _, text string, opts simrunner.TypeOptions) (simrunner.TypeAnswer, error) {
	f.typed = append(f.typed, text)
	f.opts = append(f.opts, opts)
	return f.answer, f.typeErr
}

// --- screens ---------------------------------------------------------------

// field is one text field on an otherwise plain screen, as the runner reads
// it: an empty web field reports its placeholder AS its value, and only once it
// holds text reports the placeholder apart.
func screen(field simbridge.XCTestNode, keyboard *simbridge.XCTestNode) simbridge.Snapshot {
	children := []simbridge.XCTestNode{
		{Type: "StaticText", Label: "Sign in", Value: "Sign in", Enabled: true, Frame: simbridge.Rect{X: 20, Y: 100, Width: 200, Height: 30}},
		field,
	}
	if keyboard != nil {
		children = append(children, *keyboard)
	}
	return simbridge.SnapshotFromXCTest(simbridge.XCTestHierarchy{
		Version: simrunner.WireVersion,
		Screen:  simbridge.Size{Width: 402, Height: 874},
		Apps: []simbridge.XCTestApp{{BundleID: "com.example.app", Tree: simbridge.XCTestNode{
			Type: "Application", Label: "Example", Enabled: true, Frame: simbridge.Rect{Width: 402, Height: 874},
			Children: children,
		}}},
	})
}

func textField(kind, label, value, placeholder string) simbridge.XCTestNode {
	return simbridge.XCTestNode{Type: kind, Label: label, Value: value, Placeholder: placeholder, Enabled: true,
		Focused: true, Frame: simbridge.Rect{X: 20, Y: 300, Width: 360, Height: 44}}
}

func keyboard(letters ...string) *simbridge.XCTestNode {
	board := simbridge.XCTestNode{Type: "Keyboard", Enabled: true, Frame: simbridge.Rect{Y: 580, Width: 402, Height: 290}}
	for i, l := range letters {
		board.Children = append(board.Children, simbridge.XCTestNode{Type: "Key", Label: l, Enabled: true,
			Frame: simbridge.Rect{X: float64(10 + 36*i), Y: 600, Width: 32, Height: 40}})
	}
	return &board
}

func focused(kind, label string) simrunner.TypeAnswer {
	return simrunner.TypeAnswer{Version: simrunner.WireVersion, App: "com.example.app", Keyboard: true,
		Field: &simrunner.TypeField{Type: kind, Label: label}}
}

func typed(kind, label string) simrunner.TypeAnswer {
	a := focused(kind, label)
	a.Typed, a.TypingMs = true, 300
	return a
}

// --- Run -------------------------------------------------------------------

func TestRun_TypesAndProvesItInTheFocusedField(t *testing.T) {
	holder := &fakeHolder{}
	reader := &fakeReader{screens: []simbridge.Snapshot{
		screen(textField("TextField", "email", "example@email.com", ""), keyboard("ก", "ข")),
		screen(textField("TextField", "email", "qa@a.co", "example@email.com"), keyboard("ก", "ข")),
	}}
	typist := &fakeTypist{focus: focused("TextField", "email"), answer: typed("TextField", "email")}

	result, err := Run(context.Background(), holder, reader, typist, udid, "qa@a.co")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Landing.How != simpaste.EvidenceExact || result.Landing.Field != "email" || result.Landing.Shown != "qa@a.co" {
		t.Fatalf("landing = %+v, want the email field reading the text exactly", result.Landing)
	}
	if result.App != "com.example.app" || result.Field.Label != "email" || result.TypingMs != 300 {
		t.Fatalf("result = %+v, want the runner's account of where it typed", result)
	}
	// An ordinary field is typed on whatever keyboard is up: a Thai layout
	// types Latin text into it in full.
	if len(typist.opts) != 1 || typist.opts[0].Layout != "" {
		t.Fatalf("opts = %+v, want no layout switch for an ordinary field", typist.opts)
	}
	if holder.acquired != 1 || holder.released != 1 || !holder.performed {
		t.Fatalf("hold = %+v, want one taken and given back as performed", holder)
	}
	if holder.ttl > time.Minute {
		t.Fatalf("hold ttl %s is over the lease service's one-minute ceiling", holder.ttl)
	}
}

func TestRun_ProvesOneCharacterOnAScreenFullOfIt(t *testing.T) {
	// The empty field reads its placeholder "your name", which holds an "a"
	// already: counting copies across the screen sees one "a" before and one
	// after, and cannot tell a typed character from a hint that went away. The
	// focused field's own value can.
	before := screen(textField("TextField", "name", "your name", ""), nil)
	after := screen(textField("TextField", "name", "a", "your name"), nil)
	typist := &fakeTypist{focus: focused("TextField", "name"), answer: typed("TextField", "name")}

	result, err := Run(context.Background(), &fakeHolder{}, &fakeReader{screens: []simbridge.Snapshot{before, after}},
		typist, udid, "a")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if result.Landing.Shown != "a" {
		t.Fatalf("landing = %+v", result.Landing)
	}
}

func TestRun_NoFocusTypesNothing(t *testing.T) {
	holder := &fakeHolder{}
	typist := &fakeTypist{focus: simrunner.TypeAnswer{Version: simrunner.WireVersion, Keyboard: false,
		Checked: []string{"com.example.app"},
		Error:   &simrunner.TypeError{Code: simrunner.TypeNoFocus, Message: "nothing has focus"}}}
	reader := &fakeReader{screens: []simbridge.Snapshot{screen(textField("TextField", "email", "", ""), nil)}}

	_, err := Run(context.Background(), holder, reader, typist, udid, "hello")
	var noFocus *NoFocusError
	if !errors.As(err, &noFocus) || !errors.Is(err, ErrNoFocus) {
		t.Fatalf("err = %v, want NoFocusError", err)
	}
	if !strings.Contains(err.Error(), "com.example.app") || !strings.Contains(err.Error(), "keyboard is not up") {
		t.Fatalf("the error must say where it looked and that the keyboard is down: %v", err)
	}
	if len(typist.typed) != 0 {
		t.Fatalf("typed %q with nothing focused", typist.typed)
	}
	if holder.released != 1 || holder.performed {
		t.Fatalf("hold = %+v, want it given back as NOT performed", holder)
	}
}

func TestRun_ARunnerThatIsNotThereIsUnavailable(t *testing.T) {
	typist := &fakeTypist{focusErr: simrunner.ErrNotReady}
	reader := &fakeReader{screens: []simbridge.Snapshot{screen(textField("TextField", "email", "", ""), nil)}}

	_, err := Run(context.Background(), &fakeHolder{}, reader, typist, udid, "hello")
	if !errors.Is(err, ErrUnavailable) {
		t.Fatalf("err = %v, want ErrUnavailable - nothing was sent, so the caller may take another route", err)
	}
	if len(typist.typed) != 0 {
		t.Fatal("typed through a runner that is not there")
	}
}

func TestRun_AHoldThatIsRefusedSendsNothing(t *testing.T) {
	refused := errors.New("leased by someone else")
	reader := &fakeReader{}
	typist := &fakeTypist{}

	_, err := Run(context.Background(), &fakeHolder{err: refused}, reader, typist, udid, "hello")
	if !errors.Is(err, refused) {
		t.Fatalf("err = %v, want the hold's refusal", err)
	}
	if reader.reads != 0 || len(typist.typed) != 0 {
		t.Fatal("a refused hold must not read or type anything")
	}
}

func TestRun_SecureFieldOnANonLatinKeyboardIsTypedOnLatinLetters(t *testing.T) {
	before := screen(textField("SecureTextField", "password", "", ""), keyboard("ก", "ข", "ค"))
	after := screen(textField("SecureTextField", "password", "••••••••", ""), keyboard("ก", "ข", "ค"))
	answer := typed("SecureTextField", "password")
	answer.KeyboardSwitchedTo, answer.KeyboardRestored = "English (US)", true
	typist := &fakeTypist{focus: focused("SecureTextField", "password"), answer: answer}

	result, err := Run(context.Background(), &fakeHolder{}, &fakeReader{screens: []simbridge.Snapshot{before, after}},
		typist, udid, "finno123")
	if err != nil {
		t.Fatalf("Run: %v", err)
	}
	if typist.opts[0].Layout != simrunner.LayoutLatin {
		t.Fatalf("layout = %q, want the keyboard switched to Latin letters", typist.opts[0].Layout)
	}
	if result.Landing.How != simpaste.EvidenceMasked {
		t.Fatalf("evidence = %q, want masked", result.Landing.How)
	}
	if result.KeyboardSwitchedTo != "English (US)" || !result.KeyboardRestored {
		t.Fatalf("result = %+v, want the switch and its restore reported", result)
	}
}

func TestRun_SecureFieldOnALatinKeyboardIsTypedAsItIs(t *testing.T) {
	before := screen(textField("SecureTextField", "password", "", ""), keyboard("q", "w", "e"))
	after := screen(textField("SecureTextField", "password", "•••", ""), keyboard("q", "w", "e"))
	typist := &fakeTypist{focus: focused("SecureTextField", "password"), answer: typed("SecureTextField", "password")}

	if _, err := Run(context.Background(), &fakeHolder{}, &fakeReader{screens: []simbridge.Snapshot{before, after}},
		typist, udid, "abc"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if typist.opts[0].Layout != "" {
		t.Fatalf("layout = %q, want none on a keyboard that already has Latin letters", typist.opts[0].Layout)
	}
}

func TestRun_SecureFieldsTheKeyboardCannotServeAreLeftToThePasteboard(t *testing.T) {
	for name, tc := range map[string]struct {
		keyboard *simbridge.XCTestNode
		text     string
		want     string
	}{
		"no keyboard on screen": {nil, "finno123", "no software keyboard"},
		"Thai text":             {keyboard("ก"), "รหัส", "not plain ASCII"},
		"accented text":         {keyboard("q"), "café", "not plain ASCII"},
	} {
		t.Run(name, func(t *testing.T) {
			holder := &fakeHolder{}
			typist := &fakeTypist{focus: focused("SecureTextField", "password")}
			reader := &fakeReader{screens: []simbridge.Snapshot{screen(textField("SecureTextField", "password", "", ""), tc.keyboard)}}

			_, err := Run(context.Background(), holder, reader, typist, udid, tc.text)
			var unsuitable *UnsuitableError
			if !errors.As(err, &unsuitable) || !errors.Is(err, ErrUnsuitable) {
				t.Fatalf("err = %v, want UnsuitableError", err)
			}
			if !strings.Contains(err.Error(), tc.want) || !strings.Contains(err.Error(), "secure field") {
				t.Fatalf("the reason must say why: %v", err)
			}
			if len(typist.typed) != 0 {
				t.Fatal("typed into a secure field that would drop characters")
			}
			if holder.performed {
				t.Fatal("a type that sent nothing was recorded as performed")
			}
		})
	}
}

func TestRun_XCTestRefusingWithNothingOnScreenChangedIsRefused(t *testing.T) {
	same := screen(textField("TextField", "email", "", ""), nil)
	answer := focused("TextField", "email")
	answer.Error = &simrunner.TypeError{Code: simrunner.TypeFailed, Message: "Neither element nor any descendant has keyboard focus."}
	typist := &fakeTypist{focus: focused("TextField", "email"), answer: answer}

	_, err := Run(context.Background(), &fakeHolder{}, &fakeReader{screens: []simbridge.Snapshot{same, same}},
		typist, udid, "hello")
	if !errors.Is(err, ErrRefused) {
		t.Fatalf("err = %v, want ErrRefused: XCTest said no and nothing arrived", err)
	}
	if !strings.Contains(err.Error(), "keyboard focus") {
		t.Fatalf("the refusal must carry what XCTest said: %v", err)
	}
}

func TestRun_TextThatArrivedOnlyInPartIsNotProven(t *testing.T) {
	before := screen(textField("TextField", "code", "", ""), nil)
	after := screen(textField("TextField", "code", "12345", ""), nil) // a five-character limit
	typist := &fakeTypist{focus: focused("TextField", "code"), answer: typed("TextField", "code")}

	_, err := Run(context.Background(), &fakeHolder{}, &fakeReader{screens: []simbridge.Snapshot{before, after}},
		typist, udid, "1234567890")
	if !errors.Is(err, simpaste.ErrNotProven) {
		t.Fatalf("err = %v, want ErrNotProven - never a retry, the part that arrived would go in twice", err)
	}
}

func TestRun_TypedButNothingChangedIsNotDelivered(t *testing.T) {
	same := screen(textField("TextField", "email", "", ""), nil)
	typist := &fakeTypist{focus: focused("TextField", "email"), answer: typed("TextField", "email")}

	_, err := Run(context.Background(), &fakeHolder{}, &fakeReader{screens: []simbridge.Snapshot{same, same}},
		typist, udid, "hello")
	if !errors.Is(err, simpaste.ErrNotDelivered) {
		t.Fatalf("err = %v, want ErrNotDelivered", err)
	}
}

func TestRun_TransportFailureAfterSendingIsJudgedOnScreen(t *testing.T) {
	// The request may have reached the runner: the screen decides.
	before := screen(textField("TextField", "email", "", ""), nil)
	after := screen(textField("TextField", "email", "hello", ""), nil)
	typist := &fakeTypist{focus: focused("TextField", "email"), typeErr: errors.New("connection reset")}

	result, err := Run(context.Background(), &fakeHolder{}, &fakeReader{screens: []simbridge.Snapshot{before, after}},
		typist, udid, "hello")
	if err != nil {
		t.Fatalf("Run: %v - the text is on screen", err)
	}
	if !strings.Contains(result.Warning, "connection reset") {
		t.Fatalf("warning = %q, want what went wrong in transit", result.Warning)
	}
}

func TestRun_RefusesMoreThanAChunk(t *testing.T) {
	_, err := Run(context.Background(), &fakeHolder{}, &fakeReader{}, &fakeTypist{}, udid,
		strings.Repeat("x", ChunkRunes+1))
	if err == nil || !strings.Contains(err.Error(), "Chunks") {
		t.Fatalf("err = %v, want a refusal pointing at Chunks", err)
	}
}

func TestHoldFor_AFullChunkFitsTheLeaseServiceCeiling(t *testing.T) {
	if got := HoldFor(strings.Repeat("x", ChunkRunes)); got > time.Minute {
		t.Fatalf("a full chunk asks for a %s hold; the lease service refuses over a minute", got)
	}
}

// --- Chunks ----------------------------------------------------------------

func TestChunks_KeepsEveryCharacterInOrder(t *testing.T) {
	text := strings.Repeat("สวัสดีครับ ทดสอบ test 123 👨‍👩‍👧 ", 20)
	chunks := Chunks(text, 37)
	if strings.Join(chunks, "") != text {
		t.Fatal("the chunks do not add back up to the text")
	}
	for _, c := range chunks {
		if n := len([]rune(c)); n > 37 {
			t.Fatalf("chunk of %d runes over the limit: %q", n, c)
		}
	}
}

func TestChunks_NeverSplitsWhatAPersonSeesAsOneCharacter(t *testing.T) {
	for name, text := range map[string]string{
		"a Thai vowel mark": "รหัส",
		"a tone mark":       "ก่า",
		"a ZWJ family":      "x👨‍👩‍👧",
		"a skin tone":       "x👍🏽",
	} {
		t.Run(name, func(t *testing.T) {
			// Every limit that would cut inside the cluster.
			for limit := 1; limit < len([]rune(text)); limit++ {
				for _, c := range Chunks(text, limit) {
					first := []rune(c)[0]
					if !boundary([]rune(" "+c), 1) {
						t.Fatalf("limit %d: chunk %q starts with %U, which belongs to the character before it", limit, c, first)
					}
				}
			}
		})
	}
}

func TestChunks_PrefersASpace(t *testing.T) {
	chunks := Chunks("hello world again", 13)
	if chunks[0] != "hello world " {
		t.Fatalf("chunks = %q, want the first to end at a space", chunks)
	}
}

func TestChunks_ShortTextIsOneChunk(t *testing.T) {
	if got := Chunks("hi", ChunkRunes); len(got) != 1 || got[0] != "hi" {
		t.Fatalf("chunks = %q", got)
	}
}
