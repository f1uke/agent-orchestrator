import { PROCS_INK, PROCS_RIM_PX, PROP_COLOURS } from "./palette";

// The speech bubble: one line a Proc says about what its session is doing.
//
// Three rules from the design, all of them about not lying:
//
//   1. It is SHAPED AROUND the model's own one-line description of what it is
//      doing, never the raw command — even though the raw command is available.
//      A description cannot leak a path, a host or a token, and it reads as
//      something a character could say.
//   2. It DECAYS. Every string carries a timestamp: fresh asserts fully, past its
//      TTL it dims (a weaker claim should look weaker), then it collapses to the
//      coarsest thing still true, then it says nothing. A Proc still saying
//      "Running the test suite" ten minutes after the run ended is lying.
//   3. It says NOTHING rather than something empty. No "unsupported" badge, no
//      greyed placeholder, no explanatory tooltip. A Proc without a bubble is just
//      a Proc, which is what lets the feature ship for the one harness that has
//      hooks without making the others look broken.
//
// Like the pets, it floats on the user's wallpaper rather than an app surface, so
// it is a self-contained card — fill plus the 2.4px ink rim — and its text is
// measured against its OWN fill, never against a desktop we do not control.

/** What a bubble collapses to when its detail has gone stale. */
export const BUBBLE_COARSE_TEXT = "Working…";

/** The widest a bubble card ever gets. Shared with the layout, which decides which way it opens. */
export const BUBBLE_MAX_WIDTH = 200;

export type BubbleTone = "normal" | "alert";

/**
 * How much of its claim a bubble is still entitled to make.
 * `silent` is the absence of a bubble and is represented by not rendering one.
 */
export type BubbleDecay = "fresh" | "fading" | "settled";

const DECAY_OPACITY: Record<BubbleDecay, number> = { fresh: 1, fading: 0.62, settled: 0.62 };

export type BubbleProps = {
	text: string;
	tone?: BubbleTone;
	decay?: BubbleDecay;
	/**
	 * Which side of the card the tail hangs from — i.e. which way the card opens
	 * from its Proc. Right by default; flipped when the card would otherwise open
	 * straight across whoever the Proc is talking to.
	 */
	tail?: "left" | "right";
	className?: string;
};

// Shapes that mean "this is a shell command, not a sentence": an argument flag, a
// pipe or chain, a command substitution, an absolute or home-relative path, a URL.
const COMMAND_SHAPES = [/\s--?[a-z]/i, /[|&;]{1,2}\s*\S/, /\$\(/, /(^|\s)[~/]\S*\//, /\bhttps?:\/\//i];

/**
 * True when a string is plainly a command rather than a sentence about one.
 *
 * The feed whitelists fields before anything is emitted and THAT is the real guard.
 * This is the last mile: the bubble is the only place agent-derived text is put on
 * screen, so if a raw command ever reaches it, the bubble must not be the thing
 * that shows someone's path, host or token to the room.
 */
export function looksLikeRawCommand(text: string): boolean {
	return COMMAND_SHAPES.some((shape) => shape.test(text));
}

export function Bubble({ text, tone = "normal", decay = "fresh", tail = "left", className }: BubbleProps) {
	const trimmed = text.trim();
	if (!trimmed) return null;

	const suppressed = looksLikeRawCommand(trimmed);
	const shown = decay === "settled" || suppressed ? BUBBLE_COARSE_TEXT : trimmed;
	const colour =
		tone === "alert" ? PROP_COLOURS.bubbleAlert : shown === BUBBLE_COARSE_TEXT ? PROP_COLOURS.bubbleMuted : PROCS_INK;

	return (
		<div className={className} style={{ position: "relative", display: "inline-block" }}>
			<div
				data-bubble
				data-tone={tone}
				data-decay={decay}
				style={{
					background: PROP_COLOURS.paper,
					border: `${PROCS_RIM_PX}px solid ${PROCS_INK}`,
					borderColor: PROCS_INK,
					borderRadius: "12px",
					color: colour,
					padding: "6px 10px",
					font: "500 12px/1.35 ui-sans-serif, system-ui, sans-serif",
					// NARROWER but up to three lines TALL. One 220px line held about
					// thirty characters and threw the rest of the sentence away, which is
					// the only thing the bubble is for. Three narrower lines hold roughly
					// twice as much AND lean less far over the Proc beside it — the bubble
					// grows upward, into empty sky, rather than sideways into a neighbour.
					boxSizing: "border-box",
					maxWidth: `${BUBBLE_MAX_WIDTH}px`,
					display: "-webkit-box",
					WebkitBoxOrient: "vertical",
					WebkitLineClamp: 3,
					overflow: "hidden",
					// Wrapping at spaces, but a single unbroken token — a long path or
					// identifier that slipped past the whitelist — must not push the card
					// wider than its own limit.
					overflowWrap: "anywhere",
					// Decay animates opacity ONLY, so a fading bubble stays on the
					// compositor like everything else on the overlay.
					opacity: DECAY_OPACITY[decay],
				}}
			>
				{shown}
			</div>
			{/* The tail, drawn as its own rimmed wedge so the rim stays unbroken. */}
			<svg
				data-bubble-tail
				width="18"
				height="11"
				viewBox="0 0 18 11"
				style={{
					position: "absolute",
					[tail === "left" ? "left" : "right"]: "18px",
					top: "100%",
					marginTop: "-1px",
					opacity: DECAY_OPACITY[decay],
				}}
			>
				<path
					d="M1 0 L9 9 L17 0"
					fill={PROP_COLOURS.paper}
					stroke={PROCS_INK}
					strokeWidth={PROCS_RIM_PX}
					strokeLinejoin="round"
				/>
				{/* Covers the rim where the tail meets the card, so they read as one shape. */}
				<path d="M2 0 L16 0" stroke={PROP_COLOURS.paper} strokeWidth={PROCS_RIM_PX} />
			</svg>
		</div>
	);
}
