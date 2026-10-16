import type { Transition } from "motion/react";
import { boardKeysParam, LAYER_ORDER, type Layers, type ReleasesSearch } from "./url";

/** Motion's own values only: a raw `transform` with a 'none' keyframe collapsed to scaleX(0) in d3. */
export type MotionValues = Partial<
	Record<"x" | "y" | "scale" | "scaleX" | "scaleY" | "opacity", number | number[]> & { height: number | "auto" }
>;

export type Presence = {
	initial: MotionValues | false;
	animate: MotionValues;
	exit: MotionValues;
	transition: Transition;
	exitTransition?: Transition;
};

export type ReleaseMotion = {
	reduced: boolean;
	unfold: Presence;
	appear: Presence;
	sheet: Presence;
	label: (i: number) => Presence;
	fold: Transition;
	row: Transition;
	axis: Transition;
	momentum: Transition;
};

export const DURATION = { micro: 0.08, short: 0.16, medium: 0.24 } as const;

const ease = {
	enter: [0, 0, 0.2, 1],
	exit: [0.4, 0, 1, 1],
	move: [0.4, 0, 0.2, 1],
	coast: [0.1, 0.7, 0.25, 1],
} as const;

const LABEL_STEP = 0.012;
const LABEL_MAX_DELAY = DURATION.medium - DURATION.short;

const moving: Transition = { duration: DURATION.medium, ease: ease.move };
const instant: Transition = { duration: 0 };

const MOVING: ReleaseMotion = {
	reduced: false,
	unfold: {
		initial: { height: 0, opacity: 0 },
		animate: { height: "auto", opacity: 1 },
		exit: { height: 0, opacity: 0 },
		transition: moving,
	},
	appear: {
		initial: { opacity: 0 },
		animate: { opacity: 1 },
		exit: { opacity: 0 },
		transition: { duration: DURATION.short, ease: ease.enter },
	},
	sheet: {
		initial: { opacity: 0, x: 16 },
		animate: { opacity: 1, x: 0 },
		exit: { opacity: 0, x: 14 },
		transition: { duration: DURATION.medium, ease: ease.enter },
		exitTransition: { duration: DURATION.short, ease: ease.exit },
	},
	label: (i) => ({
		initial: { opacity: 0 },
		animate: { opacity: 1 },
		exit: { opacity: 0 },
		transition: { duration: DURATION.short, ease: ease.enter, delay: Math.min(i * LABEL_STEP, LABEL_MAX_DELAY) },
	}),
	fold: moving,
	row: moving,
	axis: moving,
	momentum: { duration: DURATION.medium, ease: ease.coast },
};

function still(p: Presence): Presence {
	return { initial: false, animate: p.animate, exit: p.exit, transition: instant };
}

const STILL: ReleaseMotion = {
	reduced: true,
	unfold: still(MOVING.unfold),
	appear: still(MOVING.appear),
	sheet: { initial: false, animate: { opacity: 1 }, exit: { opacity: 0 }, transition: instant },
	label: () => still(MOVING.label(0)),
	fold: instant,
	row: instant,
	axis: instant,
	momentum: instant,
};

export function enterKeyframes(p: Presence): Record<string, (number | string)[]> | null {
	const from = p.initial;
	if (!from) return null;
	const out: Record<string, (number | string)[]> = {};
	for (const [k, to] of Object.entries(p.animate)) {
		const start = from[k as keyof MotionValues];
		if (start !== undefined && to !== undefined) out[k] = [start, to].flat();
	}
	return out;
}

export function releaseMotion(reduced: boolean): ReleaseMotion {
	return reduced ? STILL : MOVING;
}

export type MoveKeys = { rows: string; blocks: string };

export function moveKeys(
	url: ReleasesSearch,
	layers: Layers,
	folds: ReadonlySet<string>,
	hidden: ReadonlySet<string>,
): MoveKeys {
	const rows = [
		url.apps ? [...url.apps].sort().join(",") : "*",
		url.me ? "me" : "",
		url.hotfixes ? "hf" : "",
		LAYER_ORDER.filter((k) => layers[k]).join(","),
		boardKeysParam(hidden),
	].join("|");
	return { rows, blocks: `${rows}|${[...folds].sort().join(",")}` };
}

export const PAN_MOMENTUM_MS = 260;
export const PAN_MOMENTUM_MIN_VELOCITY = 0.15;

export function momentumDistance(velocityPxPerMs: number): number {
	return Math.abs(velocityPxPerMs) < PAN_MOMENTUM_MIN_VELOCITY ? 0 : velocityPxPerMs * PAN_MOMENTUM_MS;
}
