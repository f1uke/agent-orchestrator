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
/** The same four beats, stepped faster, for a Proc running to meet another one. */
export const RUN_CYCLE_MS = 260;

/**
 * Downward acceleration, in px per ms². About 2200 px/s²: a Proc dropped from
 * the top of a laptop display is back on the floor in a little over half a second,
 * which is fast enough to read as weight rather than as a feather.
 */
export const GRAVITY_PX_PER_MS2 = 0.0022;
/** How much of its speed a bounce keeps. Two or three visible hops, then still. */
export const BOUNCE_RESTITUTION = 0.42;
/** Below this landing speed a bounce is not worth drawing, so it settles instead. */
export const BOUNCE_MIN_SPEED = 0.28;
/** Sideways speed bleeds off in the air, and much faster once it is skidding. */
export const AIR_DRAG_PER_MS = 0.0006;
export const GROUND_DRAG_PER_MS = 0.008;
/** Below this it is no longer sliding, it is standing. */
export const SLIDE_MIN_SPEED = 0.02;
/** The landing speed that raises a full cloud. Anything faster raises the same. */
export const DUST_FULL_SPEED = 1.4;
/**
 * A flick can only throw a Proc so hard, however violently the pointer moved.
 *
 * 3.2 px/ms was the first number and it was far too much — a Proc left the hand
 * like something fired rather than something thrown. A pointer can cross a
 * display in a few frames, and matching that speed one-for-one is not a throw.
 */
export const MAX_THROW_SPEED = 1.5;
/**
 * How much of the hand's speed the Proc actually leaves with.
 *
 * Under one on purpose: a thrown thing does not keep all of the speed of the
 * hand that let it go, and reading the pointer literally made every flick feel
 * violent.
 */
export const THROW_SCALE = 0.5;

/** Gap between two Procs summoned at the same time, so they never stack up. */
export const SUMMON_SPACING_PX = 96;

/**
 * How far apart two Procs stand while they talk. Close enough to read as one
 * exchange, far enough that neither is standing on the other's face — and their
 * two bubbles need somewhere to go.
 */
export const MEET_GAP_PX = 108;
/** How long they stay together once they arrive. */
export const MEET_GREET_MS = 3_500;
/** A run is capped rather than paced: crossing the whole desktop must not take a minute. */
export const MEET_RUN_MAX_MS = 1_600;
export const MEET_RUN_MIN_MS = 400;
/**
 * One conversation at a time.
 *
 * A meeting is a dramatisation of something that happened at a MOMENT. Queueing
 * one to play out later would stage an event that is already over, which is the
 * same lie the bubble's TTL exists to prevent — so a second message still lands
 * in its Proc's bubble, and only the performance is skipped.
 */
export const MAX_CONCURRENT_MEETS = 1;

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
	| { kind: "held"; grabbedAt: number }
	/** Let go in mid-air or thrown. Falls, bounces, and comes to rest. */
	| { kind: "flying"; vx: number; vy: number };

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
	/**
	 * Height above the floor line, in px. 0 is standing on the band.
	 *
	 * The band was one horizontal line and a Proc had only an x — you could slide
	 * one along the floor and that was all. A Proc you can pick up and drop is a
	 * Proc that has to be able to be somewhere other than the floor.
	 */
	y: number;
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
	/** Set while this Proc is away meeting another one. See {@link startConversation}. */
	meeting?: Meeting;
	/**
	 * The last time this Proc hit the floor, and how hard.
	 *
	 * A Proc that lands and simply carries on reads as weightless — the puff of
	 * dust is the only thing that says it landed ON something. `seq` counts the
	 * landings rather than timestamping them, because that is what the renderer
	 * needs: the same number twice is the same landing, a new number is a new one
	 * and restarts the animation.
	 */
	bounce?: { seq: number; strength: number };
};

/**
 * One end of a conversation between two sessions.
 *
 * The only behaviour on this desktop that is about a RELATIONSHIP rather than
 * about one session, which is why it is the one time two Procs act together.
 */
export type Meeting = {
	/** The Proc at the other end. */
	withId: string;
	/** Where this one was standing when the message arrived. It goes back there. */
	homeX: number;
	/** What it says while they are together. Empty for the one being told. */
	line: string;
	phase: "approaching" | "greeting" | "returning";
	/** When `greeting` is over. Only the greeting is on a clock; the two runs end when they land. */
	until: number;
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
	// A snapshot is keyed by session id, so a repeated id is a BROKEN snapshot — and
	// the worst possible reading of it is two identical characters on one spot with
	// one name chip painted over the other. First reading wins, which is what a Map
	// built from the snapshot would give, and it is stable.
	const seen = new Set<string>();
	// Grows as the roster is walked, so two Procs appearing in the same snapshot are
	// placed clear of each other and not just of the ones already on screen.
	const placed: Pet[] = [];
	const pets = activities
		.filter((activity) => {
			if (seen.has(activity.sessionId)) return false;
			seen.add(activity.sessionId);
			return true;
		})
		.map((activity) => {
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
					y: 0,
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
				// …unless it is mid-conversation, where the facing is what makes the two of
				// them look at each other rather than past each other.
				facing: prev.meeting || canWander(activity.status) ? prev.facing : ("front" as Facing),
			};
			placed.push(next);
			return next;
		});
	return { ...world, pets };
}

