package releasetrain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"
)

// FixKind is what a rule's Fix button does, as the catalogue lists it.
type FixKind string

// Fix kinds. Apply writes AO's own data; queue records a dry run of a write
// to another system; choose asks which side is right first; copy hands text
// to paste.
const (
	FixNone   FixKind = ""
	FixApply  FixKind = "apply"
	FixQueue  FixKind = "queue"
	FixChoose FixKind = "choose"
	FixCopy   FixKind = "copy"
)

// Enum lists the fix kinds for the API schema.
func (FixKind) Enum() []any { return []any{FixApply, FixQueue, FixChoose, FixCopy} }

// FixMode is what a plan can do right now: its rule's kind, or blocked.
type FixMode string

// Fix modes.
const (
	ModeApply   FixMode = "apply"
	ModeQueue   FixMode = "queue"
	ModeChoose  FixMode = "choose"
	ModeCopy    FixMode = "copy"
	ModeBlocked FixMode = "blocked"
)

// Enum lists the fix modes for the API schema.
func (FixMode) Enum() []any { return []any{ModeApply, ModeQueue, ModeChoose, ModeCopy, ModeBlocked} }

// FixStep is one effect with how a person reads it.
type FixStep struct {
	Label   string       `json:"label"`
	Before  string       `json:"before"`
	After   string       `json:"after"`
	Effect  Effect       `json:"effect"`
	Request ShownRequest `json:"request"`
}

func newStep(label, before, after string, e Effect) FixStep {
	return FixStep{Label: label, Before: before, After: after, Effect: e, Request: e.Request()}
}

// UnmarshalJSON reads the step's sealed effect by its kind.
func (s *FixStep) UnmarshalJSON(b []byte) error {
	var raw struct {
		Label   string          `json:"label"`
		Before  string          `json:"before"`
		After   string          `json:"after"`
		Effect  json.RawMessage `json:"effect"`
		Request ShownRequest    `json:"request"`
	}
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	e, err := DecodeEffect(raw.Effect)
	if err != nil {
		return err
	}
	*s = FixStep{Label: raw.Label, Before: raw.Before, After: raw.After, Effect: e, Request: raw.Request}
	return nil
}

// FixChoice is one side of a choose plan.
type FixChoice struct {
	ID    string    `json:"id"`
	Label string    `json:"label"`
	Steps []FixStep `json:"steps"`
}

// FixNeed is what a blocked plan waits for: the long-lived branch sheet to open.
type FixNeed struct {
	Label string `json:"label"`
	LLB   PlanID `json:"llb"`
}

// FixPlan is what confirming a hit's fix would do, as of one Board.
type FixPlan struct {
	Hit     HitID       `json:"hit"`
	Rule    RuleID      `json:"rule"`
	Title   string      `json:"title"`
	Note    string      `json:"note,omitempty"`
	Mode    FixMode     `json:"mode"`
	Choice  string      `json:"choice,omitempty"`
	Choices []FixChoice `json:"choices,omitempty"`
	Steps   []FixStep   `json:"steps"`
	Need    *FixNeed    `json:"need,omitempty"`
	// Fingerprint hashes the chosen steps with their Before values, so a
	// confirm against changed data is refused.
	Fingerprint string `json:"fingerprint"`
}

// Recordable reports whether confirming the plan records an outbox row:
// copy and blocked plans, and a choose plan with no choice, have nothing to record.
func (p FixPlan) Recordable() bool {
	return len(p.Steps) > 0 && (p.Mode == ModeApply || p.Mode == ModeQueue || p.Mode == ModeChoose)
}

// Applies reports whether every step writes AO's own data, so the fix applies
// instead of being recorded as a dry run.
func (p FixPlan) Applies() bool {
	for _, s := range p.Steps {
		if s.Effect.Kind() != EffectAOPlanTarget {
			return false
		}
	}
	return len(p.Steps) > 0
}

func fingerprint(hit HitID, choice string, steps []FixStep) string {
	effects := make([]Effect, len(steps))
	for i, s := range steps {
		effects[i] = s.Effect
	}
	b, err := json.Marshal(struct {
		Hit     HitID    `json:"hit"`
		Choice  string   `json:"choice"`
		Effects []Effect `json:"effects"`
	}{hit, choice, effects})
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:12])
}

// FixState is where a confirmed fix stands in the outbox.
type FixState string

// Outbox states.
const (
	FixDryRun     FixState = "dry_run"
	FixApplying   FixState = "applying"
	FixApplied    FixState = "applied"
	FixFailed     FixState = "failed"
	FixUndone     FixState = "undone"
	FixWithdrawn  FixState = "withdrawn"
	FixSuperseded FixState = "superseded"
)

