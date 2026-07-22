import { PROJECT_MARKER_COLOURS } from "./palette";

// A small mark that says which PROJECT a Proc belongs to.
//
// The human looked at a full overlay and could not tell which pet belonged to
// which project — the look is assigned per SESSION, so it carries no project
// signal at all, and the only project information on screen was inside a hover
// card you have to ask for. This puts it on the label, where it can be read at a
// glance and without reading.
//
// It is a PROJECT signal and it lives next to the gold crown, which is a ROLE
// signal. Two different questions, so two different marks: the crown says "this
// one coordinates", the marker says "this one is on that project". They have to
// sit together without being mistaken for each other, which is why the marker is
// never gold and never a crown.
//
// SHAPE as well as colour, deliberately. Colour alone is one channel — it fails
// on a greyscale capture, at 10px, and for anyone with a colour-vision difference
// — and shape survives all three. Two independent axes also give 6 x 6 marks
// instead of 6, which matters on a machine running several projects.

export type MarkerShape = "circle" | "square" | "triangle" | "diamond" | "pentagon" | "hexagon";

export type ProjectMarker = {
	/** `<shape>-<colour index>`. Stable for a project, distinct between projects. */
	id: string;
	shape: MarkerShape;
	fill: string;
};

export const MARKER_SHAPES: readonly MarkerShape[] = ["circle", "square", "triangle", "diamond", "pentagon", "hexagon"];

/** Salt for the colour's hash, so shape and colour are genuinely separate axes. */
const COLOUR_SALT = " colour";

/** The mark a project always gets. Pure, stable across restarts. */
export function markerForProject(project: string): ProjectMarker {
	const name = project.trim();
	const shape = MARKER_SHAPES[hash(name) % MARKER_SHAPES.length];
	const index = hash(name + COLOUR_SALT) % PROJECT_MARKER_COLOURS.length;
	return { id: `${shape}-${index}`, shape, fill: PROJECT_MARKER_COLOURS[index] };
}

/**
 * The marker's outline as an SVG path, in a 12x12 box.
 *
 * Drawn as paths rather than as `circle`/`rect` elements so every shape takes the
 * same ink rim the same way, and so a seventh shape is a row here.
 */
export function markerPath(shape: MarkerShape): string {
	switch (shape) {
		case "circle":
			return "M6 1 A5 5 0 1 1 5.99 1 Z";
		case "square":
			return "M1.6 1.6 L10.4 1.6 L10.4 10.4 L1.6 10.4 Z";
		case "triangle":
			return "M6 1 L11 10.4 L1 10.4 Z";
		case "diamond":
			return "M6 0.8 L11.2 6 L6 11.2 L0.8 6 Z";
		case "pentagon":
			return "M6 1 L11 4.8 L9.1 10.6 L2.9 10.6 L1 4.8 Z";
		case "hexagon":
			return "M6 1 L10.5 3.6 L10.5 8.4 L6 11 L1.5 8.4 L1.5 3.6 Z";
	}
}

// FNV-1a with a murmur3 avalanche, the same construction the cast uses and for the
// same measured reason: `hash % 6` reads FNV's weakest bits, and project names
// sharing a prefix (`agent-orchestrator`, `agent-orchestrator-web`) are exactly
// the ones a person sees side by side.
function hash(value: string): number {
	let out = 0x811c9dc5;
	for (let i = 0; i < value.length; i++) {
		out ^= value.charCodeAt(i);
		out = Math.imul(out, 0x01000193);
	}
	out ^= out >>> 16;
	out = Math.imul(out, 0x85ebca6b);
	out ^= out >>> 13;
	out = Math.imul(out, 0xc2b2ae35);
	out ^= out >>> 16;
	return out >>> 0;
}