/** True when this Proc is busy with something the engine must not interrupt. */
function isBusy(pet: Pet): boolean {
	return pet.motion.kind === "held" || pet.meeting !== undefined;
}

/**
 * Stage the meeting for one `ao send` between two sessions.
 *
 * Both Procs leave what they are doing and run — faster than either of them ever
 * strolls — to a spot between them, stand face to face, and go home afterwards.
 * An ANCHORED Proc gets up from its desk or its bed for this, which is the one
 * exception to "a Proc at a place cannot wander" and a deliberate one: the
 * message is addressed to that session, so that session answers, and its place is
 * where it lives rather than a cage it is stuck in.
 *
 * Returns the world unchanged when the meeting cannot honestly be staged: an end
 * that is not on the desktop, an end in the human's hand, or another conversation
 * already running.
 */
export function startConversation(
	world: World,
	{ from, to, line, now }: { from: string; to: string; line: string; now: number },
): World {
	if (from === to) return world;
	if (world.pets.filter((pet) => pet.meeting).length >= MAX_CONCURRENT_MEETS * 2) return world;

	const sender = world.pets.find((pet) => pet.id === from);
	const receiver = world.pets.find((pet) => pet.id === to);
	if (!sender || !receiver || isBusy(sender) || isBusy(receiver)) return world;

	// They meet between where they are, so neither has to cross the whole desktop.
	const half = MEET_GAP_PX / 2;
	const centre = meetingCentre(world, (sender.x + receiver.x) / 2, [sender.id, receiver.id]);
	const senderIsLeft = sender.x <= receiver.x;
	const spots: Record<string, number> = {
		[sender.id]: senderIsLeft ? centre - half : centre + half,
		[receiver.id]: senderIsLeft ? centre + half : centre - half,
	};

	return {
		...world,
		pets: world.pets.map((pet) => {
			if (pet.id !== from && pet.id !== to) return pet;
			const meeting: Meeting = {
				withId: pet.id === from ? to : from,
				homeX: pet.x,
				line: pet.id === from ? line : "",
				phase: "approaching",
				until: now + MEET_GREET_MS,
			};
			// Reduced motion keeps the meaning — they are talking — and drops only the
			// travel. They say their piece where they stand.
			if (world.reducedMotion || world.parked) {
				return { ...pet, meeting: { ...meeting, phase: "greeting", until: now + MEET_GREET_MS } };
			}
			return {
				...pet,
				meeting,
				facing: facingFor(pet.x, spots[pet.id]),
				motion: runTo(pet.x, spots[pet.id], now),
			};
		}),
	};
}

/**
 * Where the pair actually meets.
 *
 * The midpoint is the intention, but a meeting is a STAGED scene — the whole
 * point is that you can see two Procs talking — and landing it on top of a third
 * Proc who happens to be standing there makes it unreadable. So the centre slides
 * to the nearest place where both spots are clear, and falls back to the clamped
 * midpoint when the band is too full to have one (overlapping is then honest, and
 * still better than not staging the meeting at all).
 */
function meetingCentre(world: World, wanted: number, participants: string[]): number {
	const half = MEET_GAP_PX / 2;
	const minX = world.band.minX + half;
	const maxX = world.band.maxX - half;
	const bystanders = world.pets.filter((pet) => !participants.includes(pet.id));
	const clear = (centre: number) => [centre - half, centre + half].every((x) => isFree(x, bystanders, world.spacing));

	const start = Math.min(maxX, Math.max(minX, wanted));
	if (clear(start)) return start;
	const step = Math.max(8, world.spacing / 6);
	for (let offset = step; offset <= maxX - minX; offset += step) {
		for (const candidate of [start - offset, start + offset]) {
			if (candidate < minX || candidate > maxX) continue;
			if (clear(candidate)) return candidate;
		}
	}
	return start;
}

/** A run: the same shape as a stroll, but on the event's clock rather than ambience's. */
function runTo(fromX: number, toX: number, now: number): PetMotion {
	const distance = Math.abs(toX - fromX);
	const durationMs = Math.max(MEET_RUN_MIN_MS, Math.min(MEET_RUN_MAX_MS, distance * 2));
	return { kind: "walking", fromX, toX, startedAt: now, endsAt: now + durationMs };
}

