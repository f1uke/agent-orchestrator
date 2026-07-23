import { describe, expect, it } from "vitest";
import { ALL_COMPANION_STATUSES, isAnchored } from "./scene";
import {
	MAX_ANIMATING,
	MAX_CONCURRENT_WALKS,
	REST_MAX_MS,
	REST_MIN_MS,
	SUMMON_SPACING_PX,
	WALK_CYCLE_MS,
	WALK_CYCLE_STEPS,
	WALK_MAX_MS,
	WALK_MAX_PX,
	WALK_MIN_MS,
	WALK_MIN_PX,
	advanceFlight,
	animatingCount,
	createWorld,
	dragPet,
	grabPet,
	releasePet,
	startConversation,
	startRally,
	MEET_GAP_PX,
	syncActivities,
	tick,
	walkingCount,
	walkSlots,
	type Pet,
	type World,
} from "./behaviour";
import type { SessionKind } from "./live-roster";

const BAND = { minX: 0, maxX: 1000 };
const T0 = 1_000_000;

/** An rng that walks a fixed script and then repeats it, so a tick is reproducible. */
function scripted(...values: number[]): () => number {
	let i = 0;
	return () => values[i++ % values.length];
}

const half = scripted(0.5);

function world(overrides: Partial<World> = {}): World {
	return { ...createWorld(BAND), ...overrides };
}

function activity(id: string, status: Pet["status"]) {
	return { sessionId: id, status };
}

function petById(w: World, id: string): Pet {
	const pet = w.pets.find((p) => p.id === id);
	if (!pet) throw new Error(`no pet ${id}`);
	return pet;
}

describe("syncActivities", () => {
	it("adds a standing Proc for each session", () => {
		const next = syncActivities(world(), [activity("a", "pr_open")], T0, half);

		expect(next.pets).toHaveLength(1);
		expect(next.pets[0].id).toBe("a");
		expect(next.pets[0].motion.kind).toBe("standing");
	});

	it("stands a Proc still by default — nothing walks on the tick it appears", () => {
		let next = syncActivities(world(), [activity("a", "pr_open")], T0, half);
		next = tick(next, T0, half);

		expect(walkingCount(next)).toBe(0);
	});

	it("desyncs the cast: Procs born together get different rest timers", () => {
		const rng = scripted(0.1, 0.9, 0.4);
		const next = syncActivities(
			world(),
			[activity("a", "pr_open"), activity("b", "pr_open"), activity("c", "pr_open")],
			T0,
			rng,
		);

		const rests = next.pets.map((p) => p.restUntil);
		expect(new Set(rests).size).toBe(3);
		for (const rest of rests) {
			expect(rest).toBeGreaterThanOrEqual(T0 + REST_MIN_MS);
			expect(rest).toBeLessThanOrEqual(T0 + REST_MAX_MS);
		}
	});

	it("keeps a Proc's position when only its status changes", () => {
		let next = syncActivities(world(), [activity("a", "pr_open")], T0, half);
		next = { ...next, pets: next.pets.map((p) => ({ ...p, x: 321 })) };
		next = syncActivities(next, [activity("a", "approved")], T0 + 1_000, half);

		expect(petById(next, "a").x).toBe(321);
		expect(petById(next, "a").status).toBe("approved");
	});

	it("removes the Proc when its session leaves the feed", () => {
		let next = syncActivities(world(), [activity("a", "pr_open"), activity("b", "draft")], T0, half);
		next = syncActivities(next, [activity("b", "draft")], T0 + 1_000, half);

		expect(next.pets.map((p) => p.id)).toEqual(["b"]);
	});
});

describe("walking", () => {
	// A world whose Procs are all long past their rest timer, so the only thing
	// deciding whether they walk is the behaviour rules under test.
	function ready(statuses: Array<[string, Pet["status"]]>): World {
		const base = syncActivities(
			world(),
			statuses.map(([id, s]) => activity(id, s)),
			T0,
			half,
		);
		return { ...base, pets: base.pets.map((p, i) => ({ ...p, x: 400 + i * 20, restUntil: T0 })) };
	}

	it("starts a stroll once a Proc's rest has run out", () => {
		const next = tick(ready([["a", "pr_open"]]), T0 + 1, half);
		const pet = petById(next, "a");

		expect(pet.motion.kind).toBe("walking");
		if (pet.motion.kind !== "walking") return;
		const duration = pet.motion.endsAt - pet.motion.startedAt;
		expect(duration).toBeGreaterThanOrEqual(WALK_MIN_MS);
		expect(duration).toBeLessThanOrEqual(WALK_MAX_MS);
		const distance = Math.abs(pet.motion.toX - pet.motion.fromX);
		expect(distance).toBeGreaterThanOrEqual(WALK_MIN_PX);
		expect(distance).toBeLessThanOrEqual(WALK_MAX_PX);
	});

	it("gives a free Proc a stroll within the minute, not once every couple of minutes", () => {
		// A whole minute could pass on a real desktop with nothing moving at all,
		// which reads as broken rather than calm. This holds the rest window to the
		// span a person will actually sit and watch.
		let w = ready([["a", "pr_open"]]);
		w = { ...w, pets: w.pets.map((p) => ({ ...p, restUntil: T0 + REST_MAX_MS })) };

		let walked = false;
		for (let i = 0; i <= 60 && !walked; i++) {
			w = tick(w, T0 + i * 1_000, half);
			walked = walkingCount(w) > 0;
		}

		expect(walked).toBe(true);
	});

	it("never walks a Proc whose scene has a ground", () => {
		for (const status of ALL_COMPANION_STATUSES) {
			if (!isAnchored(status)) continue;
			let w = ready([["a", status]]);
			for (let i = 0; i < 50; i++) w = tick(w, T0 + i * 1_000, scripted(0.2, 0.8, 0.1, 0.6));
			expect(walkingCount(w)).toBe(0);
			expect(petById(w, "a").x).toBe(400);
		}
	});

	it("never walks a Proc we have no live signal for", () => {
		let w = ready([
			["a", "no_signal"],
			["b", "merged"],
			["c", "terminated"],
			["d", "unknown"],
		]);
		for (let i = 0; i < 50; i++) w = tick(w, T0 + i * 1_000, scripted(0.2, 0.8, 0.1));
		expect(walkingCount(w)).toBe(0);
	});

	it("holds a Proc still until its own rest timer expires", () => {
		const base = syncActivities(world(), [activity("a", "pr_open")], T0, scripted(0.5));
		const rest = petById(base, "a").restUntil;
		const before = tick(base, rest - 1, half);
		const after = tick(base, rest + 1, half);

		expect(before.pets[0].motion.kind).toBe("standing");
		expect(after.pets[0].motion.kind).toBe("walking");
	});

	it("keeps at most two Procs strolling at once", () => {
		const w = ready([
			["a", "pr_open"],
			["b", "pr_open"],
			["c", "pr_open"],
			["d", "pr_open"],
			["e", "pr_open"],
		]);
		const next = tick(w, T0 + 1, scripted(0.5, 0.5, 0.9));

		// The literal 2, not MAX_CONCURRENT_WALKS: an assertion against the constant
		// under test moves with it and proves nothing.
		expect(walkingCount(next)).toBe(2);
		expect(MAX_CONCURRENT_WALKS).toBe(2);
	});

	it("keeps the walker cap at or below the animating backstop", () => {
		expect(MAX_CONCURRENT_WALKS).toBeLessThanOrEqual(MAX_ANIMATING);
	});

	it("counts an animated SCENE against the budget, not just a walker", () => {
		// With the full art most of the motion on screen is scenes — sparks, zzz,
		// confetti, a streaming cord — so the backstop only means anything if those
		// count. A deskful of busy sessions THINS the strolling to a single walker.
		//
		// It used to stop it dead, and that was the bug: the scenes are a status tell
		// the session has no say in, so charging them against walking banned strolling
		// on any ordinary working desktop (measured: eight `working` Procs with the
		// whole band to themselves started nothing at all in two minutes). See "a busy
		// screen still has a living band".
		const busy = ready([
			["a", "ci_failed"],
			["b", "ci_failed"],
			["c", "ci_failed"],
			["d", "ci_failed"],
			["e", "ci_failed"],
			["f", "ci_failed"],
			["g", "ci_failed"],
			["h", "ci_failed"],
			["walker", "pr_open"],
			["second", "pr_open"],
		]);
		const next = tick(busy, T0 + 1, half);

		expect(walkingCount(next)).toBe(1);
	});

	it("still lets a Proc stroll when only a few scenes are animating", () => {
		const calm = ready([
			["a", "idle"],
			["b", "working"],
			["walker", "pr_open"],
		]);
		const next = tick(calm, T0 + 1, half);

		expect(petById(next, "walker").motion.kind).toBe("walking");
	});

	it("stops starting strolls once the ~8 animating backstop is full", () => {
		// A hand-built world past the backstop. The two-walker rule means a real
		// desktop never reaches it — this proves the gate is wired, not decorative.
		const w = ready([["a", "pr_open"]]);
		const busy: World = {
			...w,
			pets: [
				...Array.from({ length: MAX_ANIMATING }, (_, i) => ({
					...w.pets[0],
					id: `busy-${i}`,
					motion: { kind: "walking" as const, fromX: 100, toX: 200, startedAt: T0, endsAt: T0 + 60_000 },
				})),
				w.pets[0],
			],
		};
		const next = tick(busy, T0 + 1, half);

		expect(petById(next, "a").motion.kind).toBe("standing");
	});

	it("re-arms a fresh, randomised rest timer when a stroll ends", () => {
		let w = tick(ready([["a", "pr_open"]]), T0 + 1, half);
		const motion = petById(w, "a").motion;
		if (motion.kind !== "walking") throw new Error("expected a walk");
		w = tick(w, motion.endsAt + 1, scripted(0.25));

		const pet = petById(w, "a");
		expect(pet.motion.kind).toBe("standing");
		expect(pet.x).toBe(motion.toX);
		expect(pet.restUntil).toBe(motion.endsAt + 1 + REST_MIN_MS + 0.25 * (REST_MAX_MS - REST_MIN_MS));
	});

	it("turns before the edge instead of half-exiting the band", () => {
		// Hard right of the band, and the rng asks for the longest possible walk to
		// the right: the only legal answer is to turn around.
		const base = ready([["a", "pr_open"]]);
		const w: World = { ...base, pets: [{ ...base.pets[0], x: BAND.maxX - 10 }] };
		const next = tick(w, T0 + 1, scripted(1, 1, 0.99));
		const pet = petById(next, "a");

		if (pet.motion.kind !== "walking") throw new Error("expected a walk");
		expect(pet.motion.toX).toBeLessThan(pet.motion.fromX);
		expect(pet.motion.toX).toBeGreaterThanOrEqual(BAND.minX);
		expect(pet.facing).toBe("left");
	});

	it("keeps every Proc inside the band over a long random run", () => {
		let w = ready([
			["a", "pr_open"],
			["b", "draft"],
			["c", "mergeable"],
		]);
		let seed = 7;
		const rng = () => {
			seed = (seed * 1103515245 + 12345) % 2147483648;
			return seed / 2147483648;
		};
		for (let i = 0; i < 2_000; i++) {
			w = tick(w, T0 + i * 500, rng);
			for (const pet of w.pets) {
				expect(pet.x).toBeGreaterThanOrEqual(BAND.minX);
				expect(pet.x).toBeLessThanOrEqual(BAND.maxX);
				if (pet.motion.kind === "walking") {
					expect(pet.motion.toX).toBeGreaterThanOrEqual(BAND.minX);
					expect(pet.motion.toX).toBeLessThanOrEqual(BAND.maxX);
				}
			}
		}
	});

	it("faces the way it is going", () => {
		const base = ready([["a", "pr_open"]]);
		const right = tick({ ...base, pets: [{ ...base.pets[0], x: 100 }] }, T0 + 1, scripted(0.5, 0.5, 0.9));
		const left = tick({ ...base, pets: [{ ...base.pets[0], x: 900 }] }, T0 + 1, scripted(0.5, 0.5, 0.1));

		expect(petById(right, "a").facing).toBe("right");
		expect(petById(left, "a").facing).toBe("left");
	});
});

