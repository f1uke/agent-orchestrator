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
import type { SessionKind } from "./live-roster";
import { modeFor, type CompanionMode } from "./mode";
import { sceneAnimates } from "./scene";

/** A stroll is 3-8 seconds long… */
export const WALK_MIN_MS = 3_000;
export const WALK_MAX_MS = 8_000;
/** …covers 60-260px… */
export const WALK_MIN_PX = 60;
export const WALK_MAX_PX = 260;
/**
 * …and is followed by 15-60s of standing still.
 *
 * Was 45-150s. On a real desktop that read as a still picture: watch the band for
 * a minute and quite possibly nothing moves at all, which makes the whole thing
 * look broken rather than calm. Shortened on the human's ask (2026-07-22).
 *
 * This does NOT make more of the cast mobile. Which Procs may walk is structural —
 * a Proc at a desk or in a bed is at a PLACE and cannot wander, whatever this
 * number says — and {@link MAX_CONCURRENT_WALKS} still holds the screen to two
 * movers. All this changes is how long a free Proc waits between strolls.
 */
export const REST_MIN_MS = 15_000;
export const REST_MAX_MS = 60_000;

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
	/** Whether this session coordinates the others. Marked on its label, not its art. */
	kind: SessionKind;
	/** Logical position along the floor band, in px from the band's left edge. */
	x: number;
	facing: Facing;
	motion: PetMotion;
	/** Earliest time this Proc may consider a stroll. Randomised per Proc. */
	restUntil: number;
	/**
	 * The front-of-band spot this Proc has ALREADY answered a summon to, if any.
	 *
	 * "It walks to the front once" was originally left implicit — it arrives at the
	 * spot, so the next tick sees it already there and has nothing to do. That
	 * premise is false: the crowding pass moves standing Procs, so the arrival is
	 * undone a tick later and the Proc sets off for the same spot again, for ever.
	 * That is the "walks in place and never arrives" bug.
	 *
	 * This is not a second copy of the position — it is WHICH summon has been
	 * answered. A later alert re-lays the rank out, the target changes, and the Proc
	 * correctly goes again; a nudge from a neighbour does not.
	 */
	summonedTo?: number;
	/**
	 * The human put this Proc here, by hand.
	 *
	 * Which means the crowding pass does not get a vote on it: it is neither moved
	 * nor treated as something to move away FROM. Dropping a Proc used to overrule
	 * the drop point and cascade a third Proc across the band, and an app that
	 * rearranges the desktop in answer to a deliberate gesture is fighting the
	 * person doing it. Overlap is the accepted cost, chosen explicitly — you can
	 * see exactly what you stacked, because you stacked it.
	 *
	 * Cleared the moment it walks somewhere under its own steam: it is then
	 * standing where the ENGINE put it, and the crowding rules own that spot again.
	 */
	placed?: boolean;
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
 * The nearest x to `from` that is clear of everyone else, or `from` itself when
 * the band has no room left. Searched outward in both directions so an arriving
 * Proc steps the SHORTEST way off whoever is already standing there.
 */
function nearestFreeX(from: number, pet: Pet, world: World): number {
	if (isFree(from, world.pets, world.spacing, pet.id)) return from;
	const { minX, maxX } = world.band;
	const step = Math.max(8, world.spacing / 8);
	for (let offset = step; offset <= maxX - minX; offset += step) {
		for (const candidate of [from - offset, from + offset]) {
			if (candidate < minX || candidate > maxX) continue;
			if (isFree(candidate, world.pets, world.spacing, pet.id)) return candidate;
		}
	}
	// Nowhere left. Overlapping is then the honest outcome, and the human chose it
	// over a band that reshuffles itself.
	return Math.min(maxX, Math.max(minX, from));
}

/**
 * Bring a Proc back inside the band.
 *
 * This is the ONLY thing that moves a Proc which is standing still, and it is a
 * rescue rather than a rearrangement: a Proc off the band is a session you cannot
 * see at all, which is a different order of problem from two standing close.
 */
