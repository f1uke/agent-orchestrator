// The behaviour engine: pure logic, no DOM, no timers, no Electron.
//
// Everything here is a function of (world, now, rng) → world, which is what makes
// the whole locomotion system testable without a screen. The renderer owns only
// the frame: it ticks this on a slow interval and lets CSS interpolate the
// composited `transform` between the positions the engine hands it.
//
// The rules the design fixed, and where each lives:
//   - standing still is the DEFAULT; a stroll is a rare short event  → restUntil
//   - a Proc at a place cannot wander                                → scene.ts / mode.ts
//   - rests are randomised per Proc so the cast never marches         → pickRest
//   - at most two strolling at once (~8 animating is the backstop)    → walkSlots
//   - a stroll never half-exits the band                              → planWalk
//   - needs_input walks to the front ONCE and then faces you          → summon

import type { SessionStatus } from "../renderer/types/workspace";
import type { CompanionActivity } from "./feed";
import { modeFor, type CompanionMode } from "./mode";
import { sceneAnimates } from "./scene";

/** A stroll is 3-8 seconds long… */
export const WALK_MIN_MS = 3_000;
export const WALK_MAX_MS = 8_000;
/** …covers 60-260px… */
export const WALK_MIN_PX = 60;
export const WALK_MAX_PX = 260;
/** …and is followed by 45-150s of standing still. */
export const REST_MIN_MS = 45_000;
export const REST_MAX_MS = 150_000;

/** Synchronised strolling reads as a screensaver, so only two Procs may move at once. */
export const MAX_CONCURRENT_WALKS = 2;
/** Total animating Procs. A backstop above the walker cap, not the binding constraint. */
export const MAX_ANIMATING = 8;

/** The walk cycle is four beats driven by `steps(4, end)` in the renderer. */
export const WALK_CYCLE_STEPS = 4;
export const WALK_CYCLE_MS = 520;

/** Gap between two Procs summoned at the same time, so they never stack up. */
export const SUMMON_SPACING_PX = 96;

export type Facing = "front" | "left" | "right";

export type PetMotion =
	{ kind: "standing" } | { kind: "walking"; fromX: number; toX: number; startedAt: number; endsAt: number };

export type Pet = {
	id: string;
	status: SessionStatus;
	/** Logical position along the floor band, in px from the band's left edge. */
	x: number;
	facing: Facing;
	motion: PetMotion;
	/** Earliest time this Proc may consider a stroll. Randomised per Proc. */
	restUntil: number;
};

/** The floor band the cast lives on: one line above the Dock, inset from both edges. */
export type Band = { minX: number; maxX: number };

export type World = {
	pets: Pet[];
	band: Band;
	/** `prefers-reduced-motion`: Procs stand at their positions, nothing walks. */
	reducedMotion: boolean;
	/** Occluded, another app is fullscreen, or the display slept: hold everything. */
	parked: boolean;
};

export type Rng = () => number;

export function createWorld(band: Band): World {
	return { pets: [], band, reducedMotion: false, parked: false };
}

export function walkingCount(world: World): number {
	return world.pets.filter((pet) => pet.motion.kind === "walking").length;
}

/**
 * Procs currently running an animation: a walker, or a Proc whose SCENE moves
 * (sparks, zzz, confetti, a streaming or tugging cord). With the full art most of
 * the motion on a desktop is scenes rather than walkers, which is what makes the
 * {@link MAX_ANIMATING} backstop bite — a screen full of failing CI stops the
 * strolling instead of adding to it.
 */
export function animatingCount(world: World): number {
	return world.pets.filter((pet) => pet.motion.kind === "walking" || sceneAnimates(pet.status)).length;
}

/** How many more Procs may start a stroll right now. Never negative. */
export function walkSlots(world: World): number {
	return Math.max(0, Math.min(MAX_CONCURRENT_WALKS - walkingCount(world), MAX_ANIMATING - animatingCount(world)));
}

function pickRest(now: number, rng: Rng): number {
	return now + REST_MIN_MS + rng() * (REST_MAX_MS - REST_MIN_MS);
}

/**
 * Where the next stroll goes. Draws duration, distance and direction (always three
 * rng reads, whatever the outcome, so a caller can reason about the sequence), then
 * turns around rather than let a Proc half-exit the band. Returns null when the band
 * is too tight to move in at all.
 */
function planWalk(x: number, band: Band, rng: Rng): { toX: number; durationMs: number } | null {
	const durationMs = WALK_MIN_MS + rng() * (WALK_MAX_MS - WALK_MIN_MS);
	const distance = WALK_MIN_PX + rng() * (WALK_MAX_PX - WALK_MIN_PX);
	let direction = rng() < 0.5 ? -1 : 1;

	let toX = x + direction * distance;
	if (toX < band.minX || toX > band.maxX) {
		direction = -direction;
		toX = x + direction * distance;
	}
	toX = Math.min(band.maxX, Math.max(band.minX, toX));
	if (Math.abs(toX - x) < 1) return null;
	return { toX, durationMs };
}

function facingFor(fromX: number, toX: number): Facing {
	return toX < fromX ? "left" : "right";
}

/**
 * Reconcile the cast against the activity source: add Procs for new sessions, drop
 * Procs whose session is gone, and carry position/timers through a status change so
 * a Proc never teleports because its PR moved on.
 */
