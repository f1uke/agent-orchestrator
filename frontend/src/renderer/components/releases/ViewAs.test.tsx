import { fireEvent, render, screen } from "@testing-library/react";
import { describe, expect, it, vi } from "vitest";
import { demoBoard, unknownIdentityBoard } from "../../lib/releases/fixture";
import { ReleasesCapabilitiesProvider } from "./ReleasesCapabilities";
import { ViewAs } from "./ViewAs";

const TODAY = "2026-10-14";

function open(board: ReturnType<typeof demoBoard>, readOnly: boolean, person?: string) {
	const onPick = vi.fn();
	render(
		<ReleasesCapabilitiesProvider readOnly={readOnly}>
			<ViewAs board={board} person={person} filtering={false} onPick={onPick} />
		</ReleasesCapabilitiesProvider>,
	);
	fireEvent.click(screen.getByTestId("view-as"));
	const options = screen.getAllByTestId("view-as-option");
	return { options, ids: options.map((o) => o.getAttribute("data-person")), onPick };
}

describe("the View as picker", () => {
	it("lists Everyone, then Me for the monitor's own person, then the others, in the app", () => {
		const { options, ids } = open(demoBoard(TODAY), false);
		expect(ids.slice(0, 2)).toEqual(["", "ada"]);
		expect(options[1].textContent).toContain("Me");
		expect(options[1].textContent).toContain("Ada Example");
	});

	it("shows no Me in a read-only host and lists the monitor's own person by name among the rest", () => {
		const { options, ids } = open(demoBoard(TODAY), true);
		expect(ids[0]).toBe("");
		expect(ids).toContain("ada");
		const names = options.slice(1).map((o) => o.querySelector(".nm")?.textContent ?? "");
		expect(names).toEqual([...names].sort((a, b) => a.localeCompare(b)));
		for (const o of options) expect(o.textContent).not.toMatch(/\bMe\b/);
		const ada = options.find((o) => o.getAttribute("data-person") === "ada");
		expect(ada?.textContent).toContain("Ada Example");
	});

	it("still picks the monitor's own person by id in a read-only host", () => {
		const { options, onPick } = open(demoBoard(TODAY), true);
		fireEvent.click(options.find((o) => o.getAttribute("data-person") === "ada") as HTMLElement);
		expect(onPick).toHaveBeenCalledWith("ada");
	});

	it("explains an unread identity under Me in the app, and has no Me to explain in a read-only host", () => {
		open(unknownIdentityBoard(TODAY), false);
		expect(screen.getByText(/Me needs your Jira and GitLab accounts/)).toBeTruthy();
	});

	it("leaves out the unread identity note in a read-only host", () => {
		open(unknownIdentityBoard(TODAY), true);
		expect(screen.queryByText(/Me needs/)).toBeNull();
	});
});
