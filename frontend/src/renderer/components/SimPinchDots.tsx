/** How wide a contact is drawn. Simulator.app's own dots are about this size at 1:1. */
const PINCH_DOT_PX = 26;

/**
 * The pivot mark. Deliberately smaller and quieter than a contact: it is not a
 * third finger, it is the point the other two turn about. Simulator.app has one
 * too, behind its own `showPinchPivotPoint` preference - it is drawn always here
 * because without it "why do both dots move when I move one" is something a
 * person has to work out from watching.
 */
const PINCH_PIVOT_PX = 17;

/**
 * The two contacts of a hand-driven pinch, and the point they turn about.
 *
 * It is its own module because what has to be true of it - that the dots land
 * where the device coordinates say, on the PICTURE rather than on the pane
 * around it - is a question about layout, which jsdom answers by having none.
 * e2e/pinch-dots.spec.ts renders it in a real browser and measures it.
 *
 * ⚠ This is the one surface in the app that may not take its colours from the
 * theme, and the reason is what it sits on: not an AO surface, but whatever the
 * app being driven is showing - a white article one moment and a dark chart the
 * next. A token that reads well against `--bg` says nothing about either. So
 * each dot is drawn with its OWN contrast rather than borrowed contrast: a white
 * ring with a dark halo outside it and a dark line inside it, which cannot be
 * lost against a light background or a dark one. The fill is the app's accent so
 * that it still reads as a tool laid over the device rather than as something
 * the app under it drew.
 *
 * Positions arrive as CSS custom properties written straight onto the container
 * by the pointer handlers - see paintDots - because a pointer moves far faster
 * than this pane re-renders on purpose.
 */
export function SimPinchDots({
	inset,
	ref,
	screen,
}: {
	/** The device body's thickness, so the dots sit over the picture and not the frame. */
	inset: number;
	ref: React.Ref<HTMLDivElement>;
	/** How big the picture is being drawn, in pixels. */
	screen: { width: number; height: number };
}) {
	return (
		// 🗝 The dots are percentages of the PICTURE, so the box they live in has
		// to be the picture and not the pane around it. It is the SAME fit the
		// canvas is sized from (fitDevice, one call, shared) rather than a second
		// containment computed here - a dot placed against the pane instead would
		// be a few percent out, which on a phone is the edge of one button and the
		// middle of the next, and nothing about the DOM would look wrong.
		<div
			aria-hidden
			className="pointer-events-none absolute"
			data-testid="sim-pinch-dots"
			ref={ref}
			style={{ left: inset, top: inset, width: screen.width, height: screen.height }}
		>
			{/* The point the fingers turn about. Simulator.app has one of these
			    too (its own preference calls it the pinch pivot); without it the
			    fact that a pinch is centred on the middle of the screen is
			    something a person has to infer from the way the dots move. */}
			<span
				className="absolute"
				data-testid="sim-pinch-pivot"
				style={{
					left: "var(--pinch-px, 50%)",
					top: "var(--pinch-py, 50%)",
					width: PINCH_PIVOT_PX,
					height: PINCH_PIVOT_PX,
					marginLeft: -PINCH_PIVOT_PX / 2,
					marginTop: -PINCH_PIVOT_PX / 2,
				}}
			>
				<span style={{ ...pinchPivotArm, left: (PINCH_PIVOT_PX - 2) / 2, top: 0, width: 2, height: PINCH_PIVOT_PX }} />
				<span style={{ ...pinchPivotArm, left: 0, top: (PINCH_PIVOT_PX - 2) / 2, width: PINCH_PIVOT_PX, height: 2 }} />
			</span>
			<span data-testid="sim-pinch-dot-a" style={pinchDotStyle("a")} />
			<span data-testid="sim-pinch-dot-b" style={pinchDotStyle("b")} />
		</div>
	);
}

// ⚠ The arms need the SAME two-sided contrast the dots have, and for the same
// reason. A white cross with a faint outline reads as a smudge on a white
// article and disappears under a bright chart line - measured by looking at
// exactly those two grounds side by side, where the first version of this was
// visibly weaker than the dots it belongs with.
const pinchPivotArm: React.CSSProperties = {
	position: "absolute",
	background: "rgba(255,255,255,0.96)",
	boxShadow: "0 0 0 1.25px rgba(0,0,0,0.62)",
	borderRadius: 1,
};

function pinchDotStyle(which: "a" | "b"): React.CSSProperties {
	return {
		position: "absolute",
		left: `var(--pinch-${which}x, 50%)`,
		top: `var(--pinch-${which}y, 50%)`,
		width: PINCH_DOT_PX,
		height: PINCH_DOT_PX,
		marginLeft: -PINCH_DOT_PX / 2,
		marginTop: -PINCH_DOT_PX / 2,
		borderRadius: "9999px",
		background: "color-mix(in srgb, var(--accent) 42%, transparent)",
		border: "2px solid rgba(255,255,255,0.95)",
		boxShadow: "0 0 0 1px rgba(0,0,0,0.55), inset 0 0 0 1px rgba(0,0,0,0.35), 0 2px 8px rgba(0,0,0,0.4)",
	};
}
