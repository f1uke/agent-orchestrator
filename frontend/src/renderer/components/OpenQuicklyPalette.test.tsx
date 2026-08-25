import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { OpenQuicklyPalette } from "./OpenQuicklyPalette";

const PATHS = [
	"backend/internal/service/session/workspace_file.go",
	"frontend/src/renderer/components/SessionView.tsx",
	"frontend/src/renderer/components/OpenQuicklyPalette.tsx",
	"frontend/node_modules/react/index.js",
	"docs/very/deeply/nested/directory/that/keeps/going/for/quite/a/while/DeeplyNestedConfiguration.tsx",
	"screenshots/OG-Promotion-Hub 2.png",
	"PromotionHub/PromotionHubViewController.swift",
];

let body: Record<string, unknown> = { available: true, paths: PATHS, truncated: false };
/** Resolved per request, so a test can hold the index in flight. */
let respond: ((value: { data: unknown }) => void) | null = null;

beforeEach(() => {
	body = { available: true, paths: PATHS, truncated: false };
	respond = null;
	getMock.mockReset().mockImplementation(async (path: string) => {
		if (!path.includes("/workspace/files")) return { data: null };
		if (respond) return new Promise((resolve) => (respond = resolve));
		return { data: body };
	});
});

function renderPalette(onOpenFile = vi.fn(), props: { enabled?: boolean; sessionId?: string } = {}) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<OpenQuicklyPalette sessionId={props.sessionId ?? "proj-1"} enabled={props.enabled} onOpenFile={onOpenFile} />
		</QueryClientProvider>,
	);
	return onOpenFile;
}

/** ⌘⇧O, the way the window listener sees it. */
async function pressOpenQuickly(user: ReturnType<typeof userEvent.setup>) {
	await user.keyboard("{Meta>}{Shift>}O{/Shift}{/Meta}");
}

const searchBox = () => screen.getByRole("combobox", { name: /open quickly/i });
const rows = () => screen.queryAllByRole("option");

