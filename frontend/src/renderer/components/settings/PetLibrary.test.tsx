import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { castForSession, withSpecies } from "../../../companion/cast";
import { LOOKS_STORAGE_KEY, parseProjectLooks, resolveSpecies } from "../../../companion/look-store";
import { SPECIES, speciesForProject } from "../../../companion/species";
import { useUiStore } from "../../stores/ui-store";
import { PetLibrary } from "./PetLibrary";

const { useWorkspaceQuery } = vi.hoisted(() => ({ useWorkspaceQuery: vi.fn() }));
const { looksChanged } = vi.hoisted(() => ({ looksChanged: vi.fn() }));

vi.mock("../../hooks/useWorkspaceQuery", () => ({ useWorkspaceQuery }));
vi.mock("../../lib/bridge", () => ({
	aoBridge: { companion: { looksChanged, onOpenPetLibrary: () => () => undefined } },
}));

function session(id: string, title: string, extra: Record<string, unknown> = {}) {
	return { id, title, isTerminated: false, kind: "worker", ...extra };
}

function workspaces(...groups: Array<{ name: string; sessions: ReturnType<typeof session>[] }>) {
	return groups.map((group, index) => ({ id: `p${index}`, name: group.name, sessions: group.sessions }));
}

const DEFAULT_DATA = workspaces(
	{ name: "agent-orchestrator", sessions: [session("ao-1", "fix the flaky test"), session("ao-2", "pet library")] },
	{ name: "starlight", sessions: [session("sl-1", "api rewrite")] },
);

beforeEach(() => {
	window.localStorage.clear();
	// A `storage` event with a null key is what `localStorage.clear()` reports, and it is
	// also how this suite drops the live store's cached snapshot between tests.
	window.dispatchEvent(new StorageEvent("storage", { key: null }));
	looksChanged.mockReset();
	useWorkspaceQuery.mockReturnValue({ data: DEFAULT_DATA, isSuccess: true });
	useUiStore.setState({ petLibraryRequest: null });
});

/** The creature TILES, filtered on `aria-pressed` so the reset button is not one. */
function creatureTiles() {
	return within(screen.getByRole("group", { name: "Creature" }))
		.getAllByRole("button")
		.filter((button) => button.hasAttribute("aria-pressed"));
}

function creatureTile(name: string) {
	return within(screen.getByRole("group", { name: "Creature" })).getByRole("button", {
		name: new RegExp(name, "i"),
	});
}

/** What the storage says this project wears, read back the way the overlay reads it. */
function storedSpecies(project: string) {
	return resolveSpecies(project, parseProjectLooks(window.localStorage.getItem(LOOKS_STORAGE_KEY)));
}

describe("choosing which project to dress", () => {
	it("lists the projects, which is the only thing there is to pick", () => {
		render(<PetLibrary />);
		const nav = within(screen.getByRole("navigation", { name: "Projects" }));

		expect(nav.getByRole("button", { name: /agent-orchestrator/ })).toBeTruthy();
		expect(nav.getByRole("button", { name: /starlight/ })).toBeTruthy();
	});

	it("never offers a session, because a session is not a thing you dress any more", () => {
		// ⚠ The regression this guards: #168 listed every worker and let each be dressed by
		// hand. The human asked for exactly one choice per project, so a session title must
		// not be a control at all.
		render(<PetLibrary />);

		expect(screen.queryByRole("button", { name: /fix the flaky test/ })).toBeNull();
		expect(screen.queryByRole("navigation", { name: "Sessions" })).toBeNull();
	});

	it("draws each row as the creature its project is", () => {
		render(<PetLibrary />);
		const row = screen.getByRole("button", { name: /starlight/ });

		expect(row.querySelector("svg[data-species]")?.getAttribute("data-species")).toBe(speciesForProject("starlight"));
	});

	it("opens on the first project, so there is always something to look at", () => {
		render(<PetLibrary />);

		expect(screen.getByRole("button", { name: /agent-orchestrator/ })).toHaveAttribute("aria-current", "true");
	});

	it("switches to another project when it is picked", async () => {
		render(<PetLibrary />);

		await userEvent.click(screen.getByRole("button", { name: /starlight/ }));

		expect(screen.getByRole("button", { name: /starlight/ })).toHaveAttribute("aria-current", "true");
	});

	it("says so plainly when there is nothing to dress", () => {
		useWorkspaceQuery.mockReturnValue({ data: [], isSuccess: true });
		render(<PetLibrary />);

		expect(screen.getByText(/no projects/i)).toBeTruthy();
	});
});

