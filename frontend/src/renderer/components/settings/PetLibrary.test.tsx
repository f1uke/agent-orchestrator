import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import {
	accessoriesFor,
	APPEARANCE_AXES,
	castForSession,
	defaultLook,
	palettesFor,
	storedIdFor,
	withSpecies,
} from "../../../companion/cast";
import {
	LOOKS_STORAGE_KEY,
	parseLookOverrides,
	parseProjectLooks,
	resolveLook,
	resolveSpecies,
} from "../../../companion/look-store";
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
	window.dispatchEvent(new StorageEvent("storage", { key: null }));
	looksChanged.mockReset();
	useWorkspaceQuery.mockReturnValue({ data: DEFAULT_DATA, isSuccess: true });
	useUiStore.setState({ petLibraryRequest: null });
});

/**
 * The option TILES under one heading, e.g. every colour.
 *
 * Filtered on `aria-pressed`, because a section that somebody has chosen in also holds a
 * "back to the default" button — counting that as an option made a six-colour set
 * measure as seven the moment anyone touched it.
 */
function optionsUnder(axisName: string) {
	return within(screen.getByRole("group", { name: axisName }))
		.getAllByRole("button")
		.filter((button) => button.hasAttribute("aria-pressed"));
}

function optionButton(axisName: string, optionName: string) {
	return within(screen.getByRole("group", { name: axisName })).getByRole("button", {
		name: new RegExp(optionName, "i"),
	});
}

describe("choosing which session to dress", () => {
	it("lists the live sessions under the project they belong to", () => {
		render(<PetLibrary />);
		// Scoped to the nav: the creature section names the project too, in its hint.
		const nav = within(screen.getByRole("navigation", { name: "Sessions" }));

		expect(nav.getByText("agent-orchestrator")).toBeTruthy();
		expect(nav.getByText("starlight")).toBeTruthy();
		expect(screen.getByRole("button", { name: /fix the flaky test/ })).toBeTruthy();
		expect(screen.getByRole("button", { name: /api rewrite/ })).toBeTruthy();
	});

	it("opens on the first session, so there is always something to look at", () => {
		render(<PetLibrary />);

		expect(screen.getByRole("button", { name: /fix the flaky test/ })).toHaveAttribute("aria-current", "true");
	});

	it("switches to another session when it is picked", async () => {
		render(<PetLibrary />);

		await userEvent.click(screen.getByRole("button", { name: /api rewrite/ }));

		expect(screen.getByRole("button", { name: /api rewrite/ })).toHaveAttribute("aria-current", "true");
	});

	it("says so plainly when there are no sessions to dress", () => {
		useWorkspaceQuery.mockReturnValue({ data: [], isSuccess: true });
		render(<PetLibrary />);

		expect(screen.getByText(/no sessions/i)).toBeTruthy();
	});
});

/** What the selected session actually shows: its own colour, on its PROJECT's creature. */
function wornBy(ref: string, project: string) {
	return withSpecies(castForSession(ref), speciesForProject(project));
}

describe("the axes", () => {
	it("draws one section per axis in the registry, plus the creature", () => {
		// The axes still arrive by being in APPEARANCE_AXES, with no change to this
		// component. The creature is its own section because it is keyed on the PROJECT
		// rather than the session, which no axis in that registry is.
		render(<PetLibrary />);

		for (const axis of APPEARANCE_AXES) {
			expect(screen.getByRole("group", { name: axis.name }), axis.id).toBeTruthy();
		}
		expect(screen.getByRole("group", { name: "Creature" })).toBeTruthy();
	});

	it("offers the CREATURE's own options, not the Proc's", () => {
		// ⚠ The failure this catches: offering the Proc's six hats to a slime. The axis
		// registry carries them as a default, and a picker that read them straight off it
		// would offer six hats to a jelly cube.
		render(<PetLibrary />);
		const species = speciesForProject("agent-orchestrator");

		expect(optionsUnder("Colour").length).toBe(palettesFor(species).length);
		expect(optionsUnder("Accessory").length).toBe(accessoriesFor(species).length);
		expect(optionsUnder("Creature").length).toBe(SPECIES.length);
	});

	it("marks the option a session is wearing as in use", () => {
		render(<PetLibrary />);
		const mine = wornBy("ao-1", "agent-orchestrator");

		expect(optionButton("Colour", mine.palette)).toHaveAttribute("aria-pressed", "true");
		expect(optionButton("Accessory", mine.hatId)).toHaveAttribute("aria-pressed", "true");
		expect(within(optionButton("Colour", mine.palette)).getByText(/in use/i)).toBeTruthy();
	});
});