describe("OpenQuicklyPalette", () => {
	it("opens on ⌘⇧O and closes on a second press", async () => {
		const user = userEvent.setup();
		renderPalette();
		expect(screen.queryByRole("combobox")).not.toBeInTheDocument();

		await pressOpenQuickly(user);
		expect(searchBox()).toBeInTheDocument();

		await pressOpenQuickly(user);
		await waitFor(() => expect(screen.queryByRole("combobox")).not.toBeInTheDocument());
	});

	it("does not bind the shortcut for a session with no worktree", async () => {
		const user = userEvent.setup();
		renderPalette(vi.fn(), { enabled: false });
		await pressOpenQuickly(user);
		expect(screen.queryByRole("combobox")).not.toBeInTheDocument();
	});

	it("does not index the workspace until the palette is opened", async () => {
		const user = userEvent.setup();
		renderPalette();
		expect(getMock).not.toHaveBeenCalled();
		await pressOpenQuickly(user);
		await waitFor(() => expect(getMock).toHaveBeenCalled());
	});

	it("ranks matches and opens the chosen file through the seam", async () => {
		const user = userEvent.setup();
		const onOpenFile = renderPalette();
		await pressOpenQuickly(user);
		await user.type(searchBox(), "promohub");
		await waitFor(() => expect(rows().length).toBeGreaterThan(0));

		// Rule 1 + rule 2: the Swift file, not the .png that also contains the letters.
		expect(rows()[0]).toHaveAttribute("title", "PromotionHub/PromotionHubViewController.swift");

		await user.keyboard("{Enter}");
		expect(onOpenFile).toHaveBeenCalledWith({
			path: "PromotionHub/PromotionHubViewController.swift",
			inWorkspace: true,
		});
	});

	it("moves the selection with the arrow keys and opens the highlighted row", async () => {
		const user = userEvent.setup();
		const onOpenFile = renderPalette();
		await pressOpenQuickly(user);
		await user.type(searchBox(), "tsx");
		await waitFor(() => expect(rows().length).toBeGreaterThan(1));

		const second = rows()[1].getAttribute("title");
		await user.keyboard("{ArrowDown}{Enter}");
		expect(onOpenFile).toHaveBeenCalledWith({ path: second, inWorkspace: true });
	});

	it("opens a clicked row", async () => {
		const user = userEvent.setup();
		const onOpenFile = renderPalette();
		await pressOpenQuickly(user);
		await user.type(searchBox(), "sessionview");
		await waitFor(() => expect(rows().length).toBeGreaterThan(0));
		await user.click(rows()[0]);
		expect(onOpenFile).toHaveBeenCalledWith({
			path: "frontend/src/renderer/components/SessionView.tsx",
			inWorkspace: true,
		});
	});

	it("marks the matched characters in the row", async () => {
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await user.type(searchBox(), "sesview");
		await waitFor(() => expect(rows().length).toBeGreaterThan(0));
		const marks = within(rows()[0]).getAllByText((_, node) => node?.tagName === "MARK");
		expect(marks.map((m) => m.textContent).join("")).toBe("SesView");
	});

	// The bug the spike hit on the symbol side, structurally excluded here:
	// results are derived from the CURRENT query, so there is no in-flight
	// request that a fast typist can outrun.
	it("never shows a previous query's results, however fast the query changes", async () => {
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		const input = searchBox();
		await user.type(input, "promohub");
		await waitFor(() => expect(rows().length).toBeGreaterThan(0));

		await user.clear(input);
		await user.type(input, "sessionview");
		// Not "a Promotion row eventually disappears" — no render in between may
		// ever have carried one, so assert on the settled list directly.
		await waitFor(() => {
			expect(rows()[0]).toHaveAttribute("title", "frontend/src/renderer/components/SessionView.tsx");
		});
		expect(rows().every((r) => !r.getAttribute("title")?.includes("Promotion"))).toBe(true);
	});

	it("says so when nothing matches, instead of showing an empty box", async () => {
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await user.type(searchBox(), "zzqqxx");
		await waitFor(() => expect(screen.getByText(/no files match/i)).toBeInTheDocument());
		expect(rows()).toHaveLength(0);
	});

	it("prompts instead of listing the whole tree for an empty query", async () => {
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await waitFor(() => expect(screen.getByText(/type to search/i)).toBeInTheDocument());
		expect(rows()).toHaveLength(0);
	});

	it("shows the deep path's file name in full and lets the directory truncate", async () => {
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await user.type(searchBox(), "deeplynested");
		await waitFor(() => expect(rows().length).toBeGreaterThan(0));
		// The name element carries the whole basename — truncation is the
		// stylesheet's job on the DIRECTORY element, never a JS-side slice of the
		// one part that identifies the file.
		expect(rows()[0].querySelector(".open-quickly__name")?.textContent).toBe("DeeplyNestedConfiguration.tsx");
		expect(rows()[0].querySelector(".open-quickly__dir")?.textContent).toBe(
			"docs/very/deeply/nested/directory/that/keeps/going/for/quite/a/while/",
		);
	});

	it("reports an index that is still loading rather than an empty result", async () => {
		respond = () => {};
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await waitFor(() => expect(screen.getByText(/indexing this workspace/i)).toBeInTheDocument());
	});

	it("explains a worktree that is gone rather than looking like an empty project", async () => {
		body = { available: false, reason: "no_workspace", paths: [], truncated: false };
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await waitFor(() => expect(screen.getByText(/no longer on disk/i)).toBeInTheDocument());
	});

	it("says out loud when the index was capped", async () => {
		body = { available: true, paths: PATHS, truncated: true };
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await waitFor(() => expect(screen.getByText(/indexed the first/i)).toBeInTheDocument());
	});

	it("surfaces an index failure in the palette", async () => {
		getMock.mockReset().mockResolvedValue({ error: new Error("daemon is down") });
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await waitFor(() => expect(screen.getByText(/daemon is down/i)).toBeInTheDocument());
	});
});