describe("summon", () => {
	function summoning(ids: string[], x = 50): World {
		const base = syncActivities(
			world(),
			ids.map((id) => activity(id, "needs_input")),
			T0,
			half,
		);
		// Rest timers far in the future: a summon is an alert, not ambience, and
		// must not wait for the Proc's stroll clock.
		return { ...base, pets: base.pets.map((p, i) => ({ ...p, x: x + i * 5, restUntil: T0 + 10 * REST_MAX_MS })) };
	}

	function runToRest(w: World, from = T0): World {
		let next = w;
		for (let i = 0; i < 60; i++) next = tick(next, from + i * 1_000, half);
		return next;
	}

	it("brings a Proc that needs you to the front and leaves it facing you", () => {
		const arrived = runToRest(summoning(["a"]));
		const pet = petById(arrived, "a");

		expect(pet.motion.kind).toBe("standing");
		expect(pet.facing).toBe("front");
		expect(pet.x).toBeCloseTo((BAND.minX + BAND.maxX) / 2, 5);
	});

	it("summons once — it stands there instead of wandering off again", () => {
		let w = runToRest(summoning(["a"]));
		const restingAt = petById(w, "a").x;
		w = runToRest(w, T0 + 10 * REST_MAX_MS);

		expect(petById(w, "a").x).toBe(restingAt);
		expect(walkingCount(w)).toBe(0);
	});

	it("is exempt from the two-walker cap, because an alert is not ambience", () => {
		const base = summoning(["a"]);
		const busy: World = {
			...base,
			pets: [
				...Array.from({ length: MAX_CONCURRENT_WALKS }, (_, i) => ({
					...base.pets[0],
					id: `walker-${i}`,
					status: "pr_open" as const,
					motion: { kind: "walking" as const, fromX: 100, toX: 200, startedAt: T0, endsAt: T0 + 60_000 },
				})),
				base.pets[0],
			],
		};
		const next = tick(busy, T0 + 1, half);

		expect(petById(next, "a").motion.kind).toBe("walking");
	});

	it("spreads a summoned cohort so two alerts never stack on one spot", () => {
		const arrived = runToRest(summoning(["a", "b"]));
		const [xa, xb] = ["a", "b"].map((id) => petById(arrived, id).x);

		expect(Math.abs(xa - xb)).toBeGreaterThanOrEqual(SUMMON_SPACING_PX);
		for (const x of [xa, xb]) {
			expect(x).toBeGreaterThanOrEqual(BAND.minX);
			expect(x).toBeLessThanOrEqual(BAND.maxX);
		}
	});

	it("makes room for a late alert instead of standing on the one already there", () => {
		let w = runToRest(summoning(["a"]));
		w = syncActivities(w, [activity("a", "needs_input"), activity("b", "needs_input")], T0 + 100_000, half);
		w = runToRest(w, T0 + 100_000);
		const [xa, xb] = ["a", "b"].map((id) => petById(w, id).x);

		expect(Math.abs(xa - xb)).toBeGreaterThanOrEqual(SUMMON_SPACING_PX);
	});

	it("comes back to the front when a new question arrives after it wandered away", () => {
		let w = runToRest(summoning(["a"]));
		// The session moved on, the Proc ambled off, and then it needs you again.
		w = syncActivities(w, [activity("a", "pr_open")], T0 + 100_000, half);
		w = { ...w, pets: w.pets.map((p) => ({ ...p, x: 100 })) };
		w = syncActivities(w, [activity("a", "needs_input")], T0 + 200_000, half);
		w = runToRest(w, T0 + 200_000);

		expect(petById(w, "a").x).toBeCloseTo((BAND.minX + BAND.maxX) / 2, 5);
		expect(petById(w, "a").facing).toBe("front");
	});
});

describe("reduced motion", () => {
	it("never starts a stroll", () => {
		const base = syncActivities(world({ reducedMotion: true }), [activity("a", "pr_open")], T0, half);
		let w: World = { ...base, pets: [{ ...base.pets[0], x: 400, restUntil: T0 }] };
		for (let i = 0; i < 50; i++) w = tick(w, T0 + i * 1_000, half);

		expect(walkingCount(w)).toBe(0);
		expect(petById(w, "a").x).toBe(400);
	});

	it("keeps the summon's meaning: the Proc IS at the front, facing you, without travelling", () => {
		const base = syncActivities(world({ reducedMotion: true }), [activity("a", "needs_input")], T0, half);
		const w = tick({ ...base, pets: [{ ...base.pets[0], x: 20 }] }, T0 + 1, half);
		const pet = petById(w, "a");

		expect(pet.motion.kind).toBe("standing");
		expect(pet.facing).toBe("front");
		expect(pet.x).toBeCloseTo((BAND.minX + BAND.maxX) / 2, 5);
	});
});

describe("parking", () => {
	it("starts nothing while the overlay is covered, fullscreened over, or asleep", () => {
		const base = syncActivities(world({ parked: true }), [activity("a", "pr_open")], T0, half);
		let w: World = { ...base, pets: [{ ...base.pets[0], x: 400, restUntil: T0 }] };
		// Assert on every tick, not just the last: a single stroll that starts and
		// is settled by the next tick would be invisible in a final-state check.
		for (let i = 0; i < 20; i++) {
			w = tick(w, T0 + i * 1_000, half);
			expect(walkingCount(w)).toBe(0);
		}
		expect(petById(w, "a").x).toBe(400);
	});

	it("settles a stroll at its destination rather than freezing a Proc mid-stride", () => {
		let w = tick(
			(() => {
				const base = syncActivities(world(), [activity("a", "pr_open")], T0, half);
				return { ...base, pets: [{ ...base.pets[0], x: 400, restUntil: T0 }] };
			})(),
			T0 + 1,
			half,
		);
		const motion = petById(w, "a").motion;
		if (motion.kind !== "walking") throw new Error("expected a walk");

		w = tick({ ...w, parked: true }, T0 + 2, half);

		expect(petById(w, "a").motion.kind).toBe("standing");
		expect(petById(w, "a").x).toBe(motion.toX);
	});
});

describe("the walk cycle contract", () => {
	it("is the four-beat strip the renderer animates with steps(4, end)", () => {
		expect(WALK_CYCLE_STEPS).toBe(4);
		expect(WALK_CYCLE_MS).toBeGreaterThan(0);
	});
});