describe("the project's own pets, drawn as they really are", () => {
	it("shows every live session of the project, each in the colour its hash gave it", () => {
		// The picture is the argument: same animal, different colours. Asserted against the
		// hash rather than against each other, so it pins the actual mapping — the session's
		// own slot, carried onto whichever creature the project wears.
		const { container } = render(<PetLibrary />);
		const drawn = [...container.querySelectorAll("[data-pet-strip] svg[data-species]")];
		const wornBy = (ref: string) => withSpecies(castForSession(ref), speciesForProject("agent-orchestrator"));

		expect(drawn).toHaveLength(2);
		expect(drawn.map((svg) => svg.getAttribute("data-palette"))).toEqual([
			wornBy("ao-1").palette,
			wornBy("ao-2").palette,
		]);
		expect(drawn.map((svg) => svg.getAttribute("data-accessory"))).toEqual([
			wornBy("ao-1").hatId,
			wornBy("ao-2").hatId,
		]);
		// And they actually differ, which is the only thing left telling two workers on one
		// project apart now that they are the same animal.
		expect(new Set(drawn.map((svg) => svg.getAttribute("data-palette"))).size).toBe(2);
	});

	it("draws them all as the ONE creature the project wears", async () => {
		const { container } = render(<PetLibrary />);

		await userEvent.click(creatureTile("Ghost"));
		const drawn = [...container.querySelectorAll("[data-pet-strip] svg[data-species]")];

		expect(drawn.map((svg) => svg.getAttribute("data-species"))).toEqual(["ghost", "ghost"]);
	});

	it("says how many it is not showing rather than dropping them quietly", () => {
		useWorkspaceQuery.mockReturnValue({
			data: workspaces({
				name: "big",
				sessions: Array.from({ length: 9 }, (_, i) => session(`b-${i}`, `job ${i}`)),
			}),
			isSuccess: true,
		});
		const { container } = render(<PetLibrary />);

		expect(container.querySelectorAll("[data-pet-strip] svg[data-species]")).toHaveLength(4);
		expect(screen.getByText(/4 of the 9 sessions/)).toBeTruthy();
	});
});

describe("choosing the creature", () => {
	it("offers every creature there is", () => {
		render(<PetLibrary />);

		expect(creatureTiles()).toHaveLength(SPECIES.length);
	});

	it("marks the one the project is wearing", () => {
		render(<PetLibrary />);
		const mine = SPECIES.find((entry) => entry.id === speciesForProject("agent-orchestrator"))!;

		expect(creatureTile(mine.name)).toHaveAttribute("aria-pressed", "true");
	});

	it("stores the choice against the PROJECT, where the next launch will find it", async () => {
		render(<PetLibrary />);

		await userEvent.click(creatureTile("Toadstool"));

		expect(storedSpecies("agent-orchestrator")).toBe("toadstool");
		// Untouched: a creature answers "which project", so choosing one must not move
		// anybody else's.
		expect(storedSpecies("starlight")).toBe(speciesForProject("starlight"));
	});

	it("nudges the overlay, so the desktop repaints without a reload", async () => {
		render(<PetLibrary />);

		await userEvent.click(creatureTile("Slime"));

		expect(looksChanged).toHaveBeenCalled();
	});

	it("offers the way back only once there is something to go back from", async () => {
		render(<PetLibrary />);
		const back = () => screen.queryByRole("button", { name: /back to the default/i });

		expect(back()).toBeNull();
		await userEvent.click(creatureTile("Ghost"));
		expect(back()).toBeTruthy();

		await userEvent.click(back()!);

		expect(storedSpecies("agent-orchestrator")).toBe(speciesForProject("agent-orchestrator"));
		expect(back()).toBeNull();
	});

	it("changes the project it is looking at, not the one it started on", async () => {
		render(<PetLibrary />);

		await userEvent.click(screen.getByRole("button", { name: /starlight/ }));
		await userEvent.click(creatureTile("Chick"));

		expect(storedSpecies("starlight")).toBe("chick");
		expect(storedSpecies("agent-orchestrator")).toBe(speciesForProject("agent-orchestrator"));
	});
});

