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
	createWorld,
	syncActivities,
	tick,
	walkingCount,
	type Pet,
	type World,
} from "./behaviour";

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