describe("crowding", () => {
	// The human's report: stationary Procs stack on top of each other. Two parked
	// pets sharing a spot are worse than a clump — you cannot tell there are two,
	// so a session silently disappears behind another.
	const SPACING = 100;

	function crowded(overrides: Partial<World> = {}): World {
		return world({ spacing: SPACING, ...overrides });
	}

	function positions(w: World): number[] {
		return w.pets.map((pet) => pet.x).sort((a, b) => a - b);
	}

	function closestPair(w: World): number {
		const xs = positions(w);
		let closest = Number.POSITIVE_INFINITY;
		for (let i = 1; i < xs.length; i++) closest = Math.min(closest, xs[i] - xs[i - 1]);
		return closest;
	}

	it("never spawns two Procs on top of each other", () => {
		const rng = scripted(0.5, 0.2, 0.5, 0.2, 0.5, 0.2, 0.51, 0.2);
		const next = syncActivities(
			crowded(),
			["a", "b", "c", "d"].map((id) => activity(id, "pr_open")),
			T0,
			rng,
		);

		expect(next.pets).toHaveLength(4);
		expect(closestPair(next)).toBeGreaterThanOrEqual(SPACING);
	});

	it("keeps a Proc that joins later clear of the ones already standing there", () => {
		let next = crowded();
		for (const id of ["a", "b", "c"]) {
			next = syncActivities(
				next,
				[...next.pets.map((p) => activity(p.id, p.status)), activity(id, "pr_open")],
				T0,
				half,
			);
		}

		expect(next.pets).toHaveLength(3);
		expect(closestPair(next)).toBeGreaterThanOrEqual(SPACING);
	});

	it("never walks a Proc into the space another one is standing in", () => {
		const base = syncActivities(crowded(), [activity("walker", "pr_open"), activity("parked", "pr_open")], T0, half);
		const w: World = {
			...base,
			pets: base.pets.map((pet) =>
				pet.id === "walker" ? { ...pet, x: 300, restUntil: T0 } : { ...pet, x: 420, restUntil: T0 + 10 * REST_MAX_MS },
			),
		};

		// The rng asks for a walk of ~120px to the right, which would land the walker
		// right on top of the parked Proc.
		const next = tick(w, T0 + 1, scripted(0.5, 0.3, 0.9));
		const walker = petById(next, "walker");

		if (walker.motion.kind === "walking") {
			expect(Math.abs(walker.motion.toX - 420)).toBeGreaterThanOrEqual(SPACING);
		}
	});

	it("leaves Procs that something already stacked exactly where they are", () => {
		// The engine used to pull these apart on the next tick. It no longer does:
		// they are on one spot because a drop, a resize or a status change PUT them
		// there, and a desktop that rearranges itself under your eyes was the worse
		// of the two problems (the human's call, 2026-07-22).
		const base = syncActivities(crowded(), [activity("a", "pr_open"), activity("b", "pr_open")], T0, half);
		const stacked: World = { ...base, pets: base.pets.map((pet) => ({ ...pet, x: 500 })) };

		const next = tick(stacked, T0 + 1, half);

		expect(positions(next)).toEqual([500, 500]);
	});

	it("leaves Procs that are already spaced exactly where they are", () => {
		// Separation must not jitter a settled cast on every tick.
		const base = syncActivities(crowded(), [activity("a", "pr_open"), activity("b", "pr_open")], T0, half);
		const spaced: World = {
			...base,
			pets: base.pets.map((pet, i) => ({ ...pet, x: 200 + i * 300, restUntil: T0 + 10 * REST_MAX_MS })),
		};

		const next = tick(spaced, T0 + 1, half);

		expect(positions(next)).toEqual([200, 500]);
	});

	it("keeps everyone inside the band", () => {
		const base = syncActivities(
			crowded(),
			["a", "b", "c", "d", "e"].map((id) => activity(id, "pr_open")),
			T0,
			half,
		);
		const stacked: World = { ...base, pets: base.pets.map((pet) => ({ ...pet, x: BAND.maxX })) };

		const next = tick(stacked, T0 + 1, half);

		for (const pet of next.pets) {
			expect(pet.x).toBeGreaterThanOrEqual(BAND.minX);
			expect(pet.x).toBeLessThanOrEqual(BAND.maxX);
		}
	});

	it("spreads a cast that has just appeared across the band rather than stacking it", () => {
		// Crowding is settled when a Proc TURNS UP, so this is where the spreading
		// happens now: ten new sessions land clear of each other, and are not pulled
		// about afterwards.
		const base = syncActivities(
			crowded(),
			Array.from({ length: 10 }, (_, i) => activity(`p${i}`, "pr_open")),
			T0,
			scripted(0.05, 0.15, 0.25, 0.35, 0.45, 0.55, 0.65, 0.75, 0.85, 0.95),
		);

		const xs = positions(base);
		expect(new Set(xs).size).toBe(10);
		expect(closestPair(base)).toBeGreaterThanOrEqual(SPACING);
	});

	it("does not separate a Proc that is mid-stroll", () => {
		// The walker sits just to the RIGHT of the parked one, so a sweep that failed
		// to exclude walkers would shove it along — and teleporting a Proc that is
		// already animating towards somewhere else is a visible jump.
		const base = syncActivities(crowded(), [activity("parked", "pr_open"), activity("walker", "pr_open")], T0, half);
		const w: World = {
			...base,
			pets: [
				{ ...base.pets[0], id: "parked", x: 500, restUntil: T0 + 10 * REST_MAX_MS },
				{
					...base.pets[1],
					id: "walker",
					x: 520,
					motion: { kind: "walking", fromX: 520, toX: 700, startedAt: T0, endsAt: T0 + 5_000 },
				},
			],
		};

		const next = tick(w, T0 + 1, half);
		const walker = petById(next, "walker");

		expect(walker.motion.kind).toBe("walking");
		expect(walker.x).toBe(520);
	});
});

describe("a crowd is not a cage", () => {
	// The human's report (2026-07-23): "when the characters stand close to each
	// other, they barely walk at all". Reproduced on the real overlay and measured
	// over two minutes of ticks: a lone Proc with the band to itself starts three
	// strolls; a Proc standing 165px from its neighbours starts NONE, ever; a band
	// of twelve does not move one pixel.
	//
	// A stroll was planned by SAMPLING. `planWalk` drew ONE distance, tried it
	// either side of the Proc, and if both spots were within a clearance of somebody
	// it abandoned the whole stroll for another 15-60s rest. The real clearance is
	// the drawn frame — 155px — and Procs settle about 165px apart, so the free
	// window around one is ~14px while the shortest stroll is 60px. Every draw is
	// rejected, for ever. A crowd was an ABSORBING state: a Proc that wandered into
	// one never wandered out, and the band silted up into the row of statues the
	// human was looking at.
	//
	// Crowding is a REPULSION, not a veto. The dice say where a Proc would like to
	// go; if somebody is standing there it goes to the roomiest spot within a
	// stroll's reach that it can actually stand on, and only stands still when the
	// band really has nowhere left to put it.
	const SPACING = 155;

	function crowd(at: Record<string, number>, status: Pet["status"] = "pr_open"): World {
		const base = syncActivities(
			{ ...world(), spacing: SPACING },
			Object.keys(at).map((id) => activity(id, status)),
			T0,
			half,
		);
		return { ...base, pets: base.pets.map((p) => ({ ...p, x: at[p.id], restUntil: T0 })) };
	}

	/** A long deterministic run, so "over time" is a fact rather than a hope. */
	function minutes(start: World, count: number): { world: World; strolled: Set<string> } {
		let seed = 7;
		const rng = () => {
			seed = (seed * 1103515245 + 12345) % 2147483648;
			return seed / 2147483648;
		};
		let next = start;
		const strolled = new Set<string>();
		for (let i = 0; i < count * 60; i++) {
			next = tick(next, T0 + i * 1_000, rng);
			for (const pet of next.pets) if (pet.motion.kind === "walking") strolled.add(pet.id);
		}
		return { world: next, strolled };
	}

	it("sets off for a spot it can stand on when the one the dice picked is taken", () => {
		// `a` is at the closed end of a row: the spot the dice offer 160px to its
		// right is where `b` stands, and 160px to its left is off the band. There is
		// open floor behind it, so standing still is not the honest answer.
		const next = tick(crowd({ a: 100, b: 265, c: 430 }), T0 + 1, half);
		const a = petById(next, "a");

		expect(a.motion.kind).toBe("walking");
		if (a.motion.kind !== "walking") return;
		for (const neighbour of [265, 430]) {
			expect(Math.abs(a.motion.toX - neighbour)).toBeGreaterThanOrEqual(SPACING);
		}
	});

	it("keeps a stroll a stroll: no further than one, and no shorter", () => {
		// The way OUT of a crowd must not become a sprint across the desktop, nor a
		// twitch on the spot. Whatever the search finds, it is still one stroll.
		const next = tick(crowd({ a: 100, b: 265, c: 430 }), T0 + 1, half);
		const a = petById(next, "a");

		if (a.motion.kind !== "walking") throw new Error("expected a walk");
		const distance = Math.abs(a.motion.toX - a.motion.fromX);
		expect(distance).toBeGreaterThanOrEqual(WALK_MIN_PX);
		expect(distance).toBeLessThanOrEqual(WALK_MAX_PX);
	});

	it("does not pin a dense row to standing for ever", () => {
		// Five Procs a clearance-and-a-bit apart. Before the fix only the one at the
		// open end ever moved: the rest were pinned for as long as the row held.
		const { strolled } = minutes(crowd({ a: 20, b: 185, c: 350, d: 515, e: 680 }), 2);

		expect([...strolled].sort()).toEqual(["a", "b", "c", "d", "e"]);
	});

	it("loosens a dense row instead of leaving it a row", () => {
		const start = crowd({ a: 20, b: 185, c: 350, d: 515, e: 680 });
		const tightestBefore = tightestGap(start);
		const { world: after } = minutes(start, 2);

		expect(tightestGap(after)).toBeGreaterThan(tightestBefore);
	});

	it("never stands a Proc on top of another, however tight the band is", () => {
		// The repulsion may not buy its liveliness with overlap: every destination
		// the engine picks is still a clearance clear of everybody else's.
		const { world: after } = minutes(crowd({ a: 20, b: 185, c: 350, d: 515, e: 680 }), 2);

		expect(tightestGap(after)).toBeGreaterThanOrEqual(SPACING);
	});

	it("leaves a Proc with nowhere to go standing, rather than shuffling it on the spot", () => {
		// A band packed to its clearance, edge to edge: there is no spot left that is
		// not already somebody's. Standing is then the truthful answer, and it must
		// KEEP being the answer — a Proc edging back and forth for ever, because each
		// spot looks better from the other one, is the worse bug.
		const row = { a: 0, b: 155, c: 310, d: 465, e: 620, f: 775, g: 930 };
		const packed = { ...crowd(row), band: { minX: 0, maxX: 930 } };
		const before = positionsOf(packed);
		const { world: after } = minutes(packed, 2);

		expect(walkingCount(after)).toBe(0);
		expect(positionsOf(after)).toEqual(before);
	});

	it("spends the slack at the end of a tight row and then settles", () => {
		// The same row with a little band left over. It spreads into what there is —
		// and then STOPS, rather than trading the gap back and forth for ever.
		const row = { a: 0, b: 155, c: 310, d: 465, e: 620, f: 775, g: 930 };
		const { world: spread } = minutes({ ...crowd(row), band: { minX: 0, maxX: 1000 } }, 2);
		const settled = positionsOf(spread);
		const { world: later } = minutes(spread, 2);

		expect(tightestGap(spread)).toBeGreaterThanOrEqual(SPACING);
		expect(positionsOf(later)).toEqual(settled);
	});
});