describe("what is NOT on this screen any more", () => {
	it("has no colour picker and no accessory picker", () => {
		// ⚠ The point of the whole change. Colour and accessory are automatic per session;
		// a control for them would be a second answer to a question the hash already owns.
		render(<PetLibrary />);

		expect(screen.queryByRole("group", { name: "Colour" })).toBeNull();
		expect(screen.queryByRole("group", { name: "Accessory" })).toBeNull();
		expect(screen.getAllByRole("group")).toHaveLength(1);
	});

	it("leaves a session's own look alone when the project's creature changes", async () => {
		render(<PetLibrary />);
		const before = castForSession("ao-1");

		await userEvent.click(creatureTile("Cat"));

		// Nothing about the session was written; only the project's row exists.
		expect(Object.keys(parseProjectLooks(window.localStorage.getItem(LOOKS_STORAGE_KEY)))).toEqual([
			"agent-orchestrator",
		]);
		expect(castForSession("ao-1")).toEqual(before);
	});
});

describe("arriving from a right-click on the desktop", () => {
	it("opens on the PROJECT of the pet that was clicked", () => {
		// The overlay sends the SESSION it was right-clicked on; the creature is chosen per
		// project, so the app resolves one to the other against the list it already holds.
		useUiStore.setState({ petLibraryRequest: "sl-1" });
		render(<PetLibrary />);

		expect(screen.getByRole("button", { name: /starlight/ })).toHaveAttribute("aria-current", "true");
	});

	it("forgets the request once honoured, or the next visit would jump unasked", () => {
		useUiStore.setState({ petLibraryRequest: "sl-1" });
		render(<PetLibrary />);

		expect(useUiStore.getState().petLibraryRequest).toBeNull();
	});

	it("stays where it is when the session is not one it knows", () => {
		useUiStore.setState({ petLibraryRequest: "ghost-session" });
		render(<PetLibrary />);

		expect(screen.getByRole("button", { name: /agent-orchestrator/ })).toHaveAttribute("aria-current", "true");
	});
});

describe("forgetting projects that are gone", () => {
	it("drops the choice of a project that no longer exists", () => {
		window.localStorage.setItem(
			LOOKS_STORAGE_KEY,
			JSON.stringify({ v: 1, projects: { "agent-orchestrator": "ghost", deleted: "cat" } }),
		);
		window.dispatchEvent(new StorageEvent("storage", { key: null }));
		render(<PetLibrary />);

		const kept = parseProjectLooks(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(Object.keys(kept)).toEqual(["agent-orchestrator"]);
	});

	it("keeps a project whose sessions have all ended, because the project has not", () => {
		// ⚠ Pruned against every PROJECT, terminated sessions included. A project with
		// nothing running is still a project, and its creature must survive the quiet spell.
		useWorkspaceQuery.mockReturnValue({
			data: workspaces(
				{ name: "agent-orchestrator", sessions: [session("ao-1", "fix the flaky test")] },
				{ name: "dormant", sessions: [session("d-1", "done", { isTerminated: true })] },
			),
			isSuccess: true,
		});
		window.localStorage.setItem(
			LOOKS_STORAGE_KEY,
			JSON.stringify({ v: 1, projects: { dormant: "ghost", gone: "cat" } }),
		);
		window.dispatchEvent(new StorageEvent("storage", { key: null }));
		render(<PetLibrary />);

		expect(Object.keys(parseProjectLooks(window.localStorage.getItem(LOOKS_STORAGE_KEY)))).toEqual(["dormant"]);
	});

	it("forgets nothing while the fetch is still in flight", () => {
		// An in-flight or failed fetch is not evidence that anybody's project is gone.
		useWorkspaceQuery.mockReturnValue({ data: undefined, isSuccess: false });
		const stored = JSON.stringify({ v: 1, projects: { "agent-orchestrator": "ghost", deleted: "cat" } });
		window.localStorage.setItem(LOOKS_STORAGE_KEY, stored);
		window.dispatchEvent(new StorageEvent("storage", { key: null }));
		render(<PetLibrary />);

		expect(window.localStorage.getItem(LOOKS_STORAGE_KEY)).toBe(stored);
	});
});
