import { describe, expect, it } from "vitest";
import { castForSession, withSpecies } from "./cast";
import {
	chooseSpecies,
	clearSpeciesChoice,
	isSpeciesChosen,
	parseProjectLooks,
	pruneProjectLooks,
	resolveSpecies,
	serializeProjectLooks,
} from "./look-store";
import { speciesForProject } from "./species";

const PROJECT = "agent-orchestrator";
const OTHER = "starlight";

describe("the creature a project wears", () => {
	// The ONE thing anybody chooses. Keyed on the PROJECT because that is the question a
	// creature answers - every session on a project is the same animal, so the band groups
	// itself by shape and there is nothing left to decode.

	it("falls back to the hash of the project's name, so a project is somebody instantly", () => {
		expect(resolveSpecies(PROJECT, {})).toBe(speciesForProject(PROJECT));
	});

	it("shows a chosen creature over the hash", () => {
		const chosen = chooseSpecies({}, PROJECT, "ghost");

		expect(resolveSpecies(PROJECT, chosen)).toBe("ghost");
		expect(isSpeciesChosen(chosen, PROJECT)).toBe(true);
	});

	it("leaves other projects on their own hash", () => {
		const chosen = chooseSpecies({}, PROJECT, "ghost");

		expect(resolveSpecies(OTHER, chosen)).toBe(speciesForProject(OTHER));
		expect(isSpeciesChosen(chosen, OTHER)).toBe(false);
	});

	it("resolves a project with no name at all", () => {
		// A pet whose project the feed did not carry. It still has to be drawn as something.
		expect(resolveSpecies(undefined, chooseSpecies({}, PROJECT, "ghost"))).toBe(speciesForProject(undefined));
	});

	it("never mutates the map it was given", () => {
		const before = {};
		chooseSpecies(before, PROJECT, "ghost");

		expect(before).toEqual({});
	});

	it("goes back to the hash when the choice is cleared, and says so by being ABSENT", () => {
		// Absent is the only way to say "the default", so there is exactly one way to say it.
		const cleared = clearSpeciesChoice(chooseSpecies({}, PROJECT, "ghost"), PROJECT);

		expect(PROJECT in cleared).toBe(false);
		expect(resolveSpecies(PROJECT, cleared)).toBe(speciesForProject(PROJECT));
		expect(isSpeciesChosen(cleared, PROJECT)).toBe(false);
	});

	it("returns the same object when there was nothing to clear", () => {
		const before = chooseSpecies({}, PROJECT, "ghost");

		expect(clearSpeciesChoice(before, OTHER)).toBe(before);
	});

	it("ignores a creature this build cannot draw", () => {
		// The file may have been written by a LATER build that knows creatures this one does
		// not. The right answer is the hash, not a crash and not a blank.
		expect(resolveSpecies(PROJECT, { [PROJECT]: "dragon" as never })).toBe(speciesForProject(PROJECT));
		expect(isSpeciesChosen({ [PROJECT]: "dragon" as never }, PROJECT)).toBe(false);
	});
});

describe("colour and accessory, which nobody chooses", () => {
	// ⚠ The whole point of this change. There is NO api here that sets a session's colour or
	// accessory, and these tests are what stops one growing back: a pet's own look is the
	// hash of its ref and nothing else, which is what "random per pet" means.

	it("gives a session the same look every time, which is what a restart is", () => {
		expect(castForSession("ao-1")).toEqual(castForSession("ao-1"));
	});

	it("cannot be moved by anything stored, because the store only holds creatures", () => {
		// A project's choice changes which BODY a session is drawn on and nothing else. Its
		// colour slot and its accessory slot are the hash's, before and after.
		const before = castForSession("ao-1");
		const after = withSpecies(before, resolveSpecies(PROJECT, chooseSpecies({}, PROJECT, "slime")));

		expect(after.species).toBe("slime");
		expect(castForSession("ao-1")).toEqual(before);
	});

	it("varies across the sessions of ONE project, so two workers are still tellable apart", () => {
		// Every session on a project is the same creature by design. The colour is the only
		// thing left that separates them, so it has to actually differ.
		const species = resolveSpecies(PROJECT, chooseSpecies({}, PROJECT, "cat"));
		const siblings = Array.from({ length: 8 }, (_, i) => `agent-orchestrator-${170 + i}`);
		const worn = siblings.map((ref) => withSpecies(castForSession(ref), species));

		expect(new Set(worn.map((cast) => cast.species))).toEqual(new Set(["cat"]));
		expect(new Set(worn.map((cast) => cast.palette)).size).toBeGreaterThan(1);
		expect(new Set(worn.map((cast) => cast.id)).size).toBeGreaterThan(4);
	});
});