/** The closest two Procs on the band stand to each other. */
function tightestGap(w: World): number {
	const xs = positionsOf(w);
	let closest = Number.POSITIVE_INFINITY;
	for (let i = 1; i < xs.length; i++) closest = Math.min(closest, xs[i] - xs[i - 1]);
	return closest;
}

describe("a busy screen still has a living band", () => {
	// The second half of the same report, and a separate cause. Measured on the
	// engine: EIGHT `working` Procs spread 230px apart — all the room in the world —
	// started NOT ONE stroll in two minutes, while seven of them started thirteen.
	// The cliff sits exactly at MAX_ANIMATING.
	//
	// `working` streams its cord, which counts as an animating scene, and the walk
	// budget was `MAX_ANIMATING - animatingCount`. So a status tell the session has
	// no say in — five of the fifteen scenes animate, `working` among them — was
	// charged against the optional, transform-only, compositor-cheap business of
	// walking, and an ordinary day's worth of running sessions banned strolling
	// outright.
	//
	// The backstop keeps its job: a busy screen still moves LESS than a quiet one.
	// It may not stop moving altogether, because a band where nothing can ever move
	// is not a calmer desktop, it is a broken one.

	function busy(count: number, status: Pet["status"]): World {
		const base = syncActivities(
			{ ...world(), spacing: 155 },
			Array.from({ length: count }, (_, i) => activity(`p${i}`, status)),
			T0,
			half,
		);
		// Spread across the whole band, so crowding cannot be what stops them.
		return {
			...base,
			pets: base.pets.map((p, i) => ({ ...p, x: BAND.minX + (i * (BAND.maxX - BAND.minX)) / count, restUntil: T0 })),
		};
	}

	it("still strolls when every Proc on the band has an animating scene", () => {
		let w = busy(MAX_ANIMATING, "working");
		let strolled = false;
		for (let i = 0; i < 120 && !strolled; i++) {
			w = tick(w, T0 + i * 1_000, half);
			strolled = walkingCount(w) > 0;
		}

		expect(strolled).toBe(true);
	});

	it("holds a busy screen to fewer strollers than a quiet one", () => {
		// The backstop, stated as the fact it is meant to be rather than as the
		// accident it had become.
		const loud = busy(MAX_ANIMATING, "working");
		const quiet = busy(MAX_ANIMATING, "pr_open");

		expect(walkSlots(loud)).toBeLessThan(walkSlots(quiet));
		expect(walkSlots(loud)).toBeGreaterThan(0);
	});

	it("never lets more than the ceiling stroll at once, whatever else is animating", () => {
		let w = busy(12, "working");
		for (let i = 0; i < 240; i++) {
			w = tick(w, T0 + i * 1_000, half);
			expect(walkingCount(w)).toBeLessThanOrEqual(MAX_CONCURRENT_WALKS);
		}
	});
});

describe("facing", () => {
	it("turns a Proc back to face you when its scene gains a place to be", () => {
		// `facing` persists after a stroll, and the whole sprite mirrors — scenery and
		// all. A Proc that walked left and then sat down at a desk would show the desk
		// flipped to its other side, which reads as the furniture teleporting.
		let w = syncActivities(world(), [activity("a", "pr_open")], T0, half);
		w = { ...w, pets: w.pets.map((pet) => ({ ...pet, facing: "left" as const })) };

		w = syncActivities(w, [activity("a", "idle")], T0 + 1_000, half);

		expect(petById(w, "a").facing).toBe("front");
	});

	it("leaves a Proc that can still stroll facing the way it was going", () => {
		let w = syncActivities(world(), [activity("a", "pr_open")], T0, half);
		w = { ...w, pets: w.pets.map((pet) => ({ ...pet, facing: "left" as const })) };

		w = syncActivities(w, [activity("a", "draft")], T0 + 1_000, half);

		expect(petById(w, "a").facing).toBe("left");
	});
});

describe("dragging", () => {
	const SPACING = 100;

	function dragWorld(ids: string[] = ["a"]): World {
		const base = syncActivities(
			{ ...world(), spacing: SPACING },
			ids.map((id) => activity(id, "pr_open")),
			T0,
			half,
		);
		return { ...base, pets: base.pets.map((pet, i) => ({ ...pet, x: 300 + i * 200, restUntil: T0 })) };
	}

	it("picks a Proc up when it is grabbed", () => {
		const next = grabPet(dragWorld(), "a", T0);

		expect(petById(next, "a").motion.kind).toBe("held");
	});

	it("follows the pointer, even off the end of the band", () => {
		let w = grabPet(dragWorld(), "a", T0);
		w = dragPet(w, "a", BAND.maxX + 250);

		expect(petById(w, "a").x).toBe(BAND.maxX + 250);
	});

	it("stands where it is dropped", () => {
		let w = grabPet(dragWorld(), "a", T0);
		w = dragPet(w, "a", 640);
		w = releasePet(w, "a", T0 + 2_000, half);

		expect(petById(w, "a").motion.kind).toBe("standing");
		expect(petById(w, "a").x).toBe(640);
	});

	it("comes back onto the band when it is thrown off the end", () => {
		// The band is the floor. A Proc left beyond it would be half off the display,
		// or gone entirely — which looks like the session vanished.
		let w = grabPet(dragWorld(), "a", T0);
		w = dragPet(w, "a", BAND.maxX + 400);
		w = releasePet(w, "a", T0 + 2_000, half);
		const pet = petById(w, "a");

		expect(pet.x).toBeLessThanOrEqual(BAND.maxX);
		expect(pet.x).toBeGreaterThanOrEqual(BAND.minX);
		// And it walks back in rather than snapping, so you can see where it went.
		expect(pet.motion.kind).toBe("walking");
	});

	it("never walks off on its own while you are holding it", () => {
		let w = grabPet(dragWorld(), "a", T0);
		for (let i = 0; i < 40; i++) w = tick(w, T0 + i * 1_000, scripted(0.2, 0.8, 0.6));

		expect(petById(w, "a").motion.kind).toBe("held");
		expect(walkingCount(w)).toBe(0);
	});

	it("is not shoved around by the crowding sweep while held", () => {
		// The human is holding it. Something else moving it under their pointer would
		// feel like the app fighting them for it.
		let w = dragWorld(["a", "b"]);
		w = grabPet(w, "a", T0);
		w = dragPet(w, "a", 500);
		w = { ...w, pets: w.pets.map((pet) => (pet.id === "b" ? { ...pet, x: 500 } : pet)) };
		w = tick(w, T0 + 1, half);

		expect(petById(w, "a").x).toBe(500);
	});

	it("lands exactly where it was let go, even on top of another Proc", () => {
		// The crowding rule used to win here and slide the Proc off the drop point.
		// The human chose the other way round (2026-07-22): a deliberate placement is
		// not a mistake to be corrected, and an overlap you made yourself is one you
		// can see and undo.
		let w = dragWorld(["a", "b"]);
		w = grabPet(w, "a", T0);
		const onto = petById(w, "b").x + 5;
		w = dragPet(w, "a", onto);
		w = releasePet(w, "a", T0 + 2_000, half);
		w = tick(w, T0 + 2_001, half);

		expect(petById(w, "a").x).toBe(onto);
	});

	it("only ever holds one Proc, because there is only one pointer", () => {
		let w = grabPet(dragWorld(["a", "b"]), "a", T0);
		w = grabPet(w, "b", T0 + 100);

		expect(petById(w, "a").motion.kind).toBe("standing");
		expect(petById(w, "b").motion.kind).toBe("held");
	});

	it("counts a held Proc against the animation budget, because it is flailing", () => {
		const w = grabPet(dragWorld(["a"]), "a", T0);

		expect(animatingCount(w)).toBe(1);
	});

	it("can be dragged even when it is anchored to a desk", () => {
		// Anchoring says a working Proc cannot WANDER. Being picked up by the human is
		// not wandering, and refusing to move would just feel broken.
		const base = syncActivities({ ...world(), spacing: SPACING }, [activity("a", "idle")], T0, half);
		let w = grabPet(base, "a", T0);
		w = dragPet(w, "a", 700);
		w = releasePet(w, "a", T0 + 2_000, half);

		expect(petById(w, "a").x).toBe(700);
	});

	it("still works with motion reduced — the drag is the user's, not ours", () => {
		let w = grabPet({ ...dragWorld(), reducedMotion: true }, "a", T0);
		w = dragPet(w, "a", 620);
		w = releasePet(w, "a", T0 + 2_000, half);

		expect(petById(w, "a").x).toBe(620);
		expect(petById(w, "a").motion.kind).toBe("standing");
	});

	it("ignores a grab for a Proc that is not there", () => {
		expect(() => grabPet(dragWorld(), "ghost", T0)).not.toThrow();
		expect(grabPet(dragWorld(), "ghost", T0).pets).toHaveLength(1);
	});
});

