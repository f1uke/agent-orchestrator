// Package simctl is the one place that discovers local iOS Simulators and
// decides which one an unqualified request means.
//
// It exists because two callers now need that answer and they must never give
// different ones: the `ao sim` CLI, and the daemon route behind the desktop
// app's Simulator tab. The rule that matters is the refusal - with two
// simulators booted there is no default, and neither surface may invent one.
// Keeping the rule here rather than in either caller is what makes "the UI is
// at least as honest as the CLI" a property of the code instead of a promise.
//
// Everything here is read-only against the machine: it runs
// `xcrun simctl list devices --json` and nothing else. Nothing in this package
// can boot, shut down, reboot or erase a device.
package simctl

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
)

// Binary and BootedState are simctl's own vocabulary.
const (
	Binary      = "xcrun"
	BootedState = "Booted"

	runtimePrefix = "com.apple.CoreSimulator.SimRuntime."
)

// Sentinel outcomes of Resolve. They are values rather than formatted strings
// because two very different surfaces phrase them: a CLI error a human reads in
// a terminal, and a device picker in the desktop app.
var (
	// ErrUnavailable: this machine cannot answer the question at all (no xcrun,
	// so almost certainly not a mac with Xcode).
	ErrUnavailable = errors.New("simctl: unavailable")
	// ErrNoDevices: the machine has no simulators.
	ErrNoDevices = errors.New("simctl: no simulators")
	// ErrNoBooted: simulators exist but none is booted. AO never boots one.
	ErrNoBooted = errors.New("simctl: no booted simulator")
	// ErrUnknownUDID: the requested udid is not on this machine.
	ErrUnknownUDID = errors.New("simctl: unknown udid")
)

// Device is one simulator as simctl reports it, plus the runtime it was listed
// under.
type Device struct {
	UDID              string `json:"udid"`
	Name              string `json:"name"`
	Runtime           string `json:"runtime"`
	RuntimeIdentifier string `json:"runtimeIdentifier"`
	State             string `json:"state"`
	Available         bool   `json:"available"`
	// Default marks the device an unqualified request would resolve to. It is
	// set by Summarize, and is false whenever there is no unambiguous answer.
	Default bool `json:"default"`
}

// Booted reports whether the device can be captured or driven at all.
func (d Device) Booted() bool { return d.State == BootedState }

// Label is how a device is named back to a person: enough to recognise it, and
// the udid needed to address it.
func (d Device) Label() string {
	return fmt.Sprintf("%s (%s, %s)", d.Name, d.Runtime, d.UDID)
}

// AmbiguousError is the refusal that matters most: several simulators are
// booted, so an unqualified request has no answer. It carries the candidates so
// a caller can list them - a CLI as copy-pasteable commands, the desktop app as
// a picker - without asking again.
type AmbiguousError struct{ Booted []Device }

func (e *AmbiguousError) Error() string {
	return fmt.Sprintf("%d simulators are booted, so there is no unambiguous default", len(e.Booted))
}

// NotBootedError is a device that exists but is shut down. It carries the
// device so the refusal can report the real state rather than a generic miss.
type NotBootedError struct{ Device Device }

func (e *NotBootedError) Error() string {
	return fmt.Sprintf("simulator %s is not booted (state: %s)", e.Device.Label(), e.Device.State)
}

// Runner executes a command and returns its combined output. Injectable so
// every caller above can be tested without Xcode, a mac or a device.
type Runner func(ctx context.Context, name string, args ...string) ([]byte, error)

// LookPath resolves a binary, matching os/exec.LookPath.
type LookPath func(file string) (string, error)

// listing mirrors the part of `simctl list devices --json` we read.
type listing struct {
	Devices map[string][]struct {
		UDID        string `json:"udid"`
		Name        string `json:"name"`
		State       string `json:"state"`
		IsAvailable bool   `json:"isAvailable"`
	} `json:"devices"`
}

