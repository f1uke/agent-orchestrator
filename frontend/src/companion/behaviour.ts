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

/**
 * Default centre-to-centre clearance between two Procs standing on the band. The
 * renderer overrides it with the real drawn width; this is the fallback for tests
 * and for a world built before anything has been measured.
 */
export const DEFAULT_SPACING_PX = 100;

export type Facing = "front" | "left" | "right";

export type PetMotion =
	| { kind: "standing" }
	| { kind: "walking"; fromX: number; toX: number; startedAt: number; endsAt: number }
	/** Picked up by the human. Follows the pointer and does nothing of its own. */
	| { kind: "held"; grabbedAt: number };

export type Pet = {
	id: string;
	status: SessionStatus;
	/** The session's board name and project, straight from the feed. */
	name: string;
	project: string;
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
	/**
	 * Minimum centre-to-centre distance between two Procs that are standing still.
	 * Parked Procs stacking on one spot is worse than a clump: you cannot see that
	 * there are two, so a session silently vanishes behind another.
	 */
	spacing: number;
	/** `prefers-reduced-motion`: Procs stand at their positions, nothing walks. */
	reducedMotion: boolean;
	/** Occluded, another app is fullscreen, or the display slept: hold everything. */
	parked: boolean;
};

export type Rng = () => number;

export function createWorld(band: Band): World {
	return { pets: [], band, spacing: DEFAULT_SPACING_PX, reducedMotion: false, parked: false };
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
	return world.pets.filter((pet) => pet.motion.kind !== "standing" || sceneAnimates(pet.status)).length;
}

/** How many more Procs may start a stroll right now. Never negative. */
export function walkSlots(world: World): number {
	return Math.max(0, Math.min(MAX_CONCURRENT_WALKS - walkingCount(world), MAX_ANIMATING - animatingCount(world)));
}

/**
 * Where a Proc occupies the band for crowding purposes: a walker is heading for its
 * destination, so that — not the spot it has already left — is the space it claims.
 */
function claimedX(pet: Pet): number {
	return pet.motion.kind === "walking" ? pet.motion.toX : pet.x;
}

/** True when `x` is clear of every Proc except `exceptId`. */
function isFree(x: number, pets: Pet[], spacing: number, exceptId?: string): boolean {
	return pets.every((pet) => pet.id === exceptId || Math.abs(claimedX(pet) - x) >= spacing);
}

/**
 * A spot for a Proc that has just appeared. Tries random places first so the cast
 * does not line up in spawn order, then falls back to the middle of the widest gap
 * — which always exists and is always the least bad answer.
 */
function placeNewPet(world: World, pets: Pet[], rng: Rng): number {
	const { minX, maxX } = world.band;
	const span = Math.max(0, maxX - minX);
	for (let attempt = 0; attempt < 12; attempt++) {
		const candidate = minX + rng() * span;
		if (isFree(candidate, pets, world.spacing)) return candidate;
	}
	const occupied = pets.map(claimedX).sort((a, b) => a - b);
	let best = (minX + maxX) / 2;
	let widest = -1;
	const edges = [minX, ...occupied, maxX];
	for (let i = 1; i < edges.length; i++) {
		const gap = edges[i] - edges[i - 1];
		if (gap > widest) {
			widest = gap;
			best = (edges[i] + edges[i - 1]) / 2;
		}
	}
	return Math.min(maxX, Math.max(minX, best));
}

/**
 * Push standing Procs apart until none is closer than `spacing` to its neighbour.
 *
 * A left-to-right sweep, then a right-to-left one to pull the tail back inside the
 * band. Walkers are left alone — they are already going somewhere — but they still
 * count as obstacles, so nobody is separated into the path of an incoming stroll.
 * When the band cannot fit everyone the shortfall is shared equally instead of
 * letting the overflow stack: ten Procs squeezed together still shows ten Procs.
 */