/**
 * Move a conversation on by one beat.
 *
 * Only the greeting is timed; the two runs end when the Procs actually land, so
 * the phases can never get ahead of the art. Runs even while parked, so a display
 * that slept mid-conversation does not leave two Procs stranded together.
 */
function advanceMeeting(pet: Pet, now: number, rng: Rng): Pet {
	const meeting = pet.meeting;
	if (!meeting || pet.motion.kind !== "standing") return pet;

	if (meeting.phase === "approaching") {
		return { ...pet, meeting: { ...meeting, phase: "greeting", until: now + MEET_GREET_MS } };
	}
	if (meeting.phase === "greeting") {
		if (now < meeting.until) return pet;
		if (Math.abs(pet.x - meeting.homeX) < 1) {
			return { ...pet, meeting: undefined, facing: "front", restUntil: pickRest(now, rng) };
		}
		return {
			...pet,
			meeting: { ...meeting, phase: "returning" },
			facing: facingFor(pet.x, meeting.homeX),
			motion: runTo(pet.x, meeting.homeX, now),
		};
	}
	// Home again. Facing the human, as a Proc that is not travelling always does.
	return { ...pet, meeting: undefined, facing: "front", restUntil: pickRest(now, rng) };
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
export function dragPet(world: World, petId: string, x: number, y = 0): World {
	return {
		...world,
		pets: world.pets.map((pet) =>
			// Deliberately unclamped in BOTH axes: a drag that stops dead at an edge
			// feels like the app grabbing back. It goes where the pointer goes.
			pet.id === petId && pet.motion.kind === "held" ? { ...pet, x, y: Math.max(0, y) } : pet,
		),
	};
}

/**
 * Let go. Inside the band it simply stands where it was dropped; beyond it, it
 * WALKS back on rather than snapping, so the user can see where it went. Either
 * way the next tick's separation pass keeps it off anybody already standing there.
 */
export function releasePet(
	world: World,
	petId: string,
	now: number,
	rng: Rng,
	/** How fast the pointer was moving when it let go, in px/ms. Up is positive. */
	throwSpeed: { vx: number; vy: number } = { vx: 0, vy: 0 },
): World {
	return {
		...world,
		pets: world.pets.map((pet) => {
			if (pet.id !== petId || pet.motion.kind !== "held") return pet;
			const landed = Math.min(world.band.maxX, Math.max(world.band.minX, pet.x));

			// Reduced motion keeps the placement — that part is the human's — and drops
			// the flight, which is decoration. It is simply set down where it was let go.
			if (world.reducedMotion || world.parked) {
				return { ...pet, y: 0, placed: true, motion: { kind: "standing" }, restUntil: pickRest(now, rng) };
			}

			const vx = clampSpeed(throwSpeed.vx);
			const vy = clampSpeed(throwSpeed.vy);
			if (pet.y > 0 || vx !== 0 || vy !== 0) {
				return { ...pet, motion: { kind: "flying", vx, vy }, restUntil: pickRest(now, rng) };
			}

			if (landed === pet.x) {
				return { ...pet, placed: true, motion: { kind: "standing" }, restUntil: pickRest(now, rng) };
			}
			// Dropped off the end with no throw behind it: the landing spot is the
			// ENGINE's choice, not the human's, so it does not earn the exemption.
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

function clampSpeed(v: number): number {
	const scaled = v * THROW_SCALE;
	return Math.max(-MAX_THROW_SPEED, Math.min(MAX_THROW_SPEED, scaled));
}

/**
 * Advance every Proc that is in the air by `dtMs`.
 *
 * Separate from {@link tick} and driven on animation frames rather than the slow
 * decision tick, because a fall is the one thing on this desktop that CSS cannot
 * interpolate for us: a transition draws a straight line between two points, and
 * a thrown Proc travels an arc.
 */
export function advanceFlight(world: World, dtMs: number): World {
	if (dtMs <= 0) return world;
	return {
		...world,
		pets: world.pets.map((pet) => (pet.motion.kind === "flying" ? step(pet, dtMs, world) : pet)),
	};
}

function step(pet: Pet, dtMs: number, world: World): Pet {
	const motion = pet.motion;
	if (motion.kind !== "flying") return pet;

	let vx = motion.vx;
	let vy = motion.vy - GRAVITY_PX_PER_MS2 * dtMs;
	let x = pet.x + vx * dtMs;
	let y = pet.y + vy * dtMs;

	// The band's edges are walls, not cliffs: a thrown Proc bounces off them rather
	// than disappearing off the side of the display.
	if (x < world.band.minX) {
		x = world.band.minX;
		vx = Math.abs(vx) * BOUNCE_RESTITUTION;
	} else if (x > world.band.maxX) {
		x = world.band.maxX;
		vx = -Math.abs(vx) * BOUNCE_RESTITUTION;
	}

	vx *= Math.max(0, 1 - (y > 0 ? AIR_DRAG_PER_MS : GROUND_DRAG_PER_MS) * dtMs);

	if (y > 0) return { ...pet, x, y, motion: { kind: "flying", vx, vy } };

	// On the floor. A fast landing bounces; a slow one has nothing left to show.
	y = 0;
	const landingSpeed = Math.abs(vy);
	const hit = pet.y > 0 && landingSpeed > 0 ? kickUpDust(pet, landingSpeed) : pet.bounce;
	if (landingSpeed > BOUNCE_MIN_SPEED || Math.abs(vx) > SLIDE_MIN_SPEED) {
		return {
			...pet,
			x,
			y,
			bounce: hit,
			motion: { kind: "flying", vx, vy: landingSpeed > BOUNCE_MIN_SPEED ? landingSpeed * BOUNCE_RESTITUTION : 0 },
		};
	}
	// Come to rest. Where it lands is where the human put it, so nothing shoves it.
	return { ...pet, x, y, bounce: hit, placed: true, facing: "front", motion: { kind: "standing" } };
}

/** Put a Proc down where it is. Used when animation frames have stopped coming. */
function land(pet: Pet, now: number, rng: Rng): Pet {
	if (pet.motion.kind !== "flying") return pet;
	return {
		...pet,
		y: 0,
		placed: true,
		facing: "front",
		motion: { kind: "standing" },
		restUntil: pickRest(now, rng),
	};
}

/**
 * The puff a landing throws up: how big, and which landing it was.
 *
 * Strength runs 0-1 off the landing speed, so a Proc set down gently barely
 * raises anything and one dropped from the top of the display raises a cloud.
 */
function kickUpDust(pet: Pet, landingSpeed: number): { seq: number; strength: number } {
	return {
		seq: (pet.bounce?.seq ?? 0) + 1,
		strength: Math.max(0.25, Math.min(1, landingSpeed / DUST_FULL_SPEED)),
	};
}

/** Advance the world to `now`. Pure: same inputs, same output. */
export function tick(world: World, now: number, rng: Rng): World {
	// 1. Settle every stroll that has finished (or that parking cut short), and put
	//    down anything still in the air if we have been parked. A flight is drawn on
	//    animation frames, and animation frames STOP when the window is occluded or
	//    the display sleeps — so without this a thrown Proc hangs in mid-air until
	//    somebody looks at the desktop again.
	let pets = world.pets.map((pet) => settle(pet, now, world, rng));
	if (world.parked) pets = pets.map((pet) => land(pet, now, rng));

	// 2. Move any conversation on. Before the crowding rescue, so a Proc that has
	//    just landed at a meeting spot is already in `greeting` rather than looking
	//    like an ordinary Proc standing somewhere unexpected.
	pets = pets.map((pet) => advanceMeeting(pet, now, rng));
	pets = faceEachOther(pets);

	// 3. Rescue anyone left off the band (a display resize). Deliberately NOT a
	//    crowding sweep: re-flowing the row on every tick meant one Proc arriving
	//    from a stroll slid every other Proc on the desktop, which is the surprise
	//    motion this engine exists to avoid. Crowding is settled by whoever turns
	//    up, at the moment they turn up — see `settle` and `placeNewPet`.
	pets = pets.map((pet) => clampToBand(pet, world.band));

	// 4. Consider new strolls, oldest-rested first so nobody is starved by the cap.
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

/** Two Procs mid-greeting turn to look at each other, whichever way round they stand. */
function faceEachOther(pets: Pet[]): Pet[] {
	const greeting = pets.filter((pet) => pet.meeting?.phase === "greeting" && pet.motion.kind === "standing");
	if (greeting.length < 2) return pets;
	const byId = new Map(pets.map((pet) => [pet.id, pet]));
	return pets.map((pet) => {
		if (pet.meeting?.phase !== "greeting" || pet.motion.kind !== "standing") return pet;
		const other = byId.get(pet.meeting.withId);
		if (!other) return pet;
		const facing: Facing = other.x < pet.x ? "left" : "right";
		return facing === pet.facing ? pet : { ...pet, facing };
	});
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
	// A summon's spot at the front and a meeting spot are both DELIBERATE, so they
	// are not nudged aside the way an ordinary stroll's destination is.
	const deliberate = summoned || pet.meeting !== undefined;
	const landed = deliberate ? pet.motion.toX : nearestFreeX(pet.motion.toX, pet, world);
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
	// A Proc in the user's hand, or away meeting another one, does nothing of its
	// own until it is let go / the conversation is over.
	if (pet.motion.kind !== "standing") return pet;
	if (pet.meeting) return pet;
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