// List asks simctl what this machine has. It is the only path into simctl, so
// there is a single place that knows how devices are discovered.
func List(ctx context.Context, lookPath LookPath, run Runner) ([]Device, error) {
	if _, err := lookPath(Binary); err != nil {
		return nil, fmt.Errorf("%w: %s not found on PATH", ErrUnavailable, Binary)
	}
	out, err := run(ctx, Binary, "simctl", "list", "devices", "--json")
	if err != nil {
		return nil, fmt.Errorf("`simctl list devices` failed: %w: %s", err, Output(out))
	}
	var parsed listing
	if err := json.Unmarshal(out, &parsed); err != nil {
		return nil, fmt.Errorf("could not parse `simctl list devices --json` output: %w: %s", err, Output(out))
	}

	// simctl keys devices by runtime, which decodes into a Go map - iteration
	// order would otherwise be random. Sort the runtimes and keep simctl's own
	// order within each so the listing is stable and matches `simctl list`.
	runtimes := make([]string, 0, len(parsed.Devices))
	for runtime := range parsed.Devices {
		runtimes = append(runtimes, runtime)
	}
	sort.Strings(runtimes)

	devices := []Device{}
	for _, runtime := range runtimes {
		for _, d := range parsed.Devices[runtime] {
			devices = append(devices, Device{
				UDID:              d.UDID,
				Name:              d.Name,
				Runtime:           RuntimeLabel(runtime),
				RuntimeIdentifier: runtime,
				State:             d.State,
				Available:         d.IsAvailable,
			})
		}
	}
	return devices, nil
}

// Resolve is the single device-resolution rule. It never picks between several
// booted simulators and never asks for one to be booted.
func Resolve(devices []Device, udid string) (Device, error) {
	if want := strings.TrimSpace(udid); want != "" {
		for _, d := range devices {
			if strings.EqualFold(d.UDID, want) {
				if !d.Booted() {
					return Device{}, &NotBootedError{Device: d}
				}
				return d, nil
			}
		}
		return Device{}, fmt.Errorf("%w: %q", ErrUnknownUDID, want)
	}

	booted := Booted(devices)
	switch len(booted) {
	case 1:
		return booted[0], nil
	case 0:
		if len(devices) == 0 {
			return Device{}, ErrNoDevices
		}
		return Device{}, fmt.Errorf("%w: %d exist, all shut down", ErrNoBooted, len(devices))
	default:
		return Device{}, &AmbiguousError{Booted: booted}
	}
}

// Booted filters a listing down to the devices that can actually be used.
func Booted(devices []Device) []Device {
	out := []Device{}
	for _, d := range devices {
		if d.Booted() {
			out = append(out, d)
		}
	}
	return out
}

// Listing is a device list plus the default an unqualified request resolves to,
// and - when there is none - the reason there is none. Both surfaces show the
// same thing: the desktop picker preselects DefaultUDID and shows nothing
// preselected when it is nil.
type Listing struct {
	Devices       []Device `json:"devices"`
	DefaultUDID   *string  `json:"defaultUdid"`
	DefaultReason string   `json:"defaultReason"`
}

// Summarize annotates a listing with the default and why.
func Summarize(devices []Device) Listing {
	result := Listing{Devices: append([]Device{}, devices...)}
	chosen, err := Resolve(result.Devices, "")
	if err != nil {
		result.DefaultReason = defaultReason(err)
		return result
	}
	for i := range result.Devices {
		if result.Devices[i].UDID == chosen.UDID {
			result.Devices[i].Default = true
		}
	}
	udid := chosen.UDID
	result.DefaultUDID = &udid
	result.DefaultReason = "the only booted simulator"
	return result
}

// defaultReason phrases "there is no default" for a listing. It stays short
// because both surfaces show it inline next to the picker or the table.
func defaultReason(err error) string {
	var ambiguous *AmbiguousError
	switch {
	case errors.As(err, &ambiguous):
		return fmt.Sprintf("%d simulators are booted, so there is no unambiguous default", len(ambiguous.Booted))
	case errors.Is(err, ErrNoDevices):
		return "no simulators found on this machine"
	case errors.Is(err, ErrNoBooted):
		return "no simulator is booted"
	default:
		return err.Error()
	}
}

// RuntimeLabel turns a runtime identifier into the label simctl itself prints
// as a section header: com.apple.CoreSimulator.SimRuntime.iOS-26-3 becomes
// "iOS 26.3". Anything unexpected is passed through untouched, and the raw
// identifier is always kept alongside it.
func RuntimeLabel(identifier string) string {
	short := strings.TrimPrefix(identifier, runtimePrefix)
	if short == identifier {
		return identifier
	}
	parts := strings.SplitN(short, "-", 2)
	if len(parts) != 2 {
		return short
	}
	return parts[0] + " " + strings.ReplaceAll(parts[1], "-", ".")
}

// Output trims simctl's own diagnostics for embedding in an error. An empty
// result is reported as such: a silent failure is worse than a noisy one.
func Output(out []byte) string {
	trimmed := strings.TrimSpace(string(out))
	if trimmed == "" {
		return "(no output)"
	}
	const limit = 400
	if len(trimmed) > limit {
		return trimmed[:limit] + "…"
	}
	return trimmed
}
