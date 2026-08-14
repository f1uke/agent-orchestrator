package simkeyboard

import (
	"context"
	"errors"
	"strings"
	"testing"
)

// The identifiers in these tests are the real thing: they were read off a
// booted device with `defaults read com.apple.keyboard.preferences`.

func TestParseMode_SplitsTheLanguageFromTheHardwareLayout(t *testing.T) {
	mode := ParseMode(`th_TH@sw=Thai;hw=Automatic`)
	if mode.Language != "th_TH" {
		t.Fatalf("language = %q, want th_TH", mode.Language)
	}
	if mode.Hardware != "Automatic" {
		t.Fatalf("hardware = %q, want Automatic", mode.Hardware)
	}
	if mode.Identifier != "th_TH@sw=Thai;hw=Automatic" {
		t.Fatalf("identifier = %q, want the whole thing kept", mode.Identifier)
	}
}

func TestModeSendsUSASCII(t *testing.T) {
	// The rule is about the HARDWARE layout, which is what turns the usages we
	// send into characters. The software layout - what an on-screen keyboard
	// looks like - has no say in it.
	cases := []struct {
		identifier string
		want       bool
		why        string
	}{
		{"en_US@sw=QWERTY;hw=Automatic", true, "the ordinary US setup, and the only one we promise"},
		{"th_TH@sw=Thai;hw=Automatic", false, "Kedmanee: the bug this exists to catch"},
		{"th_TH@sw=Thai;hw=US", true, "a Thai software keyboard over a US hardware layout still sends US"},
		{"fr_FR@sw=AZERTY;hw=Automatic", false, "AZERTY moves the letters themselves"},
		{"en_GB@sw=QWERTY-UK;hw=Automatic", false, `UK swaps @ and ", so an email address would be wrong`},
		{"emoji@sw=Emoji", false, "no hardware layout at all, so nothing is promised"},
		{"", false, "an unknown mode is never assumed to be safe"},
		{"garbage", false, "an identifier we cannot parse is not a US keyboard"},
	}
	for _, c := range cases {
		if got := ParseMode(c.identifier).SendsUSASCII(); got != c.want {
			t.Errorf("ParseMode(%q).SendsUSASCII() = %v, want %v - %s", c.identifier, got, c.want, c.why)
		}
	}
}

// probeOutput is what `defaults read <domain> KeyboardsCurrentAndNext` prints:
// an old-style plist array whose FIRST entry is the mode in use.
const probeOutput = `(
    "th_TH@sw=Thai;hw=Automatic",
    "th_TH@sw=Thai;hw=Automatic",
    "en_US@sw=QWERTY;hw=Automatic"
)
`

func TestProbe_ReadsTheCurrentModeAndNotTheNextOne(t *testing.T) {
	var got []string
	run := func(_ context.Context, name string, args ...string) ([]byte, error) {
		got = append([]string{name}, args...)
		return []byte(probeOutput), nil
	}

	mode, err := Probe(context.Background(), run, "UDID-1")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if mode.Identifier != "th_TH@sw=Thai;hw=Automatic" {
		t.Fatalf("mode = %q, want the first entry", mode.Identifier)
	}
	// The probe must stay read-only against the device: it spawns `defaults
	// read` and nothing else.
	want := []string{"xcrun", "simctl", "spawn", "UDID-1", "defaults", "read", domain, currentKey}
	if strings.Join(got, " ") != strings.Join(want, " ") {
		t.Fatalf("ran %v, want %v", got, want)
	}
}

func TestProbe_ReadsAScalarToo(t *testing.T) {
	// Nothing documents that this key is always an array, and a one-line answer
	// must not be read as "unknown".
	run := func(context.Context, string, ...string) ([]byte, error) {
		return []byte("en_US@sw=QWERTY;hw=Automatic\n"), nil
	}
	mode, err := Probe(context.Background(), run, "UDID-1")
	if err != nil {
		t.Fatalf("Probe: %v", err)
	}
	if !mode.SendsUSASCII() {
		t.Fatalf("mode = %q, want the scalar read as a US keyboard", mode.Identifier)
	}
}

func TestProbe_ReportsUnknownRatherThanGuessing(t *testing.T) {
	cases := map[string]struct {
		out []byte
		err error
	}{
		"the device has never shown a keyboard": {
			out: []byte("The domain/default pair of (com.apple.keyboard.preferences, KeyboardsCurrentAndNext) does not exist"),
			err: errors.New("exit status 1"),
		},
		"an empty array":  {out: []byte("(\n)\n")},
		"nothing at all":  {out: []byte("   \n")},
		"spawn refused":   {err: errors.New("device is not booted")},
		"empty and error": {},
	}
	for name, c := range cases {
		t.Run(name, func(t *testing.T) {
			run := func(context.Context, string, ...string) ([]byte, error) { return c.out, c.err }
			mode, err := Probe(context.Background(), run, "UDID-1")
			if !errors.Is(err, ErrUnknown) {
				t.Fatalf("err = %v (mode %q), want ErrUnknown", err, mode.Identifier)
			}
			if mode.SendsUSASCII() {
				t.Fatal("a mode that could not be read must never come back safe")
			}
		})
	}
}

func TestProbeError_QuotesWhatTheDeviceSaid(t *testing.T) {
	// A refusal a person cannot act on is barely better than the silent
	// corruption it replaced, so the device's own words are carried out.
	run := func(context.Context, string, ...string) ([]byte, error) {
		return []byte("Process spawn via launchd failed"), errors.New("exit status 1")
	}
	_, err := Probe(context.Background(), run, "UDID-1")
	if err == nil || !strings.Contains(err.Error(), "launchd") {
		t.Fatalf("err = %v, want the device's own output quoted", err)
	}
}
