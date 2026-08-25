/**
 * What ⌘⇧O must NOT answer to.
 *
 * The palette binds a window-level keydown listener, so its matcher is the only
 * thing standing between it and every other chord the app already uses. Those
 * chords live in components this test never mounts — ⌘B in `ui/sidebar.tsx`,
 * ⌘⇧B in `SessionView.tsx`, ⌘⌥arrows in `SplitTreeView.tsx` — which is exactly
 * why the check belongs on the matcher rather than on a rendered app: a
 * collision would show up as the palette opening over an unrelated action, in a
 * session nobody was testing.
 *
 * The matcher is deliberately loose in one direction (`code === "KeyO"` OR
 * `key === "o"`, so a non-US layout and a remapped keyboard both work). These
 * are the guard rails on that looseness.
 */
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { OpenQuicklyPalette } from "./OpenQuicklyPalette";

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({ data: { available: true, paths: ["a/b/File.ts"], truncated: false } });
});

function renderPalette() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<OpenQuicklyPalette sessionId="proj-1" onOpenFile={vi.fn()} />
		</QueryClientProvider>,
	);
}

const isOpen = () => screen.queryByRole("combobox", { name: /open quickly/i }) !== null;

describe("the chords that must open it", () => {
	it.each([
		["⌘⇧O", "{Meta>}{Shift>}O{/Shift}{/Meta}"],
		["Ctrl+⇧O", "{Control>}{Shift>}O{/Shift}{/Control}"],
	])("opens on %s", async (_name, chord) => {
		const user = userEvent.setup();
		renderPalette();
		await user.keyboard(chord);
		expect(isOpen()).toBe(true);
	});
});

describe("the chords the app already uses must not open it", () => {
	it.each([
		// Every one of these is bound somewhere else in the renderer today.
		["⌘B — sidebar", "{Meta>}b{/Meta}"],
		["⌘⇧B — inspector", "{Meta>}{Shift>}B{/Shift}{/Meta}"],
		["⌘⌥→ — split focus", "{Meta>}{Alt>}{ArrowRight}{/Alt}{/Meta}"],
		["⌘1 — project switch", "{Meta>}1{/Meta}"],
		// And these are near misses on the matcher itself.
		["⌘O — no shift", "{Meta>}o{/Meta}"],
		["⌘⌥⇧O — alt held", "{Meta>}{Alt>}{Shift>}O{/Shift}{/Alt}{/Meta}"],
		["⇧O — no meta or ctrl", "{Shift>}O{/Shift}"],
		["a bare o", "o"],
		["⌘⇧P", "{Meta>}{Shift>}P{/Shift}{/Meta}"],
	])("stays shut for %s", async (_name, chord) => {
		const user = userEvent.setup();
		renderPalette();
		await user.keyboard(chord);
		expect(isOpen()).toBe(false);
	});
});

describe("a session with no worktree", () => {
	it("does not answer the shortcut at all", async () => {
		const user = userEvent.setup();
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={client}>
				<OpenQuicklyPalette sessionId="orch-1" enabled={false} onOpenFile={vi.fn()} />
			</QueryClientProvider>,
		);
		await user.keyboard("{Meta>}{Shift>}O{/Shift}{/Meta}");
		expect(isOpen()).toBe(false);
		// An orchestrator has no worktree, so nothing should have been indexed.
		expect(getMock).not.toHaveBeenCalled();
	});
});
