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

function renderPalette(
	onOpenFile = vi.fn(),
	props: { enabled?: boolean; sessionId?: string; workspaceRoot?: string } = {},
) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<OpenQuicklyPalette
				sessionId={props.sessionId ?? "proj-1"}
				enabled={props.enabled}
				onOpenFile={onOpenFile}
				workspaceRoot={props.workspaceRoot}
			/>
		</QueryClientProvider>,
	);
	return onOpenFile;
}

/**
 * Stand a language server up on `window.ao.lsp` in whatever state a test needs.
 * `symbols` is the raw `workspace/symbol` payload, so the parser is exercised
 * rather than bypassed.
 */
function installLanguageServer(options: {
	state: "indexing" | "ready" | "failed";
	detail?: string;
	symbols?: unknown[];
	onRequest?: (method: string, params: unknown) => Promise<unknown>;
}) {
	const previous = (globalThis as unknown as { ao?: Record<string, unknown> }).ao;
	const bridge = {
		attach: async () => {
			if (options.state === "failed") throw new Error(options.detail ?? "gopls: spawn ENOENT");
			return { handleId: "h1", key: "go /w", state: options.state, detail: options.detail };
		},
		detach: () => undefined,
		send: (handleId: string, message: Record<string, unknown>) => {
			if (typeof message.id !== "number") return;
			const answer =
				options.onRequest?.(String(message.method), message.params) ?? Promise.resolve(options.symbols ?? []);
			void answer.then((result) => listeners.forEach((l) => l({ handleId, message: { id: message.id, result } })));
		},
		noteResult: () => undefined,
		health: async () => [],
		onMessage: (cb: (e: { handleId: string; message: Record<string, unknown> }) => void) => {
			listeners.add(cb);
			return () => listeners.delete(cb);
		},
		onState: () => () => undefined,
	};
	const listeners = new Set<(e: { handleId: string; message: Record<string, unknown> }) => void>();
	(globalThis as unknown as { ao: Record<string, unknown> }).ao = { ...previous, lsp: bridge };
	return () => {
		(globalThis as unknown as { ao?: Record<string, unknown> }).ao = previous;
	};
}