describe("never walking in place forever", () => {
	// The worst thing the overlay can draw is a Proc that walks and walks and never
	// arrives: it asserts activity that is not happening, on a surface whose entire
	// job is to be a truthful glance. So every arrangement that HAS a resting state
	// must reach one and stay in it.
	//
	// The real spacing is the whole drawn frame, which is WIDER than a summon rank
	// slot — and that is the trap. The rank pulls two alerts to 96px apart while
	// separation pushes them to 136px, so each undoes the other for ever.
	const REAL_SPACING = 136;

	function ticked(start: World, ticks: number, from = T0): World {
		let next = start;
		for (let i = 0; i < ticks; i++) next = tick(next, from + i * 1_000, half);
		return next;
	}

	function alerts(ids: string[], spacing = REAL_SPACING): World {
		const base = syncActivities(
			{ ...world(), spacing },
			ids.map((id) => activity(id, "needs_input")),
			T0,
			half,
		);
		return { ...base, pets: base.pets.map((p, i) => ({ ...p, x: 50 + i * 5 })) };
	}

	it("settles a summoned cohort instead of oscillating between the rank and separation", () => {
		let w = ticked(alerts(["a", "b"]), 400);

		expect(walkingCount(w)).toBe(0);
		// And STAYS settled: a state reached once but abandoned on the next tick is
		// the bug, not the fix.
		for (let i = 0; i < 100; i++) {
			w = tick(w, T0 + (400 + i) * 1_000, half);
			expect(walkingCount(w)).toBe(0);
		}
	});

	it("settles a summoned Proc whose front spot a neighbour is standing on", () => {
		const base = syncActivities(
			{ ...world(), spacing: REAL_SPACING },
			// The neighbour is `idle` — one of the two states that stay put — so the
			// only thing that can be walking in this test is the summoned Proc, which
			// is what it is about.
			[activity("neighbour", "idle"), activity("a", "needs_input")],
			T0,
			half,
		);
		// The still Proc is parked right where the summon rank wants to be.
		const centre = (BAND.minX + BAND.maxX) / 2;
		let w = {
			...base,
			pets: base.pets.map((p) => ({ ...p, x: p.id === "neighbour" ? centre - 20 : 60 })),
		};
		w = ticked(w, 400);

		expect(walkingCount(w)).toBe(0);
		for (let i = 0; i < 100; i++) {
			w = tick(w, T0 + (400 + i) * 1_000, half);
			expect(walkingCount(w)).toBe(0);
		}
	});

	it("never starts a walk whose destination another Proc has already claimed this tick", () => {
		// Two amble Procs due at the same moment, steered STRAIGHT AT EACH OTHER: a
		// walks right 160px from 200, b walks left 160px from 560, and the two
		// destinations land 40px apart. Deciding each walk against the roster as it
		// was at the start of the tick, neither can see the other's claim, and both
		// set off for a spot only one of them can stand on.
		const collide = scripted(0.5, 0.5, 0.6, 0.5, 0.5, 0.2);
		const base = syncActivities(
			{ ...world(), spacing: REAL_SPACING },
			[activity("a", "pr_open"), activity("b", "draft")],
			T0,
			half,
		);
		const due = {
			...base,
			pets: base.pets.map((p) => ({ ...p, x: p.id === "a" ? 200 : 560, restUntil: T0 })),
		};
		const next = tick(due, T0 + 1, collide);

		const targets = next.pets.map((p) => (p.motion.kind === "walking" ? p.motion.toX : p.x));
		expect(Math.abs(targets[0] - targets[1])).toBeGreaterThanOrEqual(REAL_SPACING);
	});
});

describe("a Proc you placed by hand", () => {
	// Dropping a Proc onto an occupied spot used to set off a cascade: the drop
	// point was overruled (the Proc slid 155px away from where it was let go) and a
	// third Proc that had nothing to do with the gesture slid 120px as well. Direct
	// manipulation has to mean what it says — where you let go IS where it goes —
	// and the human chose to allow the overlap that follows from that.
	function placedWorld(): World {
		const base = syncActivities(
			{ ...world(), spacing: 136 },
			[activity("dragged", "pr_open"), activity("sitting", "pr_open"), activity("bystander", "draft")],
			T0,
			half,
		);
		const at: Record<string, number> = { dragged: 640, sitting: 190, bystander: 380 };
		return { ...base, pets: base.pets.map((p) => ({ ...p, x: at[p.id], restUntil: T0 + 10 * REST_MAX_MS })) };
	}

	function dropOn(target: number): World {
		let w = grabPet(placedWorld(), "dragged", T0);
		w = dragPet(w, "dragged", target);
		w = releasePet(w, "dragged", T0 + 500, half);
		for (let i = 0; i < 30; i++) w = tick(w, T0 + 1_000 + i * 1_000, half);
		return w;
	}

	it("stays exactly where it was let go, even right on top of another Proc", () => {
		expect(petById(dropOn(190), "dragged").x).toBe(190);
	});

	it("stays put when it is dropped BETWEEN two Procs, not just at the end of the row", () => {
		// The old crowding sweep runs left to right, so the leftmost Proc happened to
		// keep its spot whatever else happened. A drop in the middle is the case that
		// actually moved.
		expect(petById(dropOn(380), "dragged").x).toBe(380);
	});

	it("does not shove the Proc it landed on", () => {
		expect(petById(dropOn(190), "sitting").x).toBe(190);
	});

	it("does not slide a bystander that had nothing to do with the gesture", () => {
		expect(petById(dropOn(190), "bystander").x).toBe(380);
	});

	it("goes back to being ordinary once it strolls off under its own steam", () => {
		// The hand placement is a fact about THIS position. A Proc that has since
		// walked somewhere on its own is standing where the engine put it, and the
		// crowding rules own that spot again.
		let w = dropOn(190);
		w = { ...w, pets: w.pets.map((p) => ({ ...p, restUntil: T0 })) };
		let walked = false;
		for (let i = 0; i < 200 && !walked; i++) {
			w = tick(w, T0 + 40_000 + i * 1_000, half);
			walked = petById(w, "dragged").motion.kind === "walking";
		}
		for (let i = 0; i < 60; i++) w = tick(w, T0 + 260_000 + i * 1_000, half);

		expect(walked).toBe(true);
		const [a, b] = ["dragged", "sitting"].map((id) => petById(w, id).x);
		expect(Math.abs(a - b)).toBeGreaterThanOrEqual(136);
	});
});

describe("nothing moves a Proc that is standing still", () => {
	// Two separate reports, one day apart: dropping a Proc slid a bystander across
	// the band, and a Proc ARRIVING from a stroll re-flowed the whole row. Both are
	// the same per-tick crowding sweep, which recomputed everyone's position from
	// scratch whenever anything anywhere changed.
	//
	// The rule that replaces it: crowding is resolved by the Proc that turns up, at
	// the moment it turns up. Whoever is already standing there is left alone —
	// always. A desktop that rearranges itself while you are looking at it is worse
	// than two Procs standing a little close.
	const SPACING = 136;

	function standing(at: Record<string, number>, status: Pet["status"] = "pr_open"): World {
		const base = syncActivities(
			{ ...world(), spacing: SPACING },
			Object.keys(at).map((id) => activity(id, status)),
			T0,
			half,
		);
		return {
			...base,
			pets: base.pets.map((p) => ({ ...p, x: at[p.id], restUntil: T0 + 10 * REST_MAX_MS })),
		};
	}

	it("leaves the neighbours where they are when a stroll finishes next to them", () => {
		let w = standing({ walker: 300, near: 380, far: 700 });
		// The walker arrives right beside `near`.
		w = {
			...w,
			pets: w.pets.map((p) =>
				p.id === "walker"
					? { ...p, motion: { kind: "walking" as const, fromX: 300, toX: 390, startedAt: T0, endsAt: T0 + 1_000 } }
					: p,
			),
		};
		for (let i = 0; i < 20; i++) w = tick(w, T0 + 2_000 + i * 1_000, half);

		expect(petById(w, "near").x).toBe(380);
		expect(petById(w, "far").x).toBe(700);
	});

	it("makes the ARRIVING Proc step aside instead, so it is not hidden behind the one already there", () => {
		let w = standing({ walker: 300, near: 380 });
		w = {
			...w,
			pets: w.pets.map((p) =>
				p.id === "walker"
					? { ...p, motion: { kind: "walking" as const, fromX: 300, toX: 390, startedAt: T0, endsAt: T0 + 1_000 } }
					: p,
			),
		};
		w = tick(w, T0 + 2_000, half);

		expect(Math.abs(petById(w, "walker").x - 380)).toBeGreaterThanOrEqual(SPACING);
	});

	it("leaves two Procs that are already overlapping exactly where they are", () => {
		// They are only overlapping because something PUT them there — a drop, a
		// resize, a status change. Shuffling them apart later is the surprise motion
		// this whole rule exists to stop.
		let w = standing({ a: 400, b: 405 });
		for (let i = 0; i < 20; i++) w = tick(w, T0 + i * 1_000, half);

		expect(positionsOf(w)).toEqual([400, 405]);
	});

	it("still rescues a Proc left outside the band when the display shrinks", () => {
		// Not a rearrangement: a Proc off the band is a session you cannot see at all.
		let w = standing({ a: 300, b: 900 });
		w = { ...w, band: { minX: 0, maxX: 500 } };
		w = tick(w, T0 + 1_000, half);

		expect(petById(w, "b").x).toBeLessThanOrEqual(500);
		expect(petById(w, "a").x).toBe(300);
	});
});

function positionsOf(w: World): number[] {
	return w.pets.map((pet) => pet.x).sort((a, b) => a - b);
}

