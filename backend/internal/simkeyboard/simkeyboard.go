// Package simkeyboard answers the one question `ao sim type` cannot answer for
// itself: will the key presses it is about to send arrive as the characters it
// was asked for?
//
// They often will not. The simulator's HID path speaks USB keyboard usages, and
// it is the GUEST that turns a usage into a character, using whichever input
// mode it currently has selected. Simulator.app ships with I/O > Keyboard >
// "Use the Same Keyboard Language as macOS" ticked, so a developer whose Mac is
// set to Thai has a guest set to Thai, and `type "fa12345"` lands as
// "ดฟๅ/_ภถ" - the Kedmanee mapping of those exact keys.
//
// That failure is nasty in a specific way. It is SELECTIVE: fields that force
// an ASCII keyboard (.emailAddress, .URL) make iOS switch input mode by itself
// and come out right, while ordinary fields and secure fields do not. And in a
// secure field - passwords, which is where a person meets this most often - the
// characters are hidden behind dots, so the corruption cannot be seen at the
// point it happens and cannot be read back afterwards either. Text that looks
// like bad test data is not obviously a broken tool.
//
// So the mapping is established BEFORE anything is sent, and a mapping we
// cannot promise is refused rather than typed hopefully. The device knows the
// answer and will tell us: it records the mode in use in its own preferences.
package simkeyboard

import (
	"context"
	"errors"
	"fmt"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/simctl"
)

// ErrUnknown means the guest's input mode could not be established. It is
// deliberately NOT a synonym for "US": the whole point of this package is that
// an unverified mapping is never assumed to be the safe one.
var ErrUnknown = errors.New("simkeyboard: the simulator's keyboard input mode could not be read")

const (
	// domain and currentKey are where the guest records the input mode in use.
	// The value is a list whose first entry is the current mode and whose rest
	// is what a mode-switch would land on next.
	domain     = "com.apple.keyboard.preferences"
	currentKey = "KeyboardsCurrentAndNext"

	// usLayout is the one hardware layout whose mapping matches the usages
	// internal/simbridge sends.
	usLayout = "US"
	// automaticLayout means the guest picks the hardware layout from the mode's
	// language rather than being told one.
	automaticLayout = "Automatic"
	// usLanguage is the only language whose automatic hardware layout is US.
	usLanguage = "en_US"
)

// Mode is the guest's active keyboard input mode.
//
// Its identifier reads `<language>@sw=<software layout>;hw=<hardware layout>`,
// for example `th_TH@sw=Thai;hw=Automatic`. Only the hardware half decides what
// a key press becomes: the software half is what an on-screen keyboard looks
// like, and an on-screen keyboard is not what we are driving.
type Mode struct {
	Identifier string
	Language   string
	Hardware   string
}

// ParseMode reads an input-mode identifier. Anything it cannot make sense of
// comes back as a mode that promises nothing, which is the honest reading of an
// identifier we do not recognise.
func ParseMode(identifier string) Mode {
	mode := Mode{Identifier: strings.TrimSpace(identifier)}
	language, rest, found := strings.Cut(mode.Identifier, "@")
	if !found {
		return mode
	}
	mode.Language = language
	for _, field := range strings.Split(rest, ";") {
		if key, value, ok := strings.Cut(field, "="); ok && key == "hw" {
			mode.Hardware = value
		}
	}
	return mode
}

// SendsUSASCII reports whether this mode turns the US keyboard usages we send
// into the characters they stand for.
//
// It answers yes for exactly two shapes and guesses at nothing else. A layout
// that is merely CLOSE to US is still a no: on a UK layout the letters and
// digits do land correctly, but shift-2 is `"` rather than `@`, which quietly
// corrupts every email address - the same class of bug in a smaller costume.
func (m Mode) SendsUSASCII() bool {
	switch m.Hardware {
	case usLayout:
		// The guest was told a US hardware layout outright. This is the case
		// that makes a Thai on-screen keyboard harmless.
		return true
	case automaticLayout:
		// The guest derives the hardware layout from the language.
		return m.Language == usLanguage
	default:
		return false
	}
}

// Describe names the mode for a person reading a refusal.
func (m Mode) Describe() string {
	if m.Identifier == "" {
		return "unknown"
	}
	if m.Language == "" {
		return m.Identifier
	}
	return fmt.Sprintf("%s (%s)", m.Language, m.Identifier)
}

// Probe asks a booted simulator which input mode it is using.
//
// It is read-only against the device: it spawns `defaults read` in the guest
// and nothing else. The read has to go through the guest rather than through
// the device's preferences file on the host, because the running guest's
// cfprefsd holds these keys in memory - the file on disk does not contain them
// at all, so a host-side shortcut would answer "unknown" for every running
// device. That costs about a second, which is the price of not lying.
func Probe(ctx context.Context, run simctl.Runner, udid string) (Mode, error) {
	out, err := run(ctx, simctl.Binary, "simctl", "spawn", udid, "defaults", "read", domain, currentKey)
	identifier, ok := firstEntry(out)
	if ok && err == nil {
		return ParseMode(identifier), nil
	}
	if err != nil {
		return Mode{}, fmt.Errorf("%w: %w: %s", ErrUnknown, err, simctl.Output(out))
	}
	return Mode{}, fmt.Errorf("%w: `defaults read %s %s` answered %s", ErrUnknown, domain, currentKey, simctl.Output(out))
}

// firstEntry pulls the current mode out of what `defaults read` printed. The
// value is normally an old-style plist array:
//
//	(
//	    "th_TH@sw=Thai;hw=Automatic",
//	    "en_US@sw=QWERTY;hw=Automatic"
//	)
//
// but a bare scalar is read just as well, because nothing promises the shape
// and reading a one-line answer as "unknown" would refuse a device that was
// perfectly willing to say.
func firstEntry(out []byte) (string, bool) {
	for _, line := range strings.Split(string(out), "\n") {
		entry := strings.TrimSpace(line)
		entry = strings.TrimSuffix(entry, ",")
		entry = strings.Trim(entry, `"`)
		if entry == "" || entry == "(" || entry == ")" {
			continue
		}
		return entry, true
	}
	return "", false
}