describe("picking the creature a PROJECT is", () => {
	// The one control on this screen keyed on the project rather than the session. Every
	// session on a project is the same animal, which is what took the coloured mark off
	// the name chip — and it is the answer to two projects hashing onto one creature.

	it("marks the creature the project is currently drawn as", () => {
		render(<PetLibrary />);

		expect(optionButton("Creature", speciesById("agent-orchestrator"))).toHaveAttribute("aria-pressed", "true");
	});

	it("changes the creature for the whole PROJECT, not for the session", async () => {
		render(<PetLibrary />);
		await userEvent.click(optionButton("Creature", "Ghost"));

		const stored = parseProjectLooks(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(resolveSpecies("agent-orchestrator", stored)).toBe("ghost");
		// The OTHER session on the same project moved with it; the other project did not.
		expect(resolveSpecies("starlight", stored)).toBe(speciesForProject("starlight"));
		expect(looksChanged).toHaveBeenCalled();
	});

	it("keeps the session's own colour when the creature changes under it", async () => {
		// A session's choice is stored in the Proc's option space — a SLOT — precisely so
		// it survives its project becoming a different animal.
		render(<PetLibrary />);
		const wanted = palettesFor(speciesForProject("agent-orchestrator"))[3].id;
		await userEvent.click(optionButton("Colour", wanted));
		await userEvent.click(optionButton("Creature", "Ghost"));

		const stored = parseLookOverrides(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(resolveLook("ao-1", stored).palette).toBe(
			storedIdFor("palette", speciesForProject("agent-orchestrator"), wanted),
		);
		expect(optionsUnder("Colour").length).toBe(palettesFor("ghost").length);
	});

	it("goes back to the hash of the project's name", async () => {
		render(<PetLibrary />);
		await userEvent.click(optionButton("Creature", "Ghost"));
		await userEvent.click(
			within(screen.getByRole("group", { name: "Creature" })).getByRole("button", { name: /default/i }),
		);

		const stored = parseProjectLooks(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect("agent-orchestrator" in stored).toBe(false);
		expect(resolveSpecies("agent-orchestrator", stored)).toBe(speciesForProject("agent-orchestrator"));
	});

	it("offers the reset only once somebody has chosen", async () => {
		render(<PetLibrary />);

		expect(
			within(screen.getByRole("group", { name: "Creature" })).queryByRole("button", { name: /default/i }),
		).toBeNull();

		await userEvent.click(optionButton("Creature", "Ghost"));

		expect(
			within(screen.getByRole("group", { name: "Creature" })).getByRole("button", { name: /default/i }),
		).toBeTruthy();
	});
});

/** The display NAME of the creature a project is drawn as, for finding its tile. */
function speciesById(project: string): string {
	const id = speciesForProject(project);
	return SPECIES.find((entry) => entry.id === id)!.name;
}

describe("picking a look", () => {
	async function pickHat(hat: string) {
		render(<PetLibrary />);
		await userEvent.click(optionButton("Accessory", hat));
	}

	/** An option of the CREATURE's own set that is not the one this session already has. */
	const otherThan = (axisId: "palette" | "hat", ref: string) => {
		const species = speciesForProject("agent-orchestrator");
		const set = axisId === "palette" ? palettesFor(species) : accessoriesFor(species);
		const worn = withSpecies(castForSession(ref), species);
		return set.find((option) => option.id !== (axisId === "palette" ? worn.palette : worn.hatId))!.id;
	};
	/** …and what that lands on in the store, which is the Proc's slot for it. */
	const stores = (axisId: "palette" | "hat", optionId: string) =>
		storedIdFor(axisId, speciesForProject("agent-orchestrator"), optionId);

	it("changes the hat and leaves the colour on the hash", async () => {
		const wanted = otherThan("hat", "ao-1");
		await pickHat(wanted);

		const stored = parseLookOverrides(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(resolveLook("ao-1", stored).hat).toBe(stores("hat", wanted));
		expect(resolveLook("ao-1", stored).palette).toBe(defaultLook("ao-1").palette);
	});

	it("moves the in-use mark to what was picked", async () => {
		const wanted = otherThan("hat", "ao-1");
		await pickHat(wanted);

		expect(optionButton("Accessory", wanted)).toHaveAttribute("aria-pressed", "true");
		expect(optionButton("Accessory", wornBy("ao-1", "agent-orchestrator").hatId)).toHaveAttribute(
			"aria-pressed",
			"false",
		);
	});

	it("tells the overlay to go and look, so the desktop changes at once", async () => {
		await pickHat(otherThan("hat", "ao-1"));

		expect(looksChanged).toHaveBeenCalled();
	});

	it("dresses only the session that was selected", async () => {
		await pickHat(otherThan("hat", "ao-1"));

		const stored = parseLookOverrides(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(resolveLook("ao-2", stored)).toEqual(defaultLook("ao-2"));
		expect(resolveLook("sl-1", stored)).toEqual(defaultLook("sl-1"));
	});

	it("puts one axis back on the default without touching the other", async () => {
		const hat = otherThan("hat", "ao-1");
		const palette = otherThan("palette", "ao-1");
		render(<PetLibrary />);
		await userEvent.click(optionButton("Accessory", hat));
		await userEvent.click(optionButton("Colour", palette));

		await userEvent.click(
			within(screen.getByRole("group", { name: "Accessory" })).getByRole("button", { name: /default/i }),
		);

		const stored = parseLookOverrides(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(resolveLook("ao-1", stored).hat).toBe(defaultLook("ao-1").hat);
		expect(resolveLook("ao-1", stored).palette).toBe(stores("palette", palette));
	});

	it("offers the reset only on an axis somebody has actually chosen", async () => {
		render(<PetLibrary />);

		expect(
			within(screen.getByRole("group", { name: "Accessory" })).queryByRole("button", { name: /default/i }),
		).toBeNull();

		await userEvent.click(optionButton("Accessory", otherThan("hat", "ao-1")));

		expect(
			within(screen.getByRole("group", { name: "Accessory" })).getByRole("button", { name: /default/i }),
		).toBeTruthy();
	});
});

describe("housekeeping", () => {
	it("forgets the creature of a project that no longer exists", () => {
		window.localStorage.setItem(
			LOOKS_STORAGE_KEY,
			JSON.stringify({ v: 1, sessions: {}, projects: { "agent-orchestrator": "ghost", gone: "cat" } }),
		);
		window.dispatchEvent(new StorageEvent("storage", { key: null }));

		render(<PetLibrary />);

		const stored = parseProjectLooks(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(stored).toEqual({ "agent-orchestrator": "ghost" });
	});

	it("forgets the look of a session that no longer exists", () => {
		window.localStorage.setItem(
			LOOKS_STORAGE_KEY,
			JSON.stringify({ v: 1, sessions: { "ao-1": { hat: "cone" }, "gone-9": { hat: "beanie" } } }),
		);
		window.dispatchEvent(new StorageEvent("storage", { key: null }));

		render(<PetLibrary />);

		const stored = parseLookOverrides(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(Object.keys(stored)).toEqual(["ao-1"]);
	});

	it("forgets nothing while the session list has not loaded", () => {
		// A failed or in-flight fetch is not evidence that anybody's session is gone.
		useWorkspaceQuery.mockReturnValue({ data: undefined, isSuccess: false });
		const raw = JSON.stringify({ v: 1, sessions: { "gone-9": { hat: "beanie" } } });
		window.localStorage.setItem(LOOKS_STORAGE_KEY, raw);
		window.dispatchEvent(new StorageEvent("storage", { key: null }));

		render(<PetLibrary />);

		expect(window.localStorage.getItem(LOOKS_STORAGE_KEY)).toBe(raw);
	});
});

describe("arriving from a right-click on the desktop", () => {
	it("opens on the session whose Proc was clicked", () => {
		useUiStore.setState({ petLibraryRequest: "sl-1" });

		render(<PetLibrary />);

		expect(screen.getByRole("button", { name: /api rewrite/ })).toHaveAttribute("aria-current", "true");
	});

	it("forgets the request once it has been honoured, so it cannot fire twice", () => {
		useUiStore.setState({ petLibraryRequest: "sl-1" });

		render(<PetLibrary />);

		expect(useUiStore.getState().petLibraryRequest).toBeNull();
	});

	it("stays where it is when the session is not one it can show", () => {
		useUiStore.setState({ petLibraryRequest: "not-a-session" });

		render(<PetLibrary />);

		expect(screen.getByRole("button", { name: /fix the flaky test/ })).toHaveAttribute("aria-current", "true");
		expect(useUiStore.getState().petLibraryRequest).toBeNull();
	});
});
