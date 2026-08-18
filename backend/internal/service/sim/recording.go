package sim

import (
	"context"
	"errors"
	"fmt"
	"log/slog"
	"strings"
	"time"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/simbridge"
	"github.com/aoagents/agent-orchestrator/backend/internal/simflow"
)

// GestureIntent is what a caller is about to do. It mirrors the fields the
// gesture route already accepts, because that is exactly the information a
// recorded step needs and inventing a second vocabulary for it would be two
// things to keep in step.
//
// Label and ID are set only by a tap that named its target rather than
// pointing at one (`ao sim tap --label`/`--id`) - every other caller leaves
// both empty. recordIntent prefers them over X/Y when present: a by-name tap
// already knows exactly what it targeted, and hit-testing a coordinate to
// rediscover that would both throw the caller's own answer away and pay for
// an accessibility read to recover a worse one.
type GestureIntent struct {
	Kind       string
	X, Y       float64
	ToX, ToY   float64
	DurationMS int
	Text       string
	Name       string
	Label      string
	ID         string
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
//
// expiresAt is the owning hold's own ExpiresAt, copied in verbatim rather than
// picked as a separate TTL: a stash entry must not outlive the hold it
// belongs to, and the hold's own expiry is the one number that is already
// correct by construction. A client that dies mid-gesture never calls
// ReleaseHold, so nothing would otherwise ever remove this entry; recordIntent
// sweeps expired entries on every write so the map cannot grow past one entry
// per device actually being driven right now, with no background goroutine.
type pending struct {
	udid        string
	step        domain.SimRecordingStep
	frontmost   string
	fingerprint map[string]struct{}
	expiresAt   time.Time
}

// seenScreen is the screen the recorder last read, kept so a gesture does not
// have to wait for one.
//
// 🗝 This is the whole fix for the recorder making the tab slow. Reading the
// accessibility tree costs ~0.5 s on an idle device and ~1.5 s on a real app,
// and the bridge SERIALIZES: a read and a touch cannot overlap, so a read
// taken on the gesture path is time the finger spends in the air. Measured, a
// tap went from 45 ms to 616 ms with a recording open, and a drag lost every
// one of its intermediate moves.
//
// So the recorder stops fetching the screen and starts maintaining it. The
// read happens AFTER a gesture, when the human is looking at what they just
// did and nobody is waiting; the screen it returns is the "before" screen of
// whatever they do next.
//
// ⚠ What this trades, stated plainly rather than buried: the screen a step is
// described from is the screen as of the END OF THE PREVIOUS GESTURE, not the
// instant of this one. Nothing the HUMAN does can change it in between - their
// only way to touch the device is through this very path - but the APP can, by
// finishing a load or running an animation. resolveScreen falls back to a
// synchronous read whenever the maintained screen cannot describe the gesture,
// which catches the common shape of that (the finger lands somewhere the old
// tree has nothing), and does NOT catch a screen that changed into something
// else equally resolvable. See the record for why no cheaper check is sound.
type seenScreen struct {
	snap simbridge.Snapshot
	at   time.Time
}

// screenCacheTTL bounds how old a maintained screen may be before a gesture
// pays for a fresh read instead.
//
// It is not a correctness guarantee - an app can change its screen a second
// after a refresh - and it is not pretending to be one. It bounds the blast
// radius of the case a TTL genuinely covers: a recording left open while
// somebody goes and does something else, coming back to a device that has
// moved on entirely. A minute is long enough that ordinary deliberation
// between two taps stays free, which is the whole point of maintaining the
// screen at all.
const screenCacheTTL = time.Minute

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
	delete(s.seen, key)
	s.recMu.Unlock()
	s.primeScreen(ctx, key)
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

// recorderSettleReads bounds the recorder's settle.
//
// It counts the settle's OWN reads, which start fresh - the first read has
// already happened by the time settling is decided on, so the worst case for
// one gesture is four reads, not three. Three allows one retry, which is what
// a screen one frame from being drawn needs, and four reads is far inside the
// hold's own 30 s TTL, so a screen that never settles still cannot hold a
// gesture open long enough to matter.
const recorderSettleReads = 3

// RecorderSettleMaxReads is that worst case, exported so the test that pins it
// and the constant it is derived from cannot drift apart.
const RecorderSettleMaxReads = recorderSettleReads + 1

// stableEnough decides whether the screen a step is about to be described from
// is worth describing from.
//
// The two signals are the ones that mean "this tree is not the screen yet",
// and both are things that silently produce a WRONG selector rather than an
// obviously missing one:
//
//   - nothing resolved under the gesture. Something was touched - the actor
//     saw it - so a tree with nothing there is a tree that has not caught up.
//     This is the case that produced the measurement's own false reading: a
//     web-hosted screen mid-load reported six elements where the settled
//     screen had twenty-one.
//   - the tree is the status bar and nothing else, which the read path
//     already treats as "the app has not published its screen yet".
//
// It deliberately does NOT try to detect a screen that resolved to something
// plausible but incomplete. That cannot be told apart from a finished screen
// without another read, and paying for one on every gesture is the cost this
// function exists to avoid.
func stableEnough(snap simbridge.Snapshot, resolved bool) bool {
	return resolved && !snap.OnlyStatusBar
}

// resolveScreen produces the screen a step is described from, and whatever the
// gesture resolved to on it.
//
// The fast path is the maintained screen, which costs nothing. The slow path
// is the read this used to do on every gesture, and it is taken exactly when
// the fast path cannot answer:
//
//   - nothing is maintained yet (the first gesture of a recording that was
//     started before the priming read finished), or
//   - what is maintained is older than screenCacheTTL, or
//   - the gesture does not resolve against it - which is the same condition
//     `stableEnough` has always used, now doing a second job: a finger that
//     lands where the remembered tree has nothing is the strongest cheap
//     signal that the remembered tree is not this screen.
//
// ok is false only when there is no screen to describe from at all. A read
// that fails must never fail the hold: the gesture already got its finger, and
// refusing it because the recorder could not read the screen would be damage,
// done exactly when a device is misbehaving and the human most needs to drive
// it. Losing one recorded step is a nuisance, not that.
func (s *Service) resolveScreen(ctx context.Context, udid string, intent GestureIntent) (
	snap simbridge.Snapshot, choice simflow.Choice, el simbridge.Element, found, ok bool,
) {
	if remembered, fresh := s.rememberedScreen(udid); fresh {
		choice, el, found = elementFor(remembered, intent)
		if stableEnough(remembered, found) {
			return remembered, choice, el, found, true
		}
	}

	// Nothing read here is written back into the maintained screen. There is
	// exactly one place that maintains it - the refresh after a gesture - and
	// that refresh runs moments after this, on this very gesture's release,
	// with a newer view of the screen than this one. A mutation check found the
	// write that used to be here: removing it changed nothing any test could
	// see, because it was overwritten before anything could read it.
	read := func(ctx context.Context) (simbridge.Snapshot, error) { return s.recorder.AX(ctx, udid) }
	snap, err := read(ctx)
	if err != nil {
		slog.WarnContext(ctx, "sim recorder: could not read the screen; skipping this step",
			"udid", udid, "error", err)
		return simbridge.Snapshot{}, simflow.Choice{}, simbridge.Element{}, false, false
	}
	choice, el, found = elementFor(snap, intent)
	if stableEnough(snap, found) {
		return snap, choice, el, found, true
	}

	// The screen this step will be described from has not arrived. Read again
	// until it stops changing, then resolve against THAT.
	//
	// ⚠ Conditional, not unconditional, and the condition is the whole design.
	// This is the one path still on the gesture's critical path, so settling
	// every gesture would put a full AX read back in front of every tap a
	// human makes while recording - which is the bug this file was rewritten
	// to remove. The budget is only spent on the case that actually corrupts a
	// selector: nothing resolvable under the finger, or a tree that is still
	// just the status bar. `ao sim ax --settle` is the unconditional one, for
	// callers reading a screen rather than acting on it.
	//
	// A settle that fails is not an error here: the step is still worth
	// recording from the last read, exactly as it would have been without this
	// at all.
	if settled, res, settleErr := simbridge.ReadSettled(ctx, read, simbridge.SettleOptions{MaxReads: recorderSettleReads}); settleErr == nil {
		snap = settled
		choice, el, found = elementFor(snap, intent)
		slog.DebugContext(ctx, "sim recorder: settled the screen before resolving a step",
			"udid", udid, "reads", res.Reads, "settled", res.Settled, "resolved", found)
	}
	return snap, choice, el, found, true
}

// rememberedScreen returns the maintained screen, and whether it is young
// enough to describe a gesture from.
func (s *Service) rememberedScreen(udid string) (simbridge.Snapshot, bool) {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	seen, ok := s.seen[udid]
	if !ok {
		return simbridge.Snapshot{}, false
	}
	return seen.snap, s.now().Sub(seen.at) <= screenCacheTTL
}

func (s *Service) rememberScreen(udid string, snap simbridge.Snapshot) {
	s.recMu.Lock()
	defer s.recMu.Unlock()
	s.seen[udid] = seenScreen{snap: snap, at: s.now()}
}

// primeScreen reads the screen while the recording is being opened, so the
// FIRST gesture is as cheap as every other one.
//
// 🗝 Synchronous, and that is the point rather than an oversight. Somebody
// starting a recording and immediately dragging is not an edge case, it is the
// sequence a human performs - and measured, that first gesture cost 784 ms
// when the priming read was still in flight behind it, because the bridge
// serializes. Doing it here moves that half-second onto the press of a button,
// where nothing is mid-gesture and a mode taking a moment to arm is ordinary,
// instead of onto the first thing the human does with their finger.
//
// Best effort: a screen that cannot be read must not stop a recording from
// opening. The first gesture then pays for a read, exactly as it would have.
func (s *Service) primeScreen(ctx context.Context, udid string) {
	snap, err := s.recorder.AX(ctx, udid)
	if err != nil {
		slog.DebugContext(ctx, "sim recorder: could not prime the screen", "udid", udid, "error", err)
		return
	}
	s.rememberScreen(udid, snap)
}

// refreshScreen reads the screen into the maintained one, off any caller's
// critical path.
//
// ⚠ It is deliberately NOT waited on anywhere. The point is that the read
// happens while the human is looking at what a gesture just did; a caller that
// waited for it would have put the read straight back in front of them. At
// most one is in flight per device, because the bridge serializes anyway and a
// queue of reads behind a human's next touch is the exact cost being removed.
func (s *Service) refreshScreen(udid string) {
	s.recMu.Lock()
	if s.refreshing[udid] {
		s.recMu.Unlock()
		return
	}
	s.refreshing[udid] = true
	s.recMu.Unlock()

	s.runRefresh(func() {
		defer func() {
			s.recMu.Lock()
			delete(s.refreshing, udid)
			s.recMu.Unlock()
		}()
		ctx, cancel := context.WithTimeout(context.Background(), screenRefreshTimeout)
		defer cancel()
		snap, err := s.recorder.AX(ctx, udid)
		if err != nil {
			// Nothing to do about it and nobody to tell: the next gesture that
			// needs a screen and has none will read one itself.
			slog.DebugContext(ctx, "sim recorder: could not refresh the screen", "udid", udid, "error", err)
			return
		}
		s.rememberScreen(udid, snap)
	})
}

// screenRefreshTimeout bounds a background read. It is generous because the
// read it covers is slow by nature and nobody is waiting for it; it exists so
// a wedged bridge cannot leave a refresh marked in flight forever.
const screenRefreshTimeout = 30 * time.Second

// recordIntent is what AcquireHold calls, once the hold itself is granted, to
// resolve intent into a step and stash it under token. expiresAt is the
// hold's own expiry, carried through so the stash entry cannot outlive it.
//
// The recording row is read first, and that read - a single indexed lookup -
// is the ONLY cost paid when nothing is being recorded. The screen is read
// only once a recording is confirmed open; the overwhelming majority of
// gestures never reach that call.
func (s *Service) recordIntent(ctx context.Context, udid, token string, intent GestureIntent, expiresAt time.Time) {
	rec, ok, err := s.store.GetSimRecording(ctx, udid)
	if err != nil {
		slog.WarnContext(ctx, "sim recorder: could not check for an open recording; skipping this step",
			"udid", udid, "error", err)
		return
	}
	if !ok || rec.StoppedAt != nil {
		return
	}

	snap, choice, el, found, ok := s.resolveScreen(ctx, udid, intent)
	if !ok {
		return
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
		At:            s.now(),
		Kind:          intent.Kind,
		SelectorRung:  int64(choice.Rung),
		SelectorIndex: int64(choice.Index),
		Ambiguity:     int64(choice.Ambiguity),
		OffScreen:     choice.OffScreen,
		ScreenChange:  screenChange,
		X:             intent.X,
		Y:             intent.Y,
		ToX:           intent.ToX,
		ToY:           intent.ToY,
		DurationMS:    int64(intent.DurationMS),
		Text:          intent.Text,
		Detail:        intent.Name,
	}
	switch choice.Rung {
	case simflow.RungText, simflow.RungTextIndex:
		step.Selector = choice.Text
	case simflow.RungTextAnchor:
		step.Selector = choice.Text
		step.SelectorAnchor = choice.Anchor
		step.SelectorAnchorRel = string(choice.Relation)
	case simflow.RungID:
		step.Selector = choice.ID
	}

	s.recMu.Lock()
	s.sweepExpiredPendingLocked(s.now())
	s.pending[token] = pending{
		udid:        udid,
		step:        step,
		frontmost:   snap.Frontmost.BundleID,
		fingerprint: fingerprintOf(snap),
		expiresAt:   expiresAt,
	}
	s.recMu.Unlock()
}

// sweepExpiredPendingLocked drops every stash entry whose owning hold has
// expired as of now. It must be called with recMu already held.
//
// AcquireHold is the only path that grows the map, so it is the only path
// that needs to shrink it: a stash entry for a hold that lapsed without a
// ReleaseHold (a client that died mid-gesture) would otherwise sit forever in
// a daemon that stays up for weeks. This is O(entries) over a map that holds
// at most one entry per device currently being driven, so it costs nothing
// worth a background sweeper for.
func (s *Service) sweepExpiredPendingLocked(now time.Time) {
	for token, p := range s.pending {
		if !p.expiresAt.After(now) {
			delete(s.pending, token)
		}
	}
}

// finishRecording is what ReleaseHold calls once the hold itself has been
// given back. It always drops the stash for token; it appends the step to the
// recording only when earn is true (the hold both belonged to this caller and
// the gesture it covered was actually performed).
//
// end is the release's own account of where the gesture finished, and is set
// only by a gesture whose end could not be known when the hold was taken - a
// drag. It is applied here rather than at record time because that is the
// first moment it exists: `drag-begin` carries a start and nothing else, so a
// step stashed from it has no end at all until the drag ends. A drag that is
// abandoned rather than ended deliberately never reaches this line with earn
// set, so there is no path on which a step is written without a real end.
func (s *Service) finishRecording(ctx context.Context, token string, earn bool, end *simbridge.Point) {
	s.recMu.Lock()
	p, ok := s.pending[token]
	delete(s.pending, token)
	s.recMu.Unlock()
	if !ok {
		return
	}
	// The gesture is over, so this is the moment to look: the human is reading
	// what they just did, nothing is waiting on the bridge, and what this read
	// returns is the "before" screen of whatever they do next. It runs whether
	// or not the step was earned - a gesture that was attempted and failed
	// still leaves the screen wherever it left it.
	s.refreshScreen(p.udid)
	if !earn {
		return
	}
	if end != nil {
		p.step.ToX, p.step.ToY = end.X, end.Y
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
// elementFor resolves what a step targeted into the Choice it should be
// recorded as, plus a representative element for screen-change fingerprinting
// (fingerprintKeyFor, below) when there is one. Both answers come from the
// SAME snap this call already read, so ambiguity and index still reflect what
// was actually on screen at record time.
//
// By the name the caller gave when there is one (Label wins over ID, matching
// the CLI's own exclusivity - the two are never both set in practice, but
// preferring one deterministically costs nothing), falling back to
// hit-testing the point otherwise.
func elementFor(snap simbridge.Snapshot, intent GestureIntent) (simflow.Choice, simbridge.Element, bool) {
	switch {
	case intent.Label != "":
		return selectorChoice(snap, simbridge.Selector{Kind: simbridge.SelectByLabel, Text: intent.Label})
	case intent.ID != "":
		return selectorChoice(snap, simbridge.Selector{Kind: simbridge.SelectByID, Text: intent.ID})
	default:
		el, found := hitTest(snap.Elements, intent.X, intent.Y)
		if !found {
			return simflow.Choice{Rung: simflow.RungNone}, simbridge.Element{}, false
		}
		return simflow.For(snap, el), el, true
	}
}

// selectorChoice resolves a by-name selector against snap.
//
// A unique match goes through simflow.For exactly like a coordinate hit does.
// An ambiguous match is NOT reported as not-found: there were reachable
// candidates, they just could not be told apart, so this records the name
// that was searched for and how many things answered to it - a RungText (or,
// for an id, RungID) Choice with Ambiguity > 1 and no Index. Render's
// RungText branch turns that into a warning rather than the false "cannot be
// addressed" comment an unaddressable (RungNone) step would get. Guessing
// which candidate was meant, instead, would reintroduce the exact
// wrong-element bug 0039_sim_recording_step_index.sql exists to fix, so no
// index is ever invented here - only Emit or a human adds one, deliberately.
//
// Anything else (no match at all, an empty selector) resolves to not-found,
// same as before: the gesture already happened, and this is only the
// recorder's best account of what it hit, never a gate on it.
func selectorChoice(snap simbridge.Snapshot, selector simbridge.Selector) (simflow.Choice, simbridge.Element, bool) {
	match, err := simbridge.Select(snap, selector)
	if err == nil {
		return simflow.For(snap, match.Element), match.Element, true
	}
	var ambiguous *simbridge.AmbiguousMatchError
	if errors.As(err, &ambiguous) {
		text := strings.TrimSpace(selector.Text)
		if selector.Kind == simbridge.SelectByID {
			return simflow.Choice{Rung: simflow.RungID, ID: text, Ambiguity: len(ambiguous.Matches)},
				simbridge.Element{ID: text}, true
		}
		// Through simflow, not built here, so the name gets the SAME escaping
		// a unique match gets from For: Maestro matches text as a regex, and a
		// label like "Continue." left raw would also match "Continue!" - the
		// over-matching this escaping exists to prevent, arriving on the one
		// path that already could not tell its candidates apart. The element
		// returned alongside keeps the RAW label: that one is only ever
		// compared against the labels in a screen fingerprint, which are raw
		// too.
		return simflow.ForAmbiguousText(text, len(ambiguous.Matches)),
			simbridge.Element{Label: text}, true
	}
	return simflow.Choice{Rung: simflow.RungNone}, simbridge.Element{}, false
}

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