describe("two Procs meeting when their sessions talk", () => {
	// `ao send` between two sessions is a real event with two ends, and it is the
	// only thing on this desktop that is ABOUT a relationship rather than about one
	// session. So it is the one time two Procs act together: they run to each other,
	// hop, say their piece, and go back to where they were.
	function pair(overrides: Partial<Pet> = {}): World {
		const base = syncActivities(
			{ ...world(), spacing: 136 },
			[activity("sender", "pr_open"), activity("receiver", "working")],
			T0,
			half,
		);
		return {
			...base,
			pets: base.pets.map((p) => ({
				...p,
				x: p.id === "sender" ? 200 : 800,
				restUntil: T0 + 10 * REST_MAX_MS,
				...overrides,
			})),
		};
	}

	function talk(w: World, now = T0): World {
		return startConversation(w, { from: "sender", to: "receiver", line: "P1 is fixed", now });
	}

	function run(w: World, seconds: number, from = T0): World {
		let next = w;
		for (let i = 1; i <= seconds * 4; i++) next = tick(next, from + i * 250, half);
		return next;
	}

	it("sets both Procs running toward each other", () => {
		const w = talk(pair());

		for (const id of ["sender", "receiver"]) {
			expect(petById(w, id).motion.kind, id).toBe("walking");
		}
		const [a, b] = ["sender", "receiver"].map((id) => petById(w, id).motion);
		if (a.kind !== "walking" || b.kind !== "walking") throw new Error("expected both to run");
		// Toward each other: the left one goes right, the right one goes left.
		expect(a.toX).toBeGreaterThan(200);
		expect(b.toX).toBeLessThan(800);
	});

	it("runs faster than it strolls, because this one is an event and not ambience", () => {
		const w = talk(pair());
		const motion = petById(w, "sender").motion;
		if (motion.kind !== "walking") throw new Error("expected a run");

		const distance = Math.abs(motion.toX - motion.fromX);
		const strollPace = (WALK_MIN_PX + WALK_MAX_PX) / (WALK_MIN_MS + WALK_MAX_MS);
		expect(distance / (motion.endsAt - motion.startedAt)).toBeGreaterThan(strollPace);
	});

	it("brings them face to face rather than on top of each other", () => {
		const met = run(talk(pair()), 2);
		const [a, b] = ["sender", "receiver"].map((id) => petById(met, id));

		expect(a.meeting?.phase).toBe("greeting");
		expect(Math.abs(a.x - b.x)).toBeCloseTo(MEET_GAP_PX, 0);
		expect(a.facing).toBe("right");
		expect(b.facing).toBe("left");
	});

	it("gives the sender the words and leaves the listener listening", () => {
		const met = run(talk(pair()), 2);

		expect(petById(met, "sender").meeting?.line).toBe("P1 is fixed");
		expect(petById(met, "receiver").meeting?.line).toBe("");
	});

	it("sends both home afterwards, to exactly where they were standing", () => {
		const done = run(talk(pair()), 30);

		expect(petById(done, "sender").x).toBe(200);
		expect(petById(done, "receiver").x).toBe(800);
		expect(petById(done, "sender").meeting).toBeUndefined();
		expect(petById(done, "receiver").meeting).toBeUndefined();
	});

	it("gets a Proc up from its desk or its bed for it, and puts it back", () => {
		// The one exception to structural anchoring, and a deliberate one: a message
		// is addressed to THIS session, so the Proc that owns it answers. Its place is
		// where it lives, not a cage — it goes back to it.
		let w = pair();
		w = { ...w, pets: w.pets.map((p) => (p.id === "receiver" ? { ...p, status: "idle" as const } : p)) };
		const met = run(talk(w), 2);
		const done = run(talk(w), 30);

		expect(petById(met, "receiver").meeting?.phase).toBe("greeting");
		expect(petById(done, "receiver").x).toBe(800);
	});

	it("refuses when either end is not on the desktop", () => {
		const w = pair();

		expect(startConversation(w, { from: "sender", to: "ghost", line: "hi", now: T0 })).toBe(w);
		expect(startConversation(w, { from: "ghost", to: "receiver", line: "hi", now: T0 })).toBe(w);
	});

	it("does not pull a Proc out of the human's hand", () => {
		const held = grabPet(pair(), "receiver", T0);

		expect(talk(held)).toBe(held);
	});

	it("stages one conversation at a time, and does not queue a stale one", () => {
		// A meeting is a dramatisation of something that happened at a moment. Playing
		// a queued one out fifteen seconds later would be staging an event that is
		// already over — the same lie the bubble's TTL exists to prevent. The second
		// message still reaches its Proc's bubble; only the performance is skipped.
		let w = syncActivities(
			{ ...world(), spacing: 136 },
			["a", "b", "c", "d"].map((id) => activity(id, "pr_open")),
			T0,
			scripted(0.1, 0.35, 0.6, 0.85),
		);
		w = { ...w, pets: w.pets.map((p) => ({ ...p, restUntil: T0 + 10 * REST_MAX_MS })) };
		w = startConversation(w, { from: "a", to: "b", line: "first", now: T0 });
		const after = startConversation(w, { from: "c", to: "d", line: "second", now: T0 + 100 });

		expect(after).toBe(w);
		expect(petById(after, "c").meeting).toBeUndefined();
	});

	it("keeps them still when motion is reduced, and still lets them talk", () => {
		// The meaning survives without the movement: they say their piece where they
		// stand, which is a static equivalent rather than a silently missing state.
		const w = startConversation(
			{ ...pair(), reducedMotion: true },
			{
				from: "sender",
				to: "receiver",
				line: "P1 is fixed",
				now: T0,
			},
		);

		expect(walkingCount(w)).toBe(0);
		expect(petById(w, "sender").x).toBe(200);
		expect(petById(w, "receiver").x).toBe(800);
		expect(petById(w, "sender").meeting?.phase).toBe("greeting");
		expect(petById(w, "sender").meeting?.line).toBe("P1 is fixed");
	});

	it("ends the conversation even with motion reduced, rather than leaving them stuck", () => {
		const done = run(
			startConversation(
				{ ...pair(), reducedMotion: true },
				{
					from: "sender",
					to: "receiver",
					line: "P1 is fixed",
					now: T0,
				},
			),
			30,
		);

		expect(petById(done, "sender").meeting).toBeUndefined();
	});

	it("does not let a meeting Proc wander off mid-conversation", () => {
		let w = talk(pair());
		w = { ...w, pets: w.pets.map((p) => ({ ...p, restUntil: T0 })) };
		w = run(w, 2);

		expect(petById(w, "sender").meeting?.phase).toBe("greeting");
		expect(walkingCount(w)).toBe(0);
	});
});

describe("where a conversation is staged", () => {
	// A meeting is a scene you are meant to be able to WATCH, so it must not be set
	// down on top of a third Proc who happens to be standing at the midpoint. Caught
	// by rendering it: the pair met exactly where a bystander was already standing
	// and the whole thing read as one indistinct clump.
	it("slides the meeting off a bystander standing at the midpoint", () => {
		let w = syncActivities(
			{ ...world(), spacing: 136 },
			[activity("a", "pr_open"), activity("b", "pr_open"), activity("bystander", "merged")],
			T0,
			half,
		);
		const at: Record<string, number> = { a: 300, b: 700, bystander: 500 };
		w = { ...w, pets: w.pets.map((p) => ({ ...p, x: at[p.id], restUntil: T0 + 10 * REST_MAX_MS })) };
		w = startConversation(w, { from: "a", to: "b", line: "hello", now: T0 });

		for (const id of ["a", "b"]) {
			const motion = petById(w, id).motion;
			if (motion.kind !== "walking") throw new Error(`${id} should be running`);
			expect(Math.abs(motion.toX - 500), id).toBeGreaterThanOrEqual(136);
		}
	});

	it("still stages it when the band is too full to find a clear spot", () => {
		// Overlapping is honest here, and better than silently not showing the one
		// event on this desktop that is about two sessions at once.
		let w = syncActivities(
			{ ...world(), spacing: 136 },
			["a", "b", ...Array.from({ length: 8 }, (_, i) => `p${i}`)].map((id) => activity(id, "pr_open")),
			T0,
			scripted(0.05, 0.15, 0.25, 0.35, 0.45, 0.55, 0.65, 0.75, 0.85, 0.95),
		);
		w = { ...w, pets: w.pets.map((p) => ({ ...p, restUntil: T0 + 10 * REST_MAX_MS })) };
		const talking = startConversation(w, { from: "a", to: "b", line: "hello", now: T0 });

		expect(petById(talking, "a").meeting).toBeDefined();
		expect(petById(talking, "b").meeting).toBeDefined();
	});
});

describe("a roster that repeats a session", () => {
	// The feed's contract is that a snapshot is keyed by session id, so a repeated
	// id is a broken snapshot. Rendering it as two Procs is the worst reading of it:
	// two identical characters on one spot, with one name chip painted over the
	// other, which is exactly what a crowded desktop should never look like.
	it("shows one Proc per session, however many times the snapshot names it", () => {
		const next = syncActivities(
			world(),
			[activity("a", "pr_open"), activity("a", "idle"), activity("b", "idle")],
			T0,
			half,
		);

		expect(next.pets.map((pet) => pet.id)).toEqual(["a", "b"]);
	});

	it("keeps the FIRST reading of a repeated session, not the last", () => {
		// Arbitrary, but it has to be one of them and first-wins is what a `Map`
		// built from the snapshot would give. What matters is that it is stable.
		const next = syncActivities(world(), [activity("a", "pr_open"), activity("a", "idle")], T0, half);

		expect(petById(next, "a").status).toBe("pr_open");
	});
});

