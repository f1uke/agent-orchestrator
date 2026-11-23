import { screen, within } from "@testing-library/react";
import type { UserEvent } from "@testing-library/user-event";
import { expect, onTestFinished, vi } from "vitest";

const XCODE_NAME = "App.xcworkspace";
const MAC_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)";

/**
 * Makes the "Open in…" menu reachable in jsdom: a Mac user agent (the menu is
 * macOS-only) and launchers that report an Xcode workspace and VS Code, so every
 * item the terminal toolbar can offer is on screen.
 */
export function stubOpenIn() {
	vi.spyOn(window.navigator, "userAgent", "get").mockReturnValue(MAC_UA);
	const openIn = window.ao!.openIn;
	const original = { ...openIn };
	onTestFinished(() => {
		Object.assign(openIn, original);
		vi.restoreAllMocks();
	});
	const launchers = {
		terminal: vi.fn().mockResolvedValue(undefined),
		finder: vi.fn().mockResolvedValue(undefined),
		xcode: vi.fn().mockResolvedValue(undefined),
		editor: vi.fn().mockResolvedValue(undefined),
	};
	// Mutated in place: the shared bridge captures `window.ao` once at module load.
	openIn.detectTargets = async (directory: string) => ({
		hasVSCode: true,
		xcode: { name: XCODE_NAME, path: `${directory}/${XCODE_NAME}` },
	});
	Object.assign(openIn, launchers);
	return launchers;
}

/**
 * The terminal session's "Open in…" menu, item by item: the same labels in the
 * same order, each launching on `directory`. Every placement of the menu is held
 * to this one list.
 */
export async function expectTerminalOpenInMenu(
	user: UserEvent,
	launchers: ReturnType<typeof stubOpenIn>,
	directory: string,
	scope: HTMLElement = document.body,
) {
	const items: Array<[string, ReturnType<typeof vi.fn>, string]> = [
		["Open in Terminal", launchers.terminal, directory],
		["Open in Finder", launchers.finder, directory],
		[`Open ${XCODE_NAME}`, launchers.xcode, `${directory}/${XCODE_NAME}`],
		["Open in Visual Studio Code", launchers.editor, directory],
	];
	const trigger = within(scope).getByRole("button", { name: "Open in…" });

	await user.click(trigger);
	await screen.findByRole("menuitem", { name: `Open ${XCODE_NAME}` });
	expect(screen.getAllByRole("menuitem").map((item) => item.textContent)).toEqual(items.map(([label]) => label));
	await user.keyboard("{Escape}");

	for (const [label, launcher, target] of items) {
		await user.click(trigger);
		await user.click(await screen.findByRole("menuitem", { name: label }));
		expect(launcher).toHaveBeenCalledWith(target);
	}
}