function separate(pets: Pet[], band: Band, spacing: number): Pet[] {
	// Walkers are already going somewhere and a held Proc is under the user's
	// pointer — moving either would be the app fighting for control of it.
	const standing = pets.filter((pet) => pet.motion.kind === "standing");
	if (standing.length < 2) return pets;

	const order = [...standing].sort((a, b) => a.x - b.x);
	const needed = (order.length - 1) * spacing;
	const room = band.maxX - band.minX;
	const step = needed <= room ? spacing : room / (order.length - 1);

	const moved = new Map<string, number>();
	let previous = Number.NEGATIVE_INFINITY;
	for (const pet of order) {
		const x = Math.max(pet.x, previous + step);
		moved.set(pet.id, x);
		previous = x;
	}
	// The forward sweep can push the last one past the edge; walk back to fix it.
	let limit = band.maxX;
	for (let i = order.length - 1; i >= 0; i--) {
		const pet = order[i];
		const x = Math.min(moved.get(pet.id) ?? pet.x, limit);
		moved.set(pet.id, Math.max(band.minX, x));
		limit = (moved.get(pet.id) ?? pet.x) - step;
	}

	return pets.map((pet) => {
		const x = moved.get(pet.id);
		return x === undefined || x === pet.x ? pet : { ...pet, x };
	});
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
function planWalk(
	x: number,
	band: Band,
	rng: Rng,
	isClear: (toX: number) => boolean,
): { toX: number; durationMs: number } | null {
	const durationMs = WALK_MIN_MS + rng() * (WALK_MAX_MS - WALK_MIN_MS);
	const distance = WALK_MIN_PX + rng() * (WALK_MAX_PX - WALK_MIN_PX);
	const preferred = rng() < 0.5 ? -1 : 1;

	// Try the direction the dice chose, then the other one. A stroll that would end
	// on top of a Proc already standing there is not a stroll worth taking — and
	// turning round is the same move the band edge already asks for.
	for (const direction of [preferred, -preferred]) {
		const raw = x + direction * distance;
		if (raw < band.minX || raw > band.maxX) continue;
		if (!isClear(raw)) continue;
		if (Math.abs(raw - x) < 1) continue;
		return { toX: raw, durationMs };
	}
	return null;
}

function facingFor(fromX: number, toX: number): Facing {
	return toX < fromX ? "left" : "right";
}

/** True for the modes that can travel: everything else stands facing the human. */
function canWander(status: SessionStatus): boolean {
	const mode = modeFor(status);
	return mode === "amble" || mode === "summon";
}

/**
 * Reconcile the cast against the activity source: add Procs for new sessions, drop
 * Procs whose session is gone, and carry position/timers through a status change so
 * a Proc never teleports because its PR moved on.
 */
export function syncActivities(world: World, activities: CompanionActivity[], now: number, rng: Rng): World {
	const existing = new Map(world.pets.map((pet) => [pet.id, pet]));
	// Grows as the roster is walked, so two Procs appearing in the same snapshot are
	// placed clear of each other and not just of the ones already on screen.
	const placed: Pet[] = [];
	const pets = activities.map((activity) => {
		const prev = existing.get(activity.sessionId);
		if (!prev) {
			const born: Pet = {
				id: activity.sessionId,
				status: activity.status,
				name: activity.name ?? "",
				project: activity.project ?? "",
				// Placed clear of everyone already standing there, so a Proc that joins
				// the desktop never lands on top of one that is already on it.
				x: placeNewPet(world, placed, rng),
				facing: "front",
				motion: { kind: "standing" },
				restUntil: pickRest(now, rng),
			};
			placed.push(born);
			return born;
		}
		const renamed = (activity.name ?? "") !== prev.name || (activity.project ?? "") !== prev.project;
		if (prev.status === activity.status && !renamed) {
			placed.push(prev);
			return prev;
		}
		const next = {
			...prev,
			status: activity.status,
			name: activity.name ?? prev.name,
			project: activity.project ?? prev.project,
			// A Proc that cannot stroll faces YOU. `facing` otherwise persists from the
			// last stroll, and the whole sprite mirrors — scenery and all — so a Proc
			// that walked left and then sat down at a desk would show the desk flipped
			// to its other side, reading as the furniture teleporting.
			facing: canWander(activity.status) ? prev.facing : ("front" as Facing),
		};
		placed.push(next);
		return next;
	});
	return { ...world, pets };
}

/**
 * Pick a Proc up. Only one can be held at a time — there is only one pointer — so
 * this puts down whatever was already in hand.
 */
export function grabPet(world: World, petId: string, now: number): World {
	return {
		...world,
		pets: world.pets.map((pet) => {
			if (pet.id === petId) return { ...pet, facing: "front", motion: { kind: "held", grabbedAt: now } };
			return pet.motion.kind === "held" ? { ...pet, motion: { kind: "standing" } } : pet;
		}),
	};
}

/**
 * Move the held Proc to follow the pointer. Deliberately NOT clamped to the band:
 * a drag that stops dead at the edge feels like the app grabbing back, so it goes
 * where the pointer goes and is brought home on release.
 */
export function dragPet(world: World, petId: string, x: number): World {
	return {
		...world,
		pets: world.pets.map((pet) => (pet.id === petId && pet.motion.kind === "held" ? { ...pet, x } : pet)),
	};
}

/**
 * Let go. Inside the band it simply stands where it was dropped; beyond it, it
 * WALKS back on rather than snapping, so the user can see where it went. Either
 * way the next tick's separation pass keeps it off anybody already standing there.
 */
export function releasePet(world: World, petId: string, now: number, rng: Rng): World {
	return {
		...world,
		pets: world.pets.map((pet) => {
			if (pet.id !== petId || pet.motion.kind !== "held") return pet;
			const landed = Math.min(world.band.maxX, Math.max(world.band.minX, pet.x));
			if (landed === pet.x) {
				return { ...pet, motion: { kind: "standing" }, restUntil: pickRest(now, rng) };
			}
			// Dropped off the end: walk in from the edge to a spot clearly on the band.
			const inward = landed === world.band.minX ? 1 : -1;
			const toX = Math.min(
				world.band.maxX,
				Math.max(world.band.minX, landed + inward * Math.min(WALK_MIN_PX, world.band.maxX - world.band.minX)),
			);
			return {
				...pet,
				x: landed,
				facing: facingFor(landed, toX),
				motion: { kind: "walking", fromX: landed, toX, startedAt: now, endsAt: now + WALK_MIN_MS },
				restUntil: pickRest(now, rng),
			};
		}),
	};
}

/** Advance the world to `now`. Pure: same inputs, same output. */
export function tick(world: World, now: number, rng: Rng): World {
	// 1. Settle every stroll that has finished (or that parking cut short).
	let pets = world.pets.map((pet) => settle(pet, now, world, rng));

	// 2. Push apart anyone standing on top of anyone else. Idempotent once resolved,
	//    so a settled cast is not nudged on every tick.
	pets = separate(pets, world.band, world.spacing);

	// 3. Consider new strolls, oldest-rested first so nobody is starved by the cap.
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
	// A Proc in the user's hand does nothing of its own until it is let go.
	if (pet.motion.kind !== "standing") return pet;
	if (world.parked) return pet;

	const mode: CompanionMode = modeFor(pet.status);
	if (mode === "summon") return summon(pet, now, world);
	// anchor and still both stay put; only amble strolls, and only on its own clock.
	if (mode !== "amble") return pet;
	if (world.reducedMotion) return pet;
	if (!hasSlot || now < pet.restUntil) return pet;

	const plan = planWalk(pet.x, world.band, rng, (toX) => isFree(toX, world.pets, world.spacing, pet.id));
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
