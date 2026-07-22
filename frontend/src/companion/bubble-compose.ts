import { resolveAt, type ActivitySlots, type DetailSlot } from "./activity-decay";
import { parseMessageFrom } from "./conversation";
import { BUBBLE_COARSE_TEXT, type BubbleDecay, type BubbleTone } from "./Bubble";

// Turning the feed's DATA into the Proc's WORDS.
//
// The feed deliberately emits no English UI strings — a tool name, a curated base
// name, a model-authored sentence — so the sentence is composed here. Two rules
// shape every line below:
//
//   1. Never invent. A `tool_*` frame that arrives with no `tool` means "something
//      happened and we do not know what", and the honest rendering of that is
//      "Working…", not a plausible-sounding action. Guessing here would poison the
//      one thing the bubble is for.
//   2. Never leak. The server-side whitelist already guarantees only curated fields
//      reach the wire, so this renders what it is given and truncates it — it never
//      reaches for anything else.

/**
 * The hard bound on a sentence, in characters.
 *
 * The bubble wraps to three lines and clamps there, so this is not the visual
 * limit — it is the guard against a pathologically long string being laid out at
 * all. Sized to comfortably outlast three lines (~40 characters each) so the
 * CLAMP is what a reader sees ending the sentence, not a hard cut in the middle of
 * line one, which is what 90 characters was doing.
 */
const MAX_BUBBLE_CHARS = 160;

export type ComposedBubble = {
	text: string;
	tone: BubbleTone;
	decay: BubbleDecay;
};

// Verb per whitelisted tool. A tool absent from here is still NAMED rather than
// described, because we know it ran but not what it means.
const TOOL_PHRASES: Record<string, (target?: string) => string> = {
	Read: (t) => (t ? `Reading ${t}` : "Reading a file"),
	Edit: (t) => (t ? `Editing ${t}` : "Editing a file"),
	Write: (t) => (t ? `Writing ${t}` : "Writing a file"),
	NotebookEdit: (t) => (t ? `Editing ${t}` : "Editing a notebook"),
	Glob: (t) => (t ? `Looking for ${t}` : "Looking for files"),
	Grep: (t) => (t ? `Searching for ${t}` : "Searching"),
	WebFetch: (t) => (t ? `Fetching ${t}` : "Fetching a page"),
	WebSearch: (t) => (t ? `Searching the web for ${t}` : "Searching the web"),
	TodoWrite: () => "Updating its to-do list",
};

const COARSE_TEXT: Record<string, string> = {
	working: BUBBLE_COARSE_TEXT,
	// Only ever set by a real permission prompt, never by AO's timeout guesses —
	// which is what makes it safe to raise the alarm on.
	waiting: "Waiting for you",
	idle: "Resting",
	exited: "Finished",
};

function truncate(text: string): string {
	const clean = text.replace(/\s+/g, " ").trim();
	return clean.length <= MAX_BUBBLE_CHARS ? clean : `${clean.slice(0, MAX_BUBBLE_CHARS - 1)}…`;
}

function detailSentence(detail: DetailSlot): string {
	if (detail.kind === "message") {
		// A message that arrived for this session. Marked as SPEECH so it cannot be
		// mistaken for the agent narrating itself — and attributed to whoever sent
		// it, which `ao send` stamps onto the body. A message a person typed into
		// the app carries no stamp; it is then quoted without an invented sender.
		const { sender, body } = parseMessageFrom(detail.text ?? "");
		if (!body) return sender ? `@${sender} said something` : "A message arrived";
		return sender ? `@${sender}: ${body}` : `“${body}”`;
	}
	// A model-authored sentence (a Bash/Task description) already reads like speech.
	const base = detail.text
		? detail.text
		: detail.tool
			? (TOOL_PHRASES[detail.tool]?.(detail.target) ?? `Running ${detail.tool}`)
			: // No tool, no text: something happened, and we do not know what.
				BUBBLE_COARSE_TEXT;
	return detail.kind === "tool_failed" ? `${base} — failed` : base;
}

/** What this session's Proc may honestly say at `now`, or null for silence. */
export function composeBubble(slots: ActivitySlots, now: number): ComposedBubble | null {
	const shown = resolveAt(slots, now);

	if (shown.level === "detail" && shown.detail) {
		return { text: truncate(detailSentence(shown.detail)), tone: "normal", decay: "fresh" };
	}
	if (shown.level === "coarse" && shown.coarse) {
		return {
			text: truncate(COARSE_TEXT[shown.coarse] ?? BUBBLE_COARSE_TEXT),
			tone: shown.coarse === "waiting" ? "alert" : "normal",
			// A coarse line is a weaker claim than a detail line, and should look it.
			decay: "settled",
		};
	}
	// Nothing true left to say. No bubble — not an empty one, not a placeholder.
	return null;
}