// Enum lists the outbox states for the API schema.
func (FixState) Enum() []any {
	return []any{FixDryRun, FixApplying, FixApplied, FixFailed, FixUndone, FixWithdrawn, FixSuperseded}
}

// Live reports whether the state holds the hit's one live fix per fingerprint.
func (s FixState) Live() bool { return s == FixDryRun || s == FixApplying || s == FixApplied }

// FixEventKind names one outbox transition in the audit log.
type FixEventKind string

// Audit event kinds.
const (
	EventConfirmed      FixEventKind = "confirmed"
	EventDryRunRecorded FixEventKind = "dry_run_recorded"
	EventApplied        FixEventKind = "applied"
	EventApplyFailed    FixEventKind = "apply_failed"
	EventUndone         FixEventKind = "undone"
	EventUndoFailed     FixEventKind = "undo_failed"
	EventWithdrawn      FixEventKind = "withdrawn"
	EventSuperseded     FixEventKind = "superseded"
)

// Enum lists the event kinds for the API schema.
func (FixEventKind) Enum() []any {
	return []any{EventConfirmed, EventDryRunRecorded, EventApplied, EventApplyFailed, EventUndone, EventUndoFailed, EventWithdrawn, EventSuperseded}
}

// FixAction is what moves a fix from one state to the next.
type FixAction string

// Outbox actions; the empty state is a fix not yet stored.
const (
	ActRecordDryRun FixAction = "record_dry_run"
	ActStartApply   FixAction = "start_apply"
	ActApplyOK      FixAction = "apply_ok"
	ActApplyFail    FixAction = "apply_fail"
	ActUndoOK       FixAction = "undo_ok"
	ActUndoFail     FixAction = "undo_fail"
	ActWithdraw     FixAction = "withdraw"
	ActSupersede    FixAction = "supersede"
)

type fixTransition struct {
	from  FixState
	act   FixAction
	to    FixState
	event FixEventKind
}

var fixTransitions = []fixTransition{
	{"", ActRecordDryRun, FixDryRun, EventDryRunRecorded},
	{"", ActStartApply, FixApplying, EventConfirmed},
	{FixApplying, ActApplyOK, FixApplied, EventApplied},
	{FixApplying, ActApplyFail, FixFailed, EventApplyFailed},
	{FixApplied, ActUndoOK, FixUndone, EventUndone},
	{FixApplied, ActUndoFail, FixApplied, EventUndoFailed},
	{FixDryRun, ActWithdraw, FixWithdrawn, EventWithdrawn},
	{FixDryRun, ActSupersede, FixSuperseded, EventSuperseded},
}

// TransitionError is an action the outbox table does not allow from a state.
type TransitionError struct {
	From FixState
	Act  FixAction
}

func (e *TransitionError) Error() string {
	from := string(e.From)
	if from == "" {
		from = "new"
	}
	return fmt.Sprintf("a %s fix cannot %s", from, e.Act)
}

// Transition is the outbox table: the state act leads to from, and the audit
// event it appends, or a *TransitionError.
func Transition(from FixState, act FixAction) (FixState, FixEventKind, error) {
	for _, t := range fixTransitions {
		if t.from == from && t.act == act {
			return t.to, t.event, nil
		}
	}
	return "", "", &TransitionError{From: from, Act: act}
}

// UndoAction is what undo means in a state: a dry run is withdrawn, an
// applied fix is reverted.
func UndoAction(s FixState) (FixAction, bool) {
	switch s {
	case FixDryRun:
		return ActWithdraw, true
	case FixApplied:
		return ActUndoOK, true
	}
	return "", false
}

// FixRef is the open fix of a hit as the Board shows it.
type FixRef struct {
	ID    string   `json:"id"`
	State FixState `json:"state"`
}

// Fix is one confirmed fix in the outbox.
type Fix struct {
	ID        string     `json:"id"`
	Hit       HitID      `json:"hit"`
	Rule      RuleID     `json:"rule"`
	Choice    string     `json:"choice,omitempty"`
	Plan      FixPlan    `json:"plan"`
	State     FixState   `json:"state"`
	CreatedAt time.Time  `json:"createdAt"`
	UpdatedAt time.Time  `json:"updatedAt"`
	Events    []FixEvent `json:"events"`
}

// FixEvent is one row of a fix's append-only audit log.
type FixEvent struct {
	Seq    int64           `json:"seq"`
	At     time.Time       `json:"at"`
	Actor  string          `json:"actor"`
	Kind   FixEventKind    `json:"kind"`
	Detail json.RawMessage `json:"detail"`
}

// Errors PlanFix returns.
var (
	ErrNoSuchHit     = errors.New("release trains: no such hit on the board")
	ErrNotFixable    = errors.New("release trains: this rule has no fix")
	ErrUnknownChoice = errors.New("release trains: unknown fix choice")
)
