import { beforeEach, describe, expect, it, vi } from "vitest";
import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { APPEARANCE_AXES, castForSession, defaultLook } from "../../../companion/cast";
import { LOOKS_STORAGE_KEY, parseLookOverrides, resolveLook } from "../../../companion/look-store";
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

/** The buttons under one axis heading, e.g. every colour. */
function optionsUnder(axisName: string) {
	return within(screen.getByRole("group", { name: axisName })).getAllByRole("button");
}

function optionButton(axisName: string, optionName: string) {
	return within(screen.getByRole("group", { name: axisName })).getByRole("button", {
		name: new RegExp(optionName, "i"),
	});
}

describe("choosing which session to dress", () => {
	it("lists the live sessions under the project they belong to", () => {
		render(<PetLibrary />);

		expect(screen.getByText("agent-orchestrator")).toBeTruthy();
		expect(screen.getByText("starlight")).toBeTruthy();
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

describe("the axes", () => {
	it("draws one section per axis in the registry, not two it names itself", () => {
		// A third axis - the new character types - must appear here by being added to
		// APPEARANCE_AXES, with no change to this component.
		render(<PetLibrary />);

		for (const axis of APPEARANCE_AXES) {
			expect(screen.getByRole("group", { name: axis.name }), axis.id).toBeTruthy();
			expect(optionsUnder(axis.name).length, axis.id).toBe(axis.options.length);
		}
	});

	it("marks the option a session is wearing as in use", () => {
		render(<PetLibrary />);
		const mine = castForSession("ao-1");

		expect(optionButton("Colour", mine.palette)).toHaveAttribute("aria-pressed", "true");
		expect(optionButton("Accessory", mine.hatId)).toHaveAttribute("aria-pressed", "true");
		expect(within(optionButton("Colour", mine.palette)).getByText(/in use/i)).toBeTruthy();
	});
});

describe("picking a look", () => {
	async function pickHat(hat: string) {
		render(<PetLibrary />);
		await userEvent.click(optionButton("Accessory", hat));
	}

	const otherThan = (axisId: "palette" | "hat", ref: string) =>
		APPEARANCE_AXES.find((axis) => axis.id === axisId)!.options.find(
			(option) => option.id !== defaultLook(ref)[axisId],
		)!.id;

	it("changes the hat and leaves the colour on the hash", async () => {
		const wanted = otherThan("hat", "ao-1");
		await pickHat(wanted);

		const stored = parseLookOverrides(window.localStorage.getItem(LOOKS_STORAGE_KEY));
		expect(resolveLook("ao-1", stored).hat).toBe(wanted);
		expect(resolveLook("ao-1", stored).palette).toBe(defaultLook("ao-1").palette);
	});

	it("moves the in-use mark to what was picked", async () => {
		const wanted = otherThan("hat", "ao-1");
		await pickHat(wanted);

		expect(optionButton("Accessory", wanted)).toHaveAttribute("aria-pressed", "true");
		expect(optionButton("Accessory", defaultLook("ao-1").hat)).toHaveAttribute("aria-pressed", "false");
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
		expect(resolveLook("ao-1", stored).palette).toBe(palette);
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
