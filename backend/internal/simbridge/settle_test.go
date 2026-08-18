package simbridge_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
)

// reads turns a script of snapshots into a read function, and records how many
// times it was called - which is the cost settling is allowed to have.
func reads(snaps ...simbridge.Snapshot) (func(context.Context) (simbridge.Snapshot, error), *int) {
	n := 0
	return func(context.Context) (simbridge.Snapshot, error) {
		snap := snaps[min(n, len(snaps)-1)]
		n++
		return snap, nil
	}, &n
}

func screen(labels ...string) simbridge.Snapshot {
	els := make([]simbridge.Element, 0, len(labels))
	for i, label := range labels {
		els = append(els, simbridge.Element{Path: "0." + string(rune('0'+i)), Label: label})
	}
	return simbridge.Snapshot{Elements: els}
}

// The cheap case, and the one that must stay cheap: a screen that is already
// still costs exactly two reads and no more.
func TestReadSettled_StableScreenCostsTwoReads(t *testing.T) {
	still := screen("Home", "Port")
	read, calls := reads(still, still)

	snap, res, err := simbridge.ReadSettled(t.Context(), read, simbridge.SettleOptions{Delay: time.Millisecond})

	if err != nil {
		t.Fatalf("ReadSettled: %v", err)
	}
	if !res.Settled {
		t.Error("two identical reads mean the screen is settled")
	}
	if *calls != 2 || res.Reads != 2 {
		t.Errorf("reads = %d (result says %d), want 2", *calls, res.Reads)
	}
	if len(snap.Elements) != 2 {
		t.Errorf("returned the wrong snapshot: %+v", snap)
	}
}

// The case this whole feature exists for: content that arrives late. The first
// read sees a skeleton, the second sees the loaded screen, and the caller must
// get the loaded one.
func TestReadSettled_ReturnsTheScreenThatArrivedLate(t *testing.T) {
	loaded := screen("Email", "Password", "Sign in")
	read, calls := reads(screen("Loading"), loaded, loaded)

	snap, res, err := simbridge.ReadSettled(t.Context(), read, simbridge.SettleOptions{Delay: time.Millisecond})

	if err != nil {
		t.Fatalf("ReadSettled: %v", err)
	}
	if !res.Settled {
		t.Fatalf("should have settled on the third read, got %+v", res)
	}
	if *calls != 3 {
		t.Errorf("reads = %d, want 3", *calls)
	}
	if len(snap.Elements) != 3 {
		t.Errorf("returned the skeleton instead of the loaded screen: %+v", snap)
	}
}

// A screen that never stops moving must bound out and SAY it did not settle,
// rather than hang. That is the difference between a caller that can warn a
// human and one that quietly reports a snapshot of something mid-flight.
func TestReadSettled_NeverSettlingBoundsOutAndSaysSo(t *testing.T) {
	n := 0
	read := func(context.Context) (simbridge.Snapshot, error) {
		n++
		return screen("Frame " + string(rune('0'+n))), nil
	}

	_, res, err := simbridge.ReadSettled(t.Context(), read, simbridge.SettleOptions{MaxReads: 4, Delay: time.Millisecond})

	if err != nil {
		t.Fatalf("a moving screen is not an error, it is an unsettled read: %v", err)
	}
	if res.Settled {
		t.Error("nothing ever repeated; this must not claim to have settled")
	}
	if res.Reads != 4 || n != 4 {
		t.Errorf("reads = %d (result says %d), want exactly the budget of 4", n, res.Reads)
	}
}

// Settling is about which snapshot to trust. When the device will not answer
// there is nothing to choose between, so the error comes straight back.
func TestReadSettled_ReadErrorIsReturnedImmediately(t *testing.T) {
	want := errors.New("device is not answering")
	n := 0
	read := func(context.Context) (simbridge.Snapshot, error) {
		n++
		return simbridge.Snapshot{}, want
	}

	_, res, err := simbridge.ReadSettled(t.Context(), read, simbridge.SettleOptions{Delay: time.Millisecond})

	if !errors.Is(err, want) {
		t.Fatalf("err = %v, want %v", err, want)
	}
	if n != 1 {
		t.Errorf("retried a failing read %d times; it should give up at once", n)
	}
	if res.Reads != 0 {
		t.Errorf("a failed read is not a read that happened: %+v", res)
	}
}

