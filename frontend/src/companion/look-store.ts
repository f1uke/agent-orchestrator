import { SPECIES, speciesForProject, type SpeciesId } from "./species";

// Which CREATURE each project is drawn as - the one thing about a pet anybody chooses.
//
// Pure and storage-free on purpose: `look-store-live.ts` binds this to localStorage, the
// Pet library drives it, and the overlay reads the result. Every function takes the whole
// map and returns a new one, so a caller can render a preview of a choice without
// committing it.
//
// ⚠ COLOUR AND ACCESSORY ARE NOT HERE, and that is the design rather than an omission.
// They are a stable hash of the session ref (`castForSession`), which is what makes every
// session somebody the moment it starts and what "random per pet" means. An earlier build
// let a human dress one session by hand; that was removed, store and all, because a second
// source of truth for a pet's colour is worse than no choice at all.
//
// The map is deliberately SPARSE: a project nobody has chosen for has no key, which makes
// "back to the default" a deletion rather than a second kind of value. Absent means the
// hash decides, and there is exactly one way to say that.

/**
 * Which creature each project is drawn as. Sparse; absent means the hash decides.
 *
 * ⚠ Keyed on the PROJECT, deliberately. The colour answers "which session is this?" and
 * varies within a project so two workers can be told apart. The creature answers the
 * question above that one - WHICH PROJECT - so every session on a project is the same
 * animal, and the band groups itself by shape without anybody having to read a label.
 *
 * It is what replaced the coloured mark on the name chip: a mark has to be looked at and
 * decoded, a creature is known by the time you have noticed it.
 */
export type ProjectLooks = Readonly<Record<string, SpeciesId>>;

/**
 * Where the choices live. Same key in both windows; they share an origin.
 *
 * ⚠ Still named `looks` although it now holds only creatures. Renaming it would drop every
 * project's chosen creature on upgrade, which is a real cost for a tidier string.
 */
export const LOOKS_STORAGE_KEY = "ao.companion.looks";

/** Bumped only if the stored SHAPE changes. */
const STORAGE_VERSION = 1;

/** True when `id` is a creature this build can actually draw. */
function isKnownSpecies(id: unknown): id is SpeciesId {
	return typeof id === "string" && SPECIES.some((entry) => entry.id === id);
}

/**
 * The creature this project shows: the chosen one, or the hash of its name.
 *
 * Defensive on the stored value because the file may have been written by a LATER build
 * that knows creatures this one does not, and the right answer to a creature we cannot
 * draw is the one the hash would have given - not a crash, and not a blank.
 */
export function resolveSpecies(project: string | undefined, projects: ProjectLooks): SpeciesId {
	const chosen = project ? projects[project] : undefined;
	return isKnownSpecies(chosen) ? chosen : speciesForProject(project);
}

/** True when this project is showing a human's choice rather than the hash. */
export function isSpeciesChosen(projects: ProjectLooks, project: string | undefined): boolean {
	return isKnownSpecies(project ? projects[project] : undefined);
}

/** Record a project's creature. */
export function chooseSpecies(projects: ProjectLooks, project: string, species: SpeciesId): ProjectLooks {
	return { ...projects, [project]: species };
}

/** Put a project back on the hash. Absent is the only way to say "the default". */
export function clearSpeciesChoice(projects: ProjectLooks, project: string): ProjectLooks {
	if (!(project in projects)) return projects;
	const next = { ...projects };
	delete next[project];
	return next;
}

/**
 * Forget the projects that are gone.
 *
 * ⚠ Feed this the AUTHORITATIVE project list, which only the main app has.
 *
 * Returns the same object when nothing is dropped: the caller persists on change, and a
 * fresh object every poll would rewrite localStorage on every tick for ever.
 */
export function pruneProjectLooks(projects: ProjectLooks, liveNames: Iterable<string>): ProjectLooks {
	const live = new Set(liveNames);
	const kept = Object.keys(projects).filter((name) => live.has(name));
	if (kept.length === Object.keys(projects).length) return projects;
	return Object.fromEntries(kept.map((name) => [name, projects[name]]));
}

/**
 * Read the stored choices, tolerating anything.
 *
 * This runs on the OVERLAY, on someone's desktop, before a single pet is drawn. A corrupt
 * or half-written value must cost the decoration and never the pets, so every failure path
 * returns "nobody has chosen anything" - which is a perfectly good state that the whole
 * feature is designed around.
 *
 * A value written by the build that ALSO stored per-session dressing parses fine: its
 * `sessions` half is simply not read, so those choices are dropped and every session goes
 * back to its hash, which is the intent.
 */
export function parseProjectLooks(raw: string | null | undefined): ProjectLooks {
	if (!raw) return {};
	let parsed: unknown;
	try {
		parsed = JSON.parse(raw);
	} catch {
		return {};
	}
	if (typeof parsed !== "object" || parsed === null || Array.isArray(parsed)) return {};
	const projects = (parsed as { projects?: unknown }).projects;
	if (typeof projects !== "object" || projects === null || Array.isArray(projects)) return {};

	const out: Record<string, SpeciesId> = {};
	for (const [name, value] of Object.entries(projects as Record<string, unknown>)) {
		if (name === "" || !isKnownSpecies(value)) continue;
		out[name] = value;
	}
	return out;
}

/** The stored form: a versioned envelope, so a future shape change is detectable. */
export function serializeProjectLooks(projects: ProjectLooks): string {
	return JSON.stringify({ v: STORAGE_VERSION, projects });
}
