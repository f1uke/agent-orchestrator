// Who owns the pointer: the overlay, or the desktop underneath it.
//
// The overlay is a band across the bottom of the screen that must be INVISIBLE to
// the mouse everywhere except on a Proc itself. Getting this wrong is not a
// cosmetic bug — it makes the bottom of the user's screen stop responding to
// clicks, which is why it is the first thing fixed here.
//
// The original version put `pointer-events: auto` on each Proc's wrapper div. That
// wrapper is the whole DRAWN FRAME: the figure, plus the ground prop hanging off
// one side and the held prop off the other, ~150px wide and almost entirely
// transparent. Eight Procs therefore laid ~1200px of invisible dead zone across the
// band and swallowed every click that landed in it.
//
// The fix is two-part, and both parts are needed:
//   1. CSS: only the FIGURE's painted shapes take the pointer. SVG hit-testing is
//      per-pixel by default (`visiblePainted`), so the transparent gaps inside even
//      the figure's own box fall through.
//   2. This module: the main process is told to stop ignoring mouse events ONLY
//      while the pointer is genuinely on a Proc, and told to resume the moment it
//      is not — including when the pointer leaves the window without ever crossing
//      back off the pet.

/** Marks the group holding the character itself, as opposed to its scenery. */
export const FIGURE_ATTRIBUTE = "data-figure";

const FIGURE_SELECTOR = `[${FIGURE_ATTRIBUTE}]`;

/**
 * Marks a real SURFACE on the overlay — today only the terminal bubble.
 *
 * A Proc is scenery you can poke: it takes the pointer per painted pixel, and the
 * transparent gaps inside its own box fall through to the desktop. A surface is
 * the opposite: it is a card, it is solid, and every pixel of it belongs to it —
 * including the gaps between its own controls, which the desktop must not receive
 * clicks through while a terminal is sitting there.
 */
export const SURFACE_ATTRIBUTE = "data-companion-interactive";

const SURFACE_SELECTOR = `[${SURFACE_ATTRIBUTE}]`;

/** True only when an event landed on a Proc's own pixels. */
export function isOverPet(target: EventTarget | null): boolean {
	if (!target || !(target instanceof Element)) return false;
	return target.closest(FIGURE_SELECTOR) !== null;
}

/** True when an event landed on an open surface (the terminal bubble). */
export function isOverSurface(target: EventTarget | null): boolean {
	if (!target || !(target instanceof Element)) return false;
	return target.closest(SURFACE_SELECTOR) !== null;
}

/** Either of the two things on the overlay that own the pointer. */
export function ownsPointer(target: EventTarget | null): boolean {
	return isOverPet(target) || isOverSurface(target);
}

export type InteractionTracker = {
	/** Feed it the target of a pointer event. */
	update(target: EventTarget | null): void;
	/** The pointer left the window, or the window lost focus. */
	release(): void;
	/** A Proc is being held: keep the pointer whatever it is over. */
	hold(held: boolean): void;
};

/**
 * Tracks whether the overlay should be taking mouse events, and reports only when
 * the answer CHANGES — every pointer move crosses IPC otherwise.
 *
 * Starts click-through, so the desktop under the band is never dead in the window
 * between the overlay opening and the first pointer move arriving.
 */
export function createInteractionTracker(onChange: (interactive: boolean) => void): InteractionTracker {
	let interactive = false;
	let held = false;

	const set = (next: boolean) => {
		if (next === interactive) return;
		interactive = next;
		onChange(next);
	};

	return {
		update(target) {
			set(held || ownsPointer(target));
		},
		release() {
			if (held) return;
			set(false);
		},
		hold(next) {
			held = next;
			if (next) set(true);
		},
	};
}
