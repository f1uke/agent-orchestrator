import { useLayoutEffect, useRef, useState } from "react";
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
/** The tallest: three lines at 12px/1.35, inside 6px padding and the 2.4px rim. */
export const BUBBLE_MAX_HEIGHT = 66;

export type BubbleTone = "normal" | "alert";

/**
 * How much of its claim a bubble is still entitled to make.
 * `silent` is the absence of a bubble and is represented by not rendering one.
 */
export type BubbleDecay = "fresh" | "fading" | "settled";

/**
 * How much a card fades as its claim ages.
 *
 * Only the WORDS fade. The card itself stays opaque, because it is a
 * self-contained thing on somebody's wallpaper — a see-through card is a card
 * whose legibility depends on a desktop we do not control, which is the one thing
 * the whole palette exists to avoid. (It also let a Proc's own head show through
 * the tail, which is how this was spotted.)
 */
const DECAY_TEXT_OPACITY: Record<BubbleDecay, number> = { fresh: 1, fading: 0.66, settled: 0.66 };

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

/** Corner radius of the card, and of the outline drawn around it. */
const BUBBLE_RADIUS = 12;
/** The tail: how far in from its side it sits, how wide its mouth is, how far it drops. */
const TAIL_INSET = 16;
const TAIL_WIDTH = 16;
const TAIL_DROP = 9;

/**
 * The card's outline and the tail's, as ONE path.
 *
 * They used to be two shapes — a CSS border and a separate SVG wedge — with a
 * paper-coloured strip laid over the seam to hide the card's border where the
 * tail's mouth is. It never joined cleanly, and it never could: a CSS border and
 * an SVG stroke round their sub-pixels differently, and the wedge's stroke ends
 * had to land exactly where the erased segment began. Drawn as one path, the
 * border and the tail are literally the same stroke and cannot fail to meet.
 *
 * Traced clockwise from the top-left corner, so the bottom edge runs right to
 * left and the tail's right corner comes before its left one.
 */
function outlinePath(width: number, height: number, tail: "left" | "right"): string {
	const r = Math.min(BUBBLE_RADIUS, width / 2, height / 2);
	const right = tail === "left" ? TAIL_INSET + TAIL_WIDTH : width - TAIL_INSET;
	const left = right - TAIL_WIDTH;
	return [
		`M${r} 0`,
		`H${width - r}`,
		`A${r} ${r} 0 0 1 ${width} ${r}`,
		`V${height - r}`,
		`A${r} ${r} 0 0 1 ${width - r} ${height}`,
		`H${right}`,
		`L${(left + right) / 2} ${height + TAIL_DROP}`,
		`L${left} ${height}`,
		`H${r}`,
		`A${r} ${r} 0 0 1 0 ${height - r}`,
		`V${r}`,
		`A${r} ${r} 0 0 1 ${r} 0`,
		"Z",
	].join(" ");
}

export function Bubble({ text, tone = "normal", decay = "fresh", tail = "left", className }: BubbleProps) {
	const trimmed = text.trim();
	const card = useRef<HTMLDivElement>(null);
	const [size, setSize] = useState<{ width: number; height: number } | null>(null);

	// The outline is drawn around the TEXT box, so it has to know how big the text
	// box came out. Measured in a layout effect, which runs before the browser
	// paints, so there is no frame in which the card is drawn without its outline.
	useLayoutEffect(() => {
		const box = card.current;
		if (!box) return;
		// offsetWidth/Height, NOT getBoundingClientRect: the rect is multiplied by
		// every transform above it, and the overlay puts a transform on each Proc.
		// The outline has to be drawn in the card's own LAYOUT units or it comes out
		// scaled relative to the card it is meant to be wrapped around.
		const next = { width: box.offsetWidth, height: box.offsetHeight };
		setSize((current) => (current && current.width === next.width && current.height === next.height ? current : next));
	});

	if (!trimmed) return null;

	const suppressed = looksLikeRawCommand(trimmed);
	const shown = decay === "settled" || suppressed ? BUBBLE_COARSE_TEXT : trimmed;
	const colour =
		tone === "alert" ? PROP_COLOURS.bubbleAlert : shown === BUBBLE_COARSE_TEXT ? PROP_COLOURS.bubbleMuted : PROCS_INK;
	const rim = PROCS_RIM_PX;

	return (
		<div className={className} style={{ position: "relative", display: "inline-block" }}>
			{size ? (
				<svg
					data-bubble-tail
					width={size.width + rim * 2}
					height={size.height + TAIL_DROP + rim * 2}
					viewBox={`${-rim} ${-rim} ${size.width + rim * 2} ${size.height + TAIL_DROP + rim * 2}`}
					style={{ position: "absolute", left: -rim, top: -rim, pointerEvents: "none" }}
				>
					<path
						d={outlinePath(size.width, size.height, tail)}
						fill={PROP_COLOURS.paper}
						stroke={PROCS_INK}
						strokeWidth={rim}
						strokeLinejoin="round"
					/>
				</svg>
			) : null}
			<div
				ref={card}
				data-bubble
				data-tone={tone}
				data-decay={decay}
				style={{
					// The fill and the rim are the OUTLINE's now — one shape, one stroke —
					// but the colours stay declared here, because this is still the card and
					// they are still the two channels that carry it on a wallpaper.
					background: size ? "transparent" : PROP_COLOURS.paper,
					border: `${rim}px solid transparent`,
					borderColor: size ? "transparent" : PROCS_INK,
					borderRadius: `${BUBBLE_RADIUS}px`,
					color: colour,
					position: "relative",
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
				}}
			>
				{/* Decay animates opacity ONLY, so a fading bubble stays on the
				    compositor like everything else on the overlay — and it is the WORDS
				    that fade, never the card under them. */}
				<span data-bubble-text style={{ opacity: DECAY_TEXT_OPACITY[decay] }}>
					{shown}
				</span>
			</div>
		</div>
	);
}
