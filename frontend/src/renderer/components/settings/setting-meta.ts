// The two facts every setting on this page has to state about itself, and that
// the pre-B page stated nowhere: WHOSE setting it is, and WHEN a change to it
// takes effect. They are modelled here rather than written as prose per field so
// a row cannot quietly disagree with the row above it.

// When a change bites. The distinction was invisible before B, and it is the
// reason the page could feel untrustworthy: turning something on and watching a
// running session ignore it reads as a bug rather than as design.
export type SettingTiming =
	| "next-worker" // rendered into the spawn/system prompt; a running session keeps what it was born with
	| "next-orchestrator" // same, for the orchestrator session - it has to restart
	| "live" // read at the moment the behaviour fires, so running sessions change too
	| "on-save" // takes effect as soon as the save lands; no session involved
	| "instant" // the control acts on click and never routes through the save bar
	| "readonly";

export const TIMING_LABEL: Record<SettingTiming, string> = {
	"next-worker": "Next worker",
	"next-orchestrator": "Needs orchestrator restart",
	live: "Now, running sessions too",
	"on-save": "On save",
	instant: "Instant, not saved",
	readonly: "Read-only",
};

// Amber is reserved for the timings a person guesses wrong. Marking all six would
// make the page shout and teach nothing.
export const TIMING_TONE: Record<SettingTiming, "quiet" | "warn"> = {
	"next-worker": "quiet",
	"next-orchestrator": "warn",
	live: "warn",
	"on-save": "quiet",
	instant: "warn",
	readonly: "quiet",
};

// How a setting relates to the other scope. The honest finding behind this type
// is that almost nothing is overridable: response language is the only true
// Global -> Project override in the whole page, the per-project prompts APPEND
// rather than replace, and auto-nudge is a global default a SESSION (not a
// project) may answer differently. Naming those three shapes separately is what
// stops the page implying a hierarchy that does not exist.
export type SettingOwnership =
	| { kind: "global-only" }
	| { kind: "global-appendable" }
	| { kind: "global-overridable"; overriddenBy?: { project: string; value: string }[] }
	| { kind: "project-only" }
	| { kind: "project-override"; globalValue: string; overriding: boolean }
	| { kind: "project-appends"; base: string }
	| { kind: "global-session-override" }
	| { kind: "readonly" };