export function syncActivities(world: World, activities: CompanionActivity[], now: number, rng: Rng): World {
	const existing = new Map(world.pets.map((pet) => [pet.id, pet]));
	const pets = activities.map((activity) => {
		const prev = existing.get(activity.sessionId);
		if (!prev) {
			return {
				id: activity.sessionId,
				status: activity.status,
				// Spread the cast across the band instead of stacking it on one spot.
				x: world.band.minX + rng() * Math.max(0, world.band.maxX - world.band.minX),
				facing: "front" as Facing,
				motion: { kind: "standing" as const },
				restUntil: pickRest(now, rng),
			};
		}
		if (prev.status === activity.status) return prev;
		return { ...prev, status: activity.status };
	});
	return { ...world, pets };
}

/** Advance the world to `now`. Pure: same inputs, same output. */
export function tick(world: World, now: number, rng: Rng): World {
	// 1. Settle every stroll that has finished (or that parking cut short).
	let pets = world.pets.map((pet) => settle(pet, now, world, rng));

	// 2. Consider new strolls, oldest-rested first so nobody is starved by the cap.
	let slots = walkSlots({ ...world, pets });
	const order = [...pets].sort((a, b) => a.restUntil - b.restUntil);
	const started = new Map<string, Pet>();
	for (const pet of order) {
		const next = consider(pet, now, world, rng, slots > 0);
		if (next !== pet) {
			started.set(pet.id, next);
			if (next.motion.kind === "walking") slots -= 1;
		}
	}
	if (started.size > 0) pets = pets.map((pet) => started.get(pet.id) ?? pet);

	return { ...world, pets };
}

// settle finishes a stroll: at the target, standing, with a fresh randomised rest.
// Parking settles immediately rather than freezing a Proc mid-stride.
function settle(pet: Pet, now: number, world: World, rng: Rng): Pet {
	if (pet.motion.kind !== "walking") return pet;
	if (!world.parked && now < pet.motion.endsAt) return pet;
	return {
		...pet,
		x: pet.motion.toX,
		// A summoned Proc turns to face you the moment it arrives.
		facing: modeFor(pet.status) === "summon" ? "front" : pet.facing,
		motion: { kind: "standing" },
		restUntil: pickRest(now, rng),
	};
}

// consider decides whether a standing Proc starts moving on this tick.
function consider(pet: Pet, now: number, world: World, rng: Rng, hasSlot: boolean): Pet {
	if (pet.motion.kind === "walking") return pet;
	if (world.parked) return pet;

	const mode: CompanionMode = modeFor(pet.status);
	if (mode === "summon") return summon(pet, now, world);
	// anchor and still both stay put; only amble strolls, and only on its own clock.
	if (mode !== "amble") return pet;
	if (world.reducedMotion) return pet;
	if (!hasSlot || now < pet.restUntil) return pet;

	const plan = planWalk(pet.x, world.band, rng);
	if (!plan) return { ...pet, restUntil: pickRest(now, rng) };
	return {
		...pet,
		facing: facingFor(pet.x, plan.toX),
		motion: { kind: "walking", fromX: pet.x, toX: plan.toX, startedAt: now, endsAt: now + plan.durationMs },
	};
}

// summon walks a needs_input Proc to the front and leaves it facing the human.
// Exempt from the rest timer and from the walker cap: it is the one motion that is
// an alert rather than ambience, and a Proc you have to hunt for is a failed alert.
//
// "Once" needs no bookkeeping flag: the Proc walks to its summon spot and then IS
// at its summon spot, so the next tick has nothing to do. A flag saying it already
// arrived would only be a second, driftable copy of that fact.
function summon(pet: Pet, now: number, world: World): Pet {
	const target = summonTargetX(pet, world);
	// Reduced motion keeps the meaning (it IS at the front, facing you) and drops
	// only the travel: a static equivalent, not a silently missing state.
	// Standing at the spot already is the same terminal state, reached by walking.
	if (world.reducedMotion || world.parked || Math.abs(target - pet.x) < 1) {
		return { ...pet, x: target, facing: "front", motion: { kind: "standing" } };
	}
	return {
		...pet,
		facing: facingFor(pet.x, target),
		motion: {
			kind: "walking",
			fromX: pet.x,
			toX: target,
			startedAt: now,
			endsAt: now + summonDuration(Math.abs(target - pet.x)),
		},
	};
}

// A summon covers whatever distance it must at the same pace a stroll does, so a
// far-away Proc does not sprint across the screen and a nearby one does not crawl.
function summonDuration(distance: number): number {
	const pace = (WALK_MIN_PX + WALK_MAX_PX) / (WALK_MIN_MS + WALK_MAX_MS);
	return Math.max(1_000, Math.min(WALK_MAX_MS * 2, distance / pace));
}

/**
 * Where a summoned Proc stands: the summon rank is laid out around the centre of
 * the band, one {@link SUMMON_SPACING_PX} slot each, ordered by session id so the
 * layout is stable across ticks and identical for every Proc computing it. A Proc
 * that is already standing in the rank shuffles over to make room for a late
 * alert rather than letting the two overlap.
 */
export function summonTargetX(pet: Pet, world: World): number {
	const rank = world.pets
		.filter((p) => modeFor(p.status) === "summon")
		.map((p) => p.id)
		.sort();
	const slot = Math.max(0, rank.indexOf(pet.id));
	const centre = (world.band.minX + world.band.maxX) / 2;
	const offset = (slot - (rank.length - 1) / 2) * SUMMON_SPACING_PX;
	return Math.min(world.band.maxX, Math.max(world.band.minX, centre + offset));
}