// A cancelled context must stop the wait, not sleep through it.
func TestReadSettled_HonoursCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	read := func(context.Context) (simbridge.Snapshot, error) {
		cancel()
		return screen("moving " + time.Now().String()), nil
	}

	_, _, err := simbridge.ReadSettled(ctx, read, simbridge.SettleOptions{Delay: time.Hour})

	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
}

// A budget below the floor is meaningless - one read cannot show stability -
// so it is raised to the default rather than silently returning "unsettled"
// after a single read.
func TestReadSettled_BudgetBelowTwoUsesTheDefault(t *testing.T) {
	still := screen("Home")
	read, calls := reads(still)

	_, res, err := simbridge.ReadSettled(t.Context(), read, simbridge.SettleOptions{MaxReads: 1, Delay: time.Millisecond})

	if err != nil {
		t.Fatalf("ReadSettled: %v", err)
	}
	if !res.Settled || *calls != 2 {
		t.Errorf("calls = %d settled = %v, want 2 reads and a settled screen", *calls, res.Settled)
	}
}

// The fingerprint has to notice the things a selector is built from, and
// ignore the sub-pixel drift of a view easing into place - otherwise a screen
// that is visually still would never settle.
func TestFingerprint_NoticesContentAndIgnoresSubPixelDrift(t *testing.T) {
	base := simbridge.Snapshot{Elements: []simbridge.Element{
		{Path: "0.0", Label: "Buy", Frame: simbridge.Rect{X: 10, Y: 20, Width: 100, Height: 40}},
	}}
	drifted := simbridge.Snapshot{Elements: []simbridge.Element{
		{Path: "0.0", Label: "Buy", Frame: simbridge.Rect{X: 10.2, Y: 19.8, Width: 100, Height: 40}},
	}}
	renamed := simbridge.Snapshot{Elements: []simbridge.Element{
		{Path: "0.0", Label: "Sell", Frame: simbridge.Rect{X: 10, Y: 20, Width: 100, Height: 40}},
	}}
	moved := simbridge.Snapshot{Elements: []simbridge.Element{
		{Path: "0.0", Label: "Buy", Frame: simbridge.Rect{X: 10, Y: 200, Width: 100, Height: 40}},
	}}

	if simbridge.Fingerprint(base) != simbridge.Fingerprint(drifted) {
		t.Error("sub-pixel drift must not count as movement, or nothing ever settles")
	}
	if simbridge.Fingerprint(base) == simbridge.Fingerprint(renamed) {
		t.Error("a changed label is a changed screen")
	}
	if simbridge.Fingerprint(base) == simbridge.Fingerprint(moved) {
		t.Error("an element that moved 180 points is a changed screen")
	}
}

// Field boundaries must be separated, or ("ab","c") and ("a","bc") hash alike
// and two different screens look settled.
func TestFingerprint_FieldsCannotRunTogether(t *testing.T) {
	a := simbridge.Snapshot{Elements: []simbridge.Element{{Path: "0.0", Label: "ab", Value: "c"}}}
	b := simbridge.Snapshot{Elements: []simbridge.Element{{Path: "0.0", Label: "a", Value: "bc"}}}

	if simbridge.Fingerprint(a) == simbridge.Fingerprint(b) {
		t.Error("adjacent fields ran together")
	}
}

// The frontmost app is part of the screen's identity: the same tree under a
// different app is a different screen.
func TestFingerprint_FrontmostAppIsPartOfTheIdentity(t *testing.T) {
	tree := []simbridge.Element{{Path: "0.0", Label: "OK"}}
	a := simbridge.Snapshot{Frontmost: simbridge.Frontmost{BundleID: "one"}, Elements: tree}
	b := simbridge.Snapshot{Frontmost: simbridge.Frontmost{BundleID: "two"}, Elements: tree}

	if simbridge.Fingerprint(a) == simbridge.Fingerprint(b) {
		t.Error("two different apps must not fingerprint alike")
	}
}
