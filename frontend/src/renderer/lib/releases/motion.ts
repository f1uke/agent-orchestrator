import type { Transition } from "motion/react";

/** Motion's own values only: a raw `transform` with a 'none' keyframe collapsed to scaleX(0) in d3. */
export type MotionValues = Partial<
	Record<"x" | "y" | "scale" | "scaleX" | "scaleY" | "opacity" | "height", number | number[]>
>;

export type Variant = { initial: MotionValues; animate: MotionValues; exit: MotionValues; transition: Transition };

const ease = { enter: [0, 0, 0.2, 1], exit: [0.4, 0, 1, 1] } as const;

export const DURATION = { micro: 0.08, short: 0.16, medium: 0.24 } as const;

export const sheetEnter: Variant = {
	initial: { opacity: 0, x: 16 },
	animate: { opacity: 1, x: 0 },
	exit: { opacity: 0, x: 14 },
	transition: { duration: DURATION.medium, ease: ease.enter },
};

export const sheetExit: Transition = { duration: DURATION.short, ease: ease.exit };

export const PAN_MOMENTUM_MS = 260;
export const PAN_MOMENTUM_MIN_VELOCITY = 0.15;

export function momentumDistance(velocityPxPerMs: number): number {
	return Math.abs(velocityPxPerMs) < PAN_MOMENTUM_MIN_VELOCITY ? 0 : velocityPxPerMs * PAN_MOMENTUM_MS;
}