describe("picking a Proc up and throwing it", () => {
	// The band was one horizontal line and a Proc had only an x. You could slide one
	// along the floor and that was all. The human wants to lift one into the air and
	// have it FALL back, and to fling it and have it carry.
	function airborne(): World {
		const base = syncActivities({ ...world(), spacing: 136 }, [activity("a", "pr_open")], T0, half);
		return { ...base, pets: base.pets.map((p) => ({ ...p, x: 400, restUntil: T0 + 10 * REST_MAX_MS })) };
	}

	function fly(w: World, ms: number, step = 16): World {
		let next = w;
		for (let t = step; t <= ms; t += step) next = advanceFlight(next, step);
		return next;
	}

	it("lifts a held Proc off the floor, following the pointer up", () => {
		let w = grabPet(airborne(), "a", T0);
		w = dragPet(w, "a", 500, 260);

		expect(petById(w, "a").x).toBe(500);
		expect(petById(w, "a").y).toBe(260);
	});

	it("drops it when you let go in mid-air instead of leaving it hanging", () => {
		let w = grabPet(airborne(), "a", T0);
		w = dragPet(w, "a", 500, 300);
		w = releasePet(w, "a", T0 + 500, half);

		expect(petById(w, "a").motion.kind).toBe("flying");
		const midFall = fly(w, 200);
		expect(petById(midFall, "a").y).toBeLessThan(300);
		expect(petById(midFall, "a").y).toBeGreaterThan(0);
	});

	it("lands it back on the floor and leaves it standing there", () => {
		let w = grabPet(airborne(), "a", T0);
		w = dragPet(w, "a", 500, 300);
		w = releasePet(w, "a", T0 + 500, half);
		w = fly(w, 6_000);

		expect(petById(w, "a").y).toBe(0);
		expect(petById(w, "a").motion.kind).toBe("standing");
	});

	it("carries a flung Proc sideways in the direction it was thrown", () => {
		let w = grabPet(airborne(), "a", T0);
		w = dragPet(w, "a", 500, 200);
		// A flick to the right: the renderer measures the pointer, the engine is told.
		w = releasePet(w, "a", T0 + 500, half, { vx: 1.6, vy: 0.4 });
		w = fly(w, 6_000);

		expect(petById(w, "a").x).toBeGreaterThan(560);
	});

	it("throws it further the harder it is flung", () => {
		// On a band wide enough that neither throw reaches a wall — off a wall the
		// harder throw comes BACK further, which is right but measures nothing.
		const wide = { minX: 0, maxX: 6000 };
		const thrown = (vx: number) => {
			let w = { ...airborne(), band: wide };
			w = grabPet(w, "a", T0);
			w = dragPet(w, "a", 500, 200);
			w = releasePet(w, "a", T0 + 500, half, { vx, vy: 0.3 });
			return petById(fly(w, 6_000), "a").x;
		};

		expect(thrown(1.8)).toBeGreaterThan(thrown(0.6) + 100);
	});

	it("keeps a thrown Proc on the band rather than off the side of the display", () => {
		let w = grabPet(airborne(), "a", T0);
		w = dragPet(w, "a", 500, 200);
		w = releasePet(w, "a", T0 + 500, half, { vx: 9, vy: 2 });
		w = fly(w, 8_000);

		expect(petById(w, "a").x).toBeLessThanOrEqual(BAND.maxX);
		expect(petById(w, "a").x).toBeGreaterThanOrEqual(BAND.minX);
	});

	it("always comes to rest — a Proc must never bounce for ever", () => {
		let w = grabPet(airborne(), "a", T0);
		w = dragPet(w, "a", 500, 700);
		w = releasePet(w, "a", T0 + 500, half, { vx: 2, vy: 1.5 });
		w = fly(w, 20_000);

		expect(petById(w, "a").motion.kind).toBe("standing");
		expect(petById(w, "a").y).toBe(0);
	});

	it("marks where it lands as the human's placement, so nothing shoves it afterwards", () => {
		let w = grabPet(airborne(), "a", T0);
		w = dragPet(w, "a", 500, 200);
		w = releasePet(w, "a", T0 + 500, half, { vx: 0.8, vy: 0 });
		w = fly(w, 6_000);

		expect(petById(w, "a").placed).toBe(true);
	});

	it("just sets it down when motion is reduced, with no flight and no bounce", () => {
		// The drag is the human's and stays. The physics is decoration, and decoration
		// is the part reduced motion drops.
		let w = { ...airborne(), reducedMotion: true };
		w = grabPet(w, "a", T0);
		w = dragPet(w, "a", 500, 400);
		w = releasePet(w, "a", T0 + 500, half, { vx: 2, vy: 1 });

		expect(petById(w, "a").motion.kind).toBe("standing");
		expect(petById(w, "a").y).toBe(0);
		expect(petById(w, "a").x).toBe(500);
	});

	it("does not walk off while it is in the air", () => {
		let w = grabPet(airborne(), "a", T0);
		w = dragPet(w, "a", 500, 600);
		w = releasePet(w, "a", T0 + 500, half, { vx: 0, vy: 0 });
		w = { ...w, pets: w.pets.map((p) => ({ ...p, restUntil: T0 })) };
		w = tick(w, T0 + 1_000, half);

		expect(petById(w, "a").motion.kind).toBe("flying");
	});
});

describe("the dust a landing kicks up", () => {
	// A Proc that hits the floor and simply carries on reads as weightless. The puff
	// is the only thing that says it landed on something.
	function dropped(fromHeight: number, vx = 0): World {
		const base = syncActivities({ ...world(), spacing: 136 }, [activity("a", "pr_open")], T0, half);
		let w = { ...base, pets: base.pets.map((p) => ({ ...p, x: 400, restUntil: T0 + 10 * REST_MAX_MS })) };
		w = grabPet(w, "a", T0);
		w = dragPet(w, "a", 400, fromHeight);
		return releasePet(w, "a", T0 + 100, half, { vx, vy: 0 });
	}

	function fly(w: World, ms: number, step = 16): World {
		let next = w;
		for (let t = step; t <= ms; t += step) next = advanceFlight(next, step);
		return next;
	}

	it("kicks up nothing while a Proc is still in the air", () => {
		expect(petById(fly(dropped(600), 100), "a").bounce).toBeUndefined();
	});

	it("kicks up dust the moment it hits the floor", () => {
		expect(petById(fly(dropped(600), 3_000), "a").bounce).toBeDefined();
	});

	it("kicks up MORE dust from a bigger drop, because it hit harder", () => {
		// The FIRST landing of each: by the third bounce every drop is landing softly,
		// which is right and measures nothing.
		const soft = petById(fly(dropped(80), 400), "a").bounce?.strength ?? 0;
		const hard = petById(fly(dropped(900), 1_000), "a").bounce?.strength ?? 0;

		expect(hard).toBeGreaterThan(soft);
	});

	it("counts each bounce separately, so a second one can be drawn as a second puff", () => {
		// The count is what lets the renderer restart the animation: the same number
		// twice is the same landing, a new number is a new one.
		const first = petById(fly(dropped(900), 1_000), "a").bounce?.seq ?? 0;
		const settled = petById(fly(dropped(900), 6_000), "a").bounce?.seq ?? 0;

		expect(first).toBeGreaterThan(0);
		expect(settled).toBeGreaterThan(first);
	});

	it("never re-kicks dust once it has come to rest", () => {
		const landed = fly(dropped(600), 6_000);
		const later = fly(landed, 4_000);

		expect(petById(later, "a").bounce?.seq).toBe(petById(landed, "a").bounce?.seq);
	});
});

describe("a Proc caught in mid-air when the desktop goes away", () => {
	// A flight is drawn on animation frames, and animation frames stop when the
	// window is occluded or the display sleeps. Without this a thrown Proc hangs in
	// the air until somebody looks at the desktop again.
	it("puts a flying Proc down as soon as it is parked", () => {
		const base = syncActivities({ ...world(), spacing: 136 }, [activity("a", "pr_open")], T0, half);
		let w = { ...base, pets: base.pets.map((p) => ({ ...p, x: 400 })) };
		w = grabPet(w, "a", T0);
		w = dragPet(w, "a", 400, 500);
		w = releasePet(w, "a", T0 + 100, half, { vx: 1, vy: 1 });
		expect(petById(w, "a").motion.kind).toBe("flying");

		w = tick({ ...w, parked: true }, T0 + 200, half);

		expect(petById(w, "a").motion.kind).toBe("standing");
		expect(petById(w, "a").y).toBe(0);
	});
});

describe("grabbing a Proc that is on the move", () => {
	// The engine holds a walker's `x` at the spot it SET OFF from — the compositor is
	// carrying the drawing to the destination — so picking one up mid-stroll snapped
	// it back to where the walk began. It has to be picked up from where it is.
	function walking(): World {
		const base = syncActivities({ ...world(), spacing: 136 }, [activity("a", "pr_open")], T0, half);
		return {
			...base,
			pets: base.pets.map((p) => ({
				...p,
				x: 200,
				motion: { kind: "walking" as const, fromX: 200, toX: 600, startedAt: T0, endsAt: T0 + 4_000 },
			})),
		};
	}

	it("picks it up from where it has actually got to, not where it set off", () => {
		// Halfway through a 200→600 stroll.
		expect(petById(grabPet(walking(), "a", T0 + 2_000), "a").x).toBeCloseTo(400, 0);
	});

	it("picks it up at the start of the stroll if that is where it still is", () => {
		expect(petById(grabPet(walking(), "a", T0), "a").x).toBeCloseTo(200, 0);
	});

	it("picks it up at the destination once the stroll has run its time", () => {
		expect(petById(grabPet(walking(), "a", T0 + 9_000), "a").x).toBeCloseTo(600, 0);
	});

	it("leaves a Proc that is standing exactly where it is", () => {
		const still = syncActivities({ ...world() }, [activity("a", "pr_open")], T0, half);
		const put = { ...still, pets: still.pets.map((p) => ({ ...p, x: 321 })) };

		expect(petById(grabPet(put, "a", T0 + 5_000), "a").x).toBe(321);
	});
});

