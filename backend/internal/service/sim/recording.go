package sim

import (
	"context"
	"fmt"
	"log/slog"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// GestureIntent is what a caller is about to do. It mirrors the fields the
// gesture route already accepts, because that is exactly the information a
// recorded step needs and inventing a second vocabulary for it would be two
// things to keep in step.
type GestureIntent struct {
	Kind       string
	X, Y       float64
	ToX, ToY   float64
	DurationMS int
	Text       string
	Name       string
}

// ScreenReader is the recorder's view of a device. It is an interface, and
// optional, so the lease service does not depend on a screen in order to
// exist.
type ScreenReader interface {
	AX(ctx context.Context, udid string) (simbridge.Snapshot, error)
}

// pending is a step that has been resolved but not yet earned. It lives in
// memory keyed by hold token: if the daemon restarts, the hold dies with it
// and so should the half-recorded gesture.
type pending struct {
	udid        string
	step        domain.SimRecordingStep
	frontmost   string
	fingerprint map[string]struct{}
}

// screenState is the cheap memory of what the last RECORDED step (the last one
// actually appended, not merely attempted) saw on screen. It exists to answer
// one question - did the screen change since then - without keeping a tree
// around: a bundle id and a set of the labels/ids that were reachable is
// enough to answer "did this selector exist before", and nothing here is ever
// persisted.
type screenState struct {
	frontmost   string
	fingerprint map[string]struct{}
}

// RecordingRefusedReason says why StartRecording could not open a recording.
// They are reported apart because they need different advice, the same reason
// HoldRefusedReason is - no lease, someone else's lease, or a recording
// already open.
type RecordingRefusedReason string

// Recording refusal reasons.
const (
	RecordingRefusedNotLeased     RecordingRefusedReason = "not_leased"
	RecordingRefusedLeasedByOther RecordingRefusedReason = "leased_by_other"
	RecordingRefusedAlreadyOpen   RecordingRefusedReason = "already_open"
)

// RecordingRefusedError is a refusal to open a recording, in the same shape as
// HoldRefusedError and for the same reason: a caller must never have to answer
// "why not?" with a second, racy read.
type RecordingRefusedError struct {
	UDID   string
	Reason RecordingRefusedReason
	Lease  domain.SimLease
}

func (e *RecordingRefusedError) Error() string {
	switch e.Reason {
	case RecordingRefusedLeasedByOther:
		return fmt.Sprintf("simulator %s is leased by @%s, so this session may not record it", e.UDID, e.Lease.SessionID)
	case RecordingRefusedAlreadyOpen:
		return fmt.Sprintf("simulator %s already has a recording open", e.UDID)
	default:
		return fmt.Sprintf("simulator %s is not claimed by this session, so it may not be recorded", e.UDID)
	}
}

// StartRecording opens a capture of the gestures sessionID performs on udid.
// It requires a live lease on the device - a recording without a lease behind
// it could not have produced any gestures to capture - and refuses when
// another recording is already open there.
func (s *Service) StartRecording(ctx context.Context, sessionID domain.SessionID, udid, name string) (domain.SimRecording, error) {
	key, err := s.leaseKey(udid)
	if err != nil {
		return domain.SimRecording{}, err
	}
	now := s.now()
	outcome, err := s.store.StartSimRecording(ctx, domain.SimRecording{
		UDID:      key,
		SessionID: sessionID,
		Name:      name,
		StartedAt: now,
	}, now)
	if err != nil {
		return domain.SimRecording{}, err
	}
	if !outcome.Granted {
		return domain.SimRecording{}, &RecordingRefusedError{
			UDID:   key,
			Reason: recordingRefusedReason(outcome, sessionID),
			Lease:  outcome.Lease,
		}
	}
	// A fresh recording starts with no "previous step" of its own - carrying
	// over the last one from whatever was recorded on this device before would
	// misreport the very first step as a screen change (or not) based on a
	// capture that has nothing to do with it.
	s.recMu.Lock()
	delete(s.screens, key)
	s.recMu.Unlock()
	return outcome.Recording, nil
}

// StopRecording closes the caller's open recording and returns everything it
// captured, oldest first.
func (s *Service) StopRecording(ctx context.Context, sessionID domain.SessionID, udid string) (domain.SimRecording, []domain.SimRecordingStep, error) {
	key, err := s.leaseKey(udid)
	if err != nil {
		return domain.SimRecording{}, nil, err
	}
	stopped, err := s.store.StopSimRecording(ctx, key, sessionID, s.now())
	if err != nil {
		return domain.SimRecording{}, nil, err
	}
	if !stopped {
		return domain.SimRecording{}, nil, fmt.Errorf("%w: no open recording for session %s on simulator %s", ErrNotFound, sessionID, key)
	}
	s.recMu.Lock()
	delete(s.screens, key)
	s.recMu.Unlock()

	rec, ok, err := s.store.GetSimRecording(ctx, key)
	if err != nil {
		return domain.SimRecording{}, nil, err
	}
	if !ok {
		return domain.SimRecording{}, nil, fmt.Errorf("%w: recording on simulator %s vanished after stopping", ErrNotFound, key)
	}
	steps, err := s.store.ListSimRecordingSteps(ctx, key)
	if err != nil {
		return domain.SimRecording{}, nil, err
	}
	return rec, steps, nil
}

// GetRecording returns a device's recording, open or stopped, and the steps it
// has captured so far. ok=false means no recording has ever been started on
// this device.
func (s *Service) GetRecording(ctx context.Context, udid string) (domain.SimRecording, []domain.SimRecordingStep, bool, error) {
	key, err := s.leaseKey(udid)
	if err != nil {
		return domain.SimRecording{}, nil, false, err
	}
	rec, ok, err := s.store.GetSimRecording(ctx, key)
	if err != nil {
		return domain.SimRecording{}, nil, false, err
	}
	if !ok {
		return domain.SimRecording{}, nil, false, nil
	}
	steps, err := s.store.ListSimRecordingSteps(ctx, key)
	if err != nil {
		return domain.SimRecording{}, nil, false, err
	}
	return rec, steps, true, nil
}

// recordIntent is what AcquireHold calls, once the hold itself is granted, to
// resolve intent into a step and stash it under token.
//
// The recording row is read first, and that read - a single indexed lookup -
// is the ONLY cost paid when nothing is being recorded. The screen is read
// only once a recording is confirmed open; the overwhelming majority of
// gestures never reach that call.
func (s *Service) recordIntent(ctx context.Context, udid, token string, intent GestureIntent) {
	rec, ok, err := s.store.GetSimRecording(ctx, udid)
	if err != nil {
		slog.WarnContext(ctx, "sim recorder: could not check for an open recording; skipping this step",
			"udid", udid, "error", err)
		return
	}
	if !ok || rec.StoppedAt != nil {
		return
	}

	// A screen read that fails must never fail the hold: the gesture already
	// got its finger, and refusing it because the recorder could not read the
	// screen would be damage, done exactly when a device is misbehaving and
	// the human most needs to drive it. Losing one recorded step is a
	// nuisance, not that.
	snap, err := s.recorder.AX(ctx, udid)
	if err != nil {
		slog.WarnContext(ctx, "sim recorder: could not read the screen; skipping this step",
			"udid", udid, "error", err)
		return
	}

	el, found := hitTest(snap.Elements, intent.X, intent.Y)
	choice := simflow.Choice{Rung: simflow.RungNone}
	if found {
		choice = simflow.For(snap, el)
	}

	s.recMu.Lock()
	prev, hasPrev := s.screens[udid]
	s.recMu.Unlock()

	screenChange := false
	if hasPrev {
		switch {
		case snap.Frontmost.BundleID != prev.frontmost:
			screenChange = true
		default:
			if key, ok := fingerprintKeyFor(el); found && ok {
				if _, existed := prev.fingerprint[key]; !existed {
					screenChange = true
				}
			}
		}
	}

	step := domain.SimRecordingStep{
		At:           s.now(),
		Kind:         intent.Kind,
		SelectorRung: int64(choice.Rung),
		Ambiguity:    int64(choice.Ambiguity),
		OffScreen:    choice.OffScreen,
		ScreenChange: screenChange,
		X:            intent.X,
		Y:            intent.Y,
		ToX:          intent.ToX,
		ToY:          intent.ToY,
		DurationMS:   int64(intent.DurationMS),
		Text:         intent.Text,
		Detail:       intent.Name,
	}
	switch choice.Rung {
	case simflow.RungText, simflow.RungTextIndex:
		step.Selector = choice.Text
	case simflow.RungID:
		step.Selector = choice.ID
	}

	s.recMu.Lock()
	s.pending[token] = pending{
		udid:        udid,
		step:        step,
		frontmost:   snap.Frontmost.BundleID,
		fingerprint: fingerprintOf(snap),
	}
	s.recMu.Unlock()
}

// finishRecording is what ReleaseHold calls once the hold itself has been
// given back. It always drops the stash for token; it appends the step to the
// recording only when earn is true (the hold both belonged to this caller and
// the gesture it covered was actually performed).
func (s *Service) finishRecording(ctx context.Context, token string, earn bool) {
	s.recMu.Lock()
	p, ok := s.pending[token]
	delete(s.pending, token)
	s.recMu.Unlock()
	if !ok || !earn {
		return
	}
	_, appended, err := s.store.AppendSimRecordingStep(ctx, p.udid, p.step)
	if err != nil {
		slog.WarnContext(ctx, "sim recorder: could not append the recorded step",
			"udid", p.udid, "error", err)
		return
	}
	if !appended {
		// The recording closed between this gesture's acquire and release;
		// there is nothing left to record it into.
		return
	}
	s.recMu.Lock()
	s.screens[p.udid] = screenState{frontmost: p.frontmost, fingerprint: p.fingerprint}
	s.recMu.Unlock()
}

// recordingRefusedReason picks the refusal a caller can act on, the same way
// holdRefusedReason does for a hold.
func recordingRefusedReason(outcome domain.SimRecordingOutcome, caller domain.SessionID) RecordingRefusedReason {
	switch {
	case outcome.Busy:
		return RecordingRefusedAlreadyOpen
	case outcome.Leased && outcome.Lease.SessionID != caller:
		return RecordingRefusedLeasedByOther
	default:
		return RecordingRefusedNotLeased
	}
}

// hitTest finds the element at a normalized 0..1 point, preferring the
// deepest (most specific) match: a label sitting on a whole row and on the
// text inside it are the same real control, and the child is what a tap
// actually lands on. Later siblings are tried first because on a real screen
// later usually means drawn on top.
func hitTest(elements []simbridge.Element, x, y float64) (simbridge.Element, bool) {
	for i := len(elements) - 1; i >= 0; i-- {
		el := elements[i]
		if child, ok := hitTest(el.Children, x, y); ok {
			return child, true
		}
		if boxContains(el.Box, x, y) {
			return el, true
		}
	}
	return simbridge.Element{}, false
}

func boxContains(box *simbridge.Box, x, y float64) bool {
	if box == nil {
		return false
	}
	return x >= box.X1 && x <= box.X2 && y >= box.Y1 && y <= box.Y2
}

// fingerprintKeyFor is the identity recordIntent checks a resolved element
// against the previous step's fingerprint by - the same precedence
// simflow.For itself uses (a label first, then an id), because those are the
// two names a selector can be built from. A point-only match (no label, no
// id) has no stable identity to compare, so it reports false: only the
// frontmost bundle id can say anything about a screen change for it.
func fingerprintKeyFor(el simbridge.Element) (string, bool) {
	if label := strings.TrimSpace(el.Label); label != "" {
		return "label:" + label, true
	}
	if id := strings.TrimSpace(el.ID); id != "" {
		return "id:" + id, true
	}
	return "", false
}

// fingerprintOf is every label/id reachable in a snapshot, as a set. It is
// deliberately not the tree itself: this is all recordIntent ever needs to
// answer "did this selector exist before", and it is thrown away as soon as
// the next comparison is made.
func fingerprintOf(snap simbridge.Snapshot) map[string]struct{} {
	set := map[string]struct{}{}
	var walk func([]simbridge.Element)
	walk = func(els []simbridge.Element) {
		for _, el := range els {
			if key, ok := fingerprintKeyFor(el); ok {
				set[key] = struct{}{}
			}
			walk(el.Children)
		}
	}
	walk(snap.Elements)
	return set
}