describe("persisting", () => {
	it("survives a write and a read, which is what a restart is", () => {
		const projects = chooseSpecies(chooseSpecies({}, PROJECT, "ghost"), OTHER, "toadstool");

		const restored = parseProjectLooks(serializeProjectLooks(projects));

		expect(restored).toEqual(projects);
		expect(resolveSpecies(PROJECT, restored)).toBe("ghost");
		expect(resolveSpecies(OTHER, restored)).toBe("toadstool");
	});

	it("writes a versioned envelope holding the projects and nothing else", () => {
		// ⚠ The `sessions` half #168 wrote is GONE, not merely unread. Keeping a key nothing
		// writes would leave a second, stale answer to "what colour is this pet".
		const written = JSON.parse(serializeProjectLooks(chooseSpecies({}, PROJECT, "ghost")));

		expect(written.v).toBe(1);
		expect(written.projects).toEqual({ [PROJECT]: "ghost" });
		expect(Object.keys(written).sort()).toEqual(["projects", "v"]);
	});

	it("keeps every project out of a value written by the build that also stored sessions", () => {
		// The upgrade path off #168: its per-session dressing is dropped on the floor, which
		// is the intent, and its project creatures come through untouched.
		const old = JSON.stringify({
			v: 1,
			sessions: { "ao-1": { palette: "mint", hat: "cone" } },
			projects: { [PROJECT]: "ghost" },
		});

		expect(parseProjectLooks(old)).toEqual({ [PROJECT]: "ghost" });
	});

	it("reads nothing at all as nothing chosen", () => {
		expect(parseProjectLooks(null)).toEqual({});
		expect(parseProjectLooks("")).toEqual({});
	});

	it("survives anything at all in the stored value", () => {
		// This runs on the OVERLAY, on someone's desktop, before a single pet is drawn. A
		// parse failure must cost the decoration and never the pets.
		for (const raw of [
			"not json",
			"{",
			"[]",
			"42",
			'"nope"',
			"null",
			'{"v":"x"}',
			'{"projects":5}',
			'{"projects":[]}',
			'{"projects":{"":"ghost"}}',
			'{"projects":{"a":7}}',
			'{"projects":{"a":"dragon"}}',
		]) {
			expect(parseProjectLooks(raw), raw).toEqual({});
		}
	});
});

describe("forgetting projects that no longer exist", () => {
	it("forgets a project that is gone and keeps the ones that are not", () => {
		const projects = chooseSpecies(chooseSpecies({}, PROJECT, "ghost"), "gone", "cat");

		expect(pruneProjectLooks(projects, [PROJECT])).toEqual({ [PROJECT]: "ghost" });
	});

	it("returns the very same map when there is nothing to forget", () => {
		// Identity, not equality: the caller writes to localStorage on a change, and a fresh
		// object every poll would rewrite it on every tick for ever.
		const projects = chooseSpecies({}, PROJECT, "ghost");

		expect(pruneProjectLooks(projects, [PROJECT, OTHER])).toBe(projects);
	});

	it("forgets everything when there are no projects left", () => {
		expect(pruneProjectLooks(chooseSpecies({}, PROJECT, "ghost"), [])).toEqual({});
	});
});