describe("a rally: shaking the Orchestrator calls its project in", () => {
	// A rally is the one gesture that moves Procs that were not touched. It is
	// user-directed, so it may get an anchored Proc up from its desk — and like the
	// meet, its post is home rather than a cage, so it goes back to it.
	function project(id: string, name: string, kind: SessionKind = "worker", status: Pet["status"] = "pr_open") {
		return { sessionId: id, status, name: id, project: name, kind };
	}

	/** Leader mid-band on `alpha`, two more on `alpha`, one on `beta` well out of the way. */
	function band(at: Record<string, number> = {}): World {
		const base = syncActivities(
			world(),
			[project("lead", "alpha", "orchestrator"), project("a1", "alpha"), project("a2", "alpha"), project("b1", "beta")],
			T0,
			half,
		);
		const home: Record<string, number> = { lead: 500, a1: 120, a2: 900, b1: 40, ...at };
		return {
			...base,
			pets: base.pets.map((p) => ({ ...p, x: home[p.id], restUntil: T0 + 10 * REST_MAX_MS })),
		};
	}

	function run(w: World, seconds: number, from = T0): World {
		let next = w;
		for (let i = 1; i <= seconds * 4; i++) next = tick(next, from + i * 250, half);
		return next;
	}

	const members = (w: World) => w.pets.filter((p) => p.rally && p.rally.leaderId !== p.id);

	it("sets every Proc on the leader's project running toward it", () => {
		const called = startRally(band(), "lead", T0);

		for (const id of ["a1", "a2"]) {
			expect(petById(called, id).motion.kind, id).toBe("walking");
			expect(petById(called, id).rally?.leaderId, id).toBe("lead");
		}
	});

	it("leaves every other project exactly where it was standing", () => {
		const called = startRally(band(), "lead", T0);
		const other = petById(called, "b1");

		expect(other.rally).toBeUndefined();
		expect(other.motion.kind).toBe("standing");
		expect(other.x).toBe(40);
	});

	it("runs them in, rather than strolling them in", () => {
		const motion = petById(startRally(band(), "lead", T0), "a1").motion;
		if (motion.kind !== "walking") throw new Error("expected a run");

		const strollPace = (WALK_MIN_PX + WALK_MAX_PX) / (WALK_MIN_MS + WALK_MAX_MS);
		expect(Math.abs(motion.toX - motion.fromX) / (motion.endsAt - motion.startedAt)).toBeGreaterThan(strollPace);
	});

	it("stands them around the leader without piling them on top of it", () => {
		const gathered = run(startRally(band(), "lead", T0), 2);
		const spots = [...members(gathered), petById(gathered, "lead")].map((p) => p.x).sort((a, b) => a - b);

		expect(members(gathered)).toHaveLength(2);
		for (let i = 1; i < spots.length; i++) {
			expect(spots[i] - spots[i - 1]).toBeGreaterThanOrEqual(gathered.spacing);
		}
		// Surrounding it: one on each side, not a queue on one flank.
		expect(spots[1]).toBe(500);
	});

	it("keeps the whole huddle on the band when the leader is called at the edge", () => {
		const gathered = run(startRally(band({ lead: 995 }), "lead", T0), 3);

		for (const pet of gathered.pets) {
			expect(pet.x, pet.id).toBeGreaterThanOrEqual(gathered.band.minX);
			expect(pet.x, pet.id).toBeLessThanOrEqual(gathered.band.maxX);
		}
		expect(members(gathered).every((p) => p.rally?.phase === "gathered")).toBe(true);
	});

	it("keeps the ring clear of a Proc that was never called", () => {
		// A bystander standing exactly where a ring spot would go. The spot moves; the
		// bystander does not — it had nothing to do with the gesture.
		const gathered = run(startRally(band({ b1: 600 }), "lead", T0), 3);

		expect(petById(gathered, "b1").x).toBe(600);
		for (const member of members(gathered)) {
			expect(Math.abs(member.x - 600), member.id).toBeGreaterThanOrEqual(gathered.spacing);
		}
	});

	it("stays NEXT to the leader even on a band with no room to be tidy in", () => {
		// Found by looking at it: with the near slots taken, a search that steps over
		// every uninvolved Proc walks the "huddle" most of a screen away from the Proc
		// it is meant to be surrounding. Overlapping a bystander is the lesser evil —
		// the huddle paints in front of the band it is standing in.
		const crowded = band({ lead: 500, a1: 60, a2: 940, b1: 400 });
		const packed = {
			...crowded,
			pets: [...crowded.pets, { ...petById(crowded, "b1"), id: "b2", x: 600 }],
		};
		const gathered = run(startRally(packed, "lead", T0), 3);

		for (const member of members(gathered)) {
			expect(Math.abs(member.x - 500), member.id).toBeLessThanOrEqual(2 * gathered.spacing);
		}
	});

	it("turns the ring to look at the leader", () => {
		const gathered = run(startRally(band(), "lead", T0), 2);
		const [left, right] = [...members(gathered)].sort((a, b) => a.x - b.x);

		expect(left.facing).toBe("right");
		expect(right.facing).toBe("left");
		expect(petById(gathered, "lead").facing).toBe("front");
	});

	it("holds the huddle a moment and then lets everyone go, where they stand", () => {
		// They do NOT troop back to where they were called from. They came because
		// they were called, and the spot they are on now is simply where they live —
		// a Proc's place travels with it, desk and all, so there is nothing to go back
		// TO. (The human asked for this explicitly, 2026-07-23: running home again read
		// as the roll-call being undone.)
		const w = band();
		const gathered = run(startRally(w, "lead", T0), 2);
		const spots = members(gathered).map((p) => p.x);
		const after = run(startRally(w, "lead", T0), 30);

		expect(members(gathered)).toHaveLength(2);
		expect(after.pets.every((p) => p.rally === undefined)).toBe(true);
		expect(["a1", "a2"].map((id) => petById(after, id).x)).toEqual(spots);
	});

	it("never leaves a Proc gathered for ever, whatever else happens", () => {
		// Including the leader's own session ending mid-rally: the clock is absolute,
		// so the ones it called still disperse.
		let w = startRally(band(), "lead", T0);
		w = run(w, 1);
		w = syncActivities(w, [project("a1", "alpha"), project("a2", "alpha")], T0 + 1_000, half);

		expect(run(w, 30, T0 + 1_000).pets.every((p) => p.rally === undefined)).toBe(true);
	});

	it("gets a Proc up from its desk for it, and leaves it standing where it came to", () => {
		// The one exception to structural anchoring, exactly as the ao-send meet is:
		// the call is addressed to this project, so this project answers. Its place is
		// not a spot on the band — the desk is drawn AROUND the Proc and travels with
		// it — so there is nothing it has been taken away from.
		const w = band();
		const desks = { ...w, pets: w.pets.map((p) => (p.id === "a1" ? { ...p, status: "idle" as const } : p)) };
		const gathered = run(startRally(desks, "lead", T0), 2);

		expect(isAnchored("idle")).toBe(true);
		expect(petById(gathered, "a1").rally?.phase).toBe("gathered");
		expect(petById(gathered, "a1").x).not.toBe(120);
		expect(petById(run(startRally(desks, "lead", T0), 30), "a1").x).toBe(petById(gathered, "a1").x);
	});

	it("moves Procs and nothing else — every session still shows its real state", () => {
		const before = band();
		const gathered = run(startRally(before, "lead", T0), 2);

		for (const pet of before.pets) {
			expect(petById(gathered, pet.id).status, pet.id).toBe(pet.status);
		}
	});

	it("leaves the leader in the hand — a shake does not put down what you are holding", () => {
		const held = dragPet(grabPet(band(), "lead", T0), "lead", 640, 180);
		const leader = petById(startRally(held, "lead", T0 + 200), "lead");

		expect(leader.motion.kind).toBe("held");
		expect(leader.x).toBe(640);
		expect(leader.y).toBe(180);
		expect(leader.rally?.leaderId).toBe("lead");
	});

	it("gathers around wherever the shaking hand has the leader, on the band", () => {
		// Held out past the end of the display, the ring still forms ON the band —
		// a huddle nobody can see is not a roll-call.
		const held = dragPet(grabPet(band(), "lead", T0), "lead", 1_400, 0);
		const gathered = run(startRally(held, "lead", T0 + 200), 3, T0 + 200);

		for (const member of members(gathered)) {
			expect(member.x, member.id).toBeLessThanOrEqual(gathered.band.maxX);
			expect(member.x, member.id).toBeGreaterThanOrEqual(gathered.band.minX);
		}
	});

	it("ends the leader's call on the clock even while it is still being held", () => {
		// The call is on an absolute clock, and the hand may simply not have let go.
		const held = grabPet(band(), "lead", T0);
		const long = run(startRally(held, "lead", T0), 30);

		expect(petById(long, "lead").rally).toBeUndefined();
		expect(petById(long, "lead").motion.kind).toBe("held");
	});

	it("is only ever called by an Orchestrator", () => {
		const w = band();

		expect(startRally(w, "a1", T0)).toBe(w);
	});

	it("refuses when we do not know which project the leader is on", () => {
		// A Proc whose project we cannot see cannot honestly call "its project" — and
		// the alternative, gathering everything else we also cannot place, is a lie.
		const w = band();
		const unknown = { ...w, pets: w.pets.map((p) => (p.id === "lead" ? { ...p, project: "" } : p)) };

		expect(startRally(unknown, "lead", T0)).toBe(unknown);
		expect(startRally(w, "nobody", T0)).toBe(w);
	});

	it("does not pull a Proc out of the human's hand, or out of a conversation", () => {
		const talking = startConversation(band(), { from: "a1", to: "b1", line: "hi", now: T0 });
		const called = startRally(talking, "lead", T0 + 10);

		expect(petById(called, "a1").rally).toBeUndefined();
		expect(petById(called, "a1").meeting?.withId).toBe("b1");
	});

	it("ignores a second shake while the roll-call is still running", () => {
		const called = startRally(band(), "lead", T0);

		expect(startRally(called, "lead", T0 + 400)).toBe(called);
	});

	it("does not let the ambient stroll clock pull a Proc out of the huddle", () => {
		const w = band();
		const eager = { ...w, pets: w.pets.map((p) => ({ ...p, restUntil: 0 })) };
		const gathered = run(startRally(eager, "lead", T0), 2);

		expect(members(gathered).every((p) => p.motion.kind === "standing")).toBe(true);
	});

	it("fires for an Orchestrator that is the only session on its project", () => {
		// Nobody comes, and that is the honest answer — but the gesture still registered,
		// so the call cue still plays. A shake that looks like nothing happened reads as
		// a broken gesture.
		const alone = startRally(band(), "lead", T0);
		const solo = startRally(
			{ ...alone, pets: alone.pets.filter((p) => p.project !== "alpha" || p.id === "lead") },
			"lead",
			T0,
		);

		expect(petById(solo, "lead").rally?.leaderId).toBe("lead");
	});

	describe("with motion reduced", () => {
		const quiet = (w: World) => ({ ...w, reducedMotion: true });

		it("gathers them without running them, and the gesture still works", () => {
			const called = startRally(quiet(band()), "lead", T0);

			expect(members(called)).toHaveLength(2);
			for (const member of members(called)) {
				expect(member.motion.kind, member.id).toBe("standing");
				expect(member.rally?.phase, member.id).toBe("gathered");
			}
			const spots = members(called).map((p) => p.x);
			expect(Math.abs(spots[0] - spots[1])).toBeGreaterThanOrEqual(called.spacing);
		});

		it("lets them go again when the huddle breaks up, where they gathered", () => {
			const called = startRally(quiet(band()), "lead", T0);
			const done = run(called, 30);

			expect(["a1", "a2"].map((id) => petById(done, id).x)).toEqual(["a1", "a2"].map((id) => petById(called, id).x));
			expect(done.pets.every((p) => p.rally === undefined)).toBe(true);
		});
	});
});