const GO_SYMBOL = {
	name: "ConfinedPath",
	kind: 12,
	containerName: "previewutil",
	location: { uri: "file:///w/internal/preview/entry.go", range: { start: { line: 210, character: 5 } } },
};

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

	// Reopening used to carry the old query with the caret at its END, so the
	// next thing typed was APPENDED: "promo", esc, ⌘⇧O, "hub" gave "promohub".
	// The query is kept on purpose (⌘⇧O then Enter re-runs the last search) —
	// what must not survive is the caret position.
	it("reopens with the last query selected, so typing replaces it", async () => {
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await user.type(searchBox(), "promo");
		await waitFor(() => expect(rows().length).toBeGreaterThan(0));

		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("combobox")).not.toBeInTheDocument());
		await pressOpenQuickly(user);

		const input = searchBox() as HTMLInputElement;
		expect(input.value).toBe("promo");
		expect([input.selectionStart, input.selectionEnd]).toEqual([0, "promo".length]);

		// `user.type` clicks first, which collapses the selection — that is the
		// test harness, not the browser. Type into the focused element as a real
		// keypress does.
		await user.keyboard("hub");
		expect((searchBox() as HTMLInputElement).value).toBe("hub");
	});

	it("reopens on the best match, not on wherever the arrow keys were left", async () => {
		const user = userEvent.setup();
		renderPalette();
		await pressOpenQuickly(user);
		await user.type(searchBox(), "tsx");
		await waitFor(() => expect(rows().length).toBeGreaterThan(1));
		await user.keyboard("{ArrowDown}");
		expect(rows()[1]).toHaveAttribute("aria-selected", "true");

		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("combobox")).not.toBeInTheDocument());
		await pressOpenQuickly(user);
		await waitFor(() => expect(rows().length).toBeGreaterThan(1));
		expect(rows()[0]).toHaveAttribute("aria-selected", "true");
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

	/**
	 * 🗝 A server that is up, answering, and returning empty must be
	 * DISTINGUISHABLE from one that is still indexing and from one that is not
	 * running. Three distinct strings, asserted, because answering NOTHING while
	 * looking healthy is what this whole stack does wrong.
	 */
	describe("the symbol section never goes silent", () => {
		it("not ready → says it is loading packages, and shows no symbol rows", async () => {
			const restore = installLanguageServer({ state: "indexing" });
			try {
				const user = userEvent.setup();
				renderPalette(vi.fn(), { workspaceRoot: "/w" });
				await pressOpenQuickly(user);
				await user.type(searchBox(), "confined");
				expect(await screen.findByText(/loading this workspace.s go packages/i)).toBeInTheDocument();
				expect(screen.queryByText(/no go symbols match/i)).not.toBeInTheDocument();
				expect(screen.queryByText("ConfinedPath")).not.toBeInTheDocument();
			} finally {
				restore();
			}
		});

		it("ready but empty → says nothing matched, naming the query", async () => {
			const restore = installLanguageServer({ state: "ready", symbols: [] });
			try {
				const user = userEvent.setup();
				renderPalette(vi.fn(), { workspaceRoot: "/w" });
				await pressOpenQuickly(user);
				await user.type(searchBox(), "confined");
				expect(await screen.findByText(/no go symbols match .confined./i)).toBeInTheDocument();
			} finally {
				restore();
			}
		});

		it("failed → says the server is not running, and why", async () => {
			const restore = installLanguageServer({ state: "failed", detail: "gopls: spawn ENOENT" });
			try {
				const user = userEvent.setup();
				renderPalette(vi.fn(), { workspaceRoot: "/w" });
				await pressOpenQuickly(user);
				await user.type(searchBox(), "confined");
				expect(await screen.findByText(/isn.t running/i)).toBeInTheDocument();
				expect(screen.getByText(/ENOENT/)).toBeInTheDocument();
			} finally {
				restore();
			}
		});

		it("no workspace root → no symbol section at all, and files still work", async () => {
			// An orchestrator session has no worktree. The FILE half must not be held
			// hostage by the half that needs a language server.
			const restore = installLanguageServer({ state: "ready", symbols: [GO_SYMBOL] });
			try {
				const user = userEvent.setup();
				renderPalette(vi.fn());
				await pressOpenQuickly(user);
				await user.type(searchBox(), "sessionview");
				await waitFor(() => expect(rows().length).toBeGreaterThan(0));
				expect(screen.queryByTestId("open-quickly-symbols")).not.toBeInTheDocument();
			} finally {
				restore();
			}
		});
	});

	describe("choosing a symbol", () => {
		it("opens through the seam WITH a column", async () => {
			const restore = installLanguageServer({ state: "ready", symbols: [GO_SYMBOL] });
			try {
				const user = userEvent.setup();
				const onOpenFile = renderPalette(vi.fn(), { workspaceRoot: "/w" });
				await pressOpenQuickly(user);
				await user.type(searchBox(), "confinedpath");
				await user.click(await screen.findByText("ConfinedPath"));
				// A column is the whole reason slice 2 left that field on the seam.
				expect(onOpenFile).toHaveBeenCalledWith({
					path: "internal/preview/entry.go",
					line: 211,
					column: 6,
					inWorkspace: true,
				});
			} finally {
				restore();
			}
		});

		it("a symbol outside the workspace opens as an absolute path", async () => {
			// Go definitions land in GOROOT constantly; dropping them would make the
			// most common ⌘⇧O hit a dead row.
			const restore = installLanguageServer({
				state: "ready",
				symbols: [
					{
						name: "Printf",
						kind: 12,
						location: { uri: "file:///usr/local/go/src/fmt/print.go", range: { start: { line: 0, character: 5 } } },
					},
				],
			});
			try {
				const user = userEvent.setup();
				const onOpenFile = renderPalette(vi.fn(), { workspaceRoot: "/w" });
				await pressOpenQuickly(user);
				await user.type(searchBox(), "printf");
				await user.click(await screen.findByText("Printf"));
				expect(onOpenFile).toHaveBeenCalledWith({
					path: "/usr/local/go/src/fmt/print.go",
					line: 1,
					column: 6,
					inWorkspace: false,
				});
			} finally {
				restore();
			}
		});
	});

	describe("a stale symbol answer is discarded, not shown late", () => {
		it("the previous query's symbols vanish the moment the query changes", async () => {
			// 🗝 The tag, not the cancellation guard, is what this pins. A request is
			// debounced, so between the keystroke and the next answer there is a
			// window in which the OLD query's rows are still in state. Without
			// checking that the answer's tag still equals the box, those rows sit
			// there as if they answered the new query - which is exactly the
			// wrong-then-right behaviour the spike hit on the symbol side.
			const restore = installLanguageServer({
				state: "ready",
				onRequest: async (_method, params) => {
					const query = (params as { query: string }).query;
					if (query === "confined") return [GO_SYMBOL];
					// Never resolves, so the assertion below runs INSIDE the window.
					return new Promise(() => {});
				},
			});
			try {
				const user = userEvent.setup();
				renderPalette(vi.fn(), { workspaceRoot: "/w" });
				await pressOpenQuickly(user);
				await user.type(searchBox(), "confined");
				// The row, not its text: a prefix match splits the name across <mark>
				// runs, so the matched characters are literally in separate elements.
				const symbolSection = () => screen.getByTestId("open-quickly-symbols");
				await waitFor(() => expect(within(symbolSection()).getAllByRole("option")).toHaveLength(1));

				await user.type(searchBox(), "x"); // now "confinedx"; its answer never arrives
				await waitFor(() => expect(within(symbolSection()).queryAllByRole("option")).toHaveLength(0));
				expect(screen.getByText(/searching symbols/i)).toBeInTheDocument();
			} finally {
				restore();
			}
		});
	});
});
