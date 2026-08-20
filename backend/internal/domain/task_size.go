package domain

// TaskSize is the orchestrator's estimate of how much process ceremony a worker
// task warrants. It is captured once at spawn (`ao spawn --task-size`), persisted
// on the session, and consumed only by the worker system prompt: a `mechanical`
// task is explicitly authorized to skip the heavyweight process skills
// (brainstorming / writing-plans / TDD) and go straight to edit + verify, cutting
// the turn-count blow-up a small change would otherwise incur. `standard` (the
// default) and `deep` keep the full default ceremony; `deep` is a distinct,
// persisted tag today but does not change the prompt.
type TaskSize string

// Task size values. The zero value is the empty string, which WithDefault
// normalizes to TaskSizeStandard so an unset column/flag means "full ceremony".
const (
	TaskSizeMechanical TaskSize = "mechanical"
	TaskSizeStandard   TaskSize = "standard"
	TaskSizeDeep       TaskSize = "deep"
)

// Valid reports whether s is one of the known task sizes. The empty string is
// NOT valid (callers use WithDefault to normalize an unset value); Valid is for
// rejecting a garbage, explicitly-set value at the API/CLI boundary.
func (s TaskSize) Valid() bool {
	switch s {
	case TaskSizeMechanical, TaskSizeStandard, TaskSizeDeep:
		return true
	}
	return false
}

// WithDefault returns s unchanged when it is a known size, and TaskSizeStandard
// when it is empty or unrecognized. Persistence and prompt assembly go through
// this so a missing value (old row, omitted flag) resolves to full ceremony.
func (s TaskSize) WithDefault() TaskSize {
	if s.Valid() {
		return s
	}
	return TaskSizeStandard
}

// WantsCrew reports whether a task of this size is worked by a CREW (dev + qa)
// rather than by dev alone.
//
// It is the ONE place the crew shape is decided, and it is decided at spawn from
// a tag a human already had to choose. `standard` and `deep` get a qa member;
// `mechanical` - a rename, a copy tweak, a config bump - does not, because below
// roughly 70 turns every crew shape costs more than a single worker (design §3).
// An unset size resolves to standard through WithDefault, so a spawn that says
// nothing gets the crew: the tag is what OPTS OUT of it.
func (s TaskSize) WantsCrew() bool {
	return s.WithDefault() != TaskSizeMechanical
}