function clampToBand(pet: Pet, band: Band): Pet {
	if (pet.motion.kind !== "standing") return pet;
	const x = Math.min(band.maxX, Math.max(band.minX, pet.x));
	return x === pet.x ? pet : { ...pet, x };
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
				kind: activity.kind ?? "worker",
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
		const renamed =
			(activity.name ?? "") !== prev.name ||
			(activity.project ?? "") !== prev.project ||
			(activity.kind ?? "worker") !== prev.kind;
		if (prev.status === activity.status && !renamed) {
			placed.push(prev);
			return prev;
		}
		const next = {
			...prev,
			status: activity.status,
			name: activity.name ?? prev.name,
			project: activity.project ?? prev.project,
			kind: activity.kind ?? prev.kind,
			// Leaving needs_input clears the answered-summon mark, so a session that
			// asks again later is walked to the front again rather than staying put.
			summonedTo: modeFor(activity.status) === "summon" ? prev.summonedTo : undefined,
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
				return { ...pet, placed: true, motion: { kind: "standing" }, restUntil: pickRest(now, rng) };
			}
			// Dropped off the end: the landing spot is the ENGINE's choice, not the
			// human's, so it does not earn the hand-placed exemption.
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

	// 2. Rescue anyone left off the band (a display resize). Deliberately NOT a
	//    crowding sweep: re-flowing the row on every tick meant one Proc arriving
	//    from a stroll slid every other Proc on the desktop, which is the surprise
	//    motion this engine exists to avoid. Crowding is settled by whoever turns
	//    up, at the moment they turn up — see `settle` and `placeNewPet`.
	pets = pets.map((pet) => clampToBand(pet, world.band));

	// 3. Consider new strolls, oldest-rested first so nobody is starved by the cap.
	let slots = walkSlots({ ...world, pets });
	const order = [...pets].sort((a, b) => a.restUntil - b.restUntil);
	const started = new Map<string, Pet>();
	// Each decision sees the ones already taken THIS tick — any destination just
	// claimed, and any rescue just applied. Deciding against `world.pets`
	// instead let two Procs pick the same empty spot in one pass and walk into each
	// other, which is a destination neither of them can actually stand on.
	let live = pets;
	for (const pet of order) {
		const next = consider(pet, now, { ...world, pets: live }, rng, slots > 0);
		if (next !== pet) {
			started.set(pet.id, next);
			live = live.map((entry) => (entry.id === pet.id ? next : entry));
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
	const summoned = modeFor(pet.status) === "summon";
	// The destination was clear when the walk was planned; if somebody has taken it
	// since, the ARRIVING Proc steps aside. Never the one already standing there.
	// A summoned Proc is exempt: its spot at the front is the alert.
	const landed = summoned ? pet.motion.toX : nearestFreeX(pet.motion.toX, pet, world);
	return {
		...pet,
		x: landed,
		// A summoned Proc turns to face you the moment it arrives — and this arrival
		// is what marks the alert answered, so a later nudge does not restart it.
		facing: summoned ? "front" : pet.facing,
		summonedTo: summoned ? landed : pet.summonedTo,
		// Wherever it has arrived, the ENGINE chose it — so any hand placement it was
		// carrying is spent, and the crowding rules own this spot.
		placed: false,
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
	// Already answered THIS summon. It came to the front, and the crowding pass has
	// since nudged it a little — that is not a reason to walk to the front again,
	// and treating it as one is an endless walk that never arrives.
	if (pet.summonedTo === target) return pet;
	// Reduced motion keeps the meaning (it IS at the front, facing you) and drops
	// only the travel: a static equivalent, not a silently missing state.
	// Standing at the spot already is the same terminal state, reached by walking.
	if (world.reducedMotion || world.parked || Math.abs(target - pet.x) < 1) {
		return { ...pet, x: target, facing: "front", motion: { kind: "standing" }, summonedTo: target };
	}
	// `summonedTo` is recorded on ARRIVAL, not here: a walk cut short by parking must
	// still count as unanswered, or the Proc would silently give up on the alert.
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
	const offset = (slot - (rank.length - 1) / 2) * summonPitch(rank.length, world);
	return Math.min(world.band.maxX, Math.max(world.band.minX, centre + offset));
}

/**
 * Slot width for the summon rank.
 *
 * At least {@link SUMMON_SPACING_PX}, but never tighter than the standing
 * clearance, so a rank of alerts arrives already clear of each other rather than
 * landing on top of one another at the front. Squeezed to fit the band when the
 * cohort is large, so a big rank stays on screen instead of stacking at the edge.
 */
function summonPitch(rankSize: number, world: World): number {
	const wanted = Math.max(SUMMON_SPACING_PX, world.spacing);
	if (rankSize < 2) return wanted;
	return Math.min(wanted, (world.band.maxX - world.band.minX) / (rankSize - 1));
}
