import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { WikiPage, elideVaultPath, resolveNotePath } from "./WikiPage";
import { buildTree, compactAge, summarise } from "./WikiVaultRail";
import { runningFor } from "./WikiAgentControl";

const getMock = vi.fn();
const postMock = vi.fn();
const deleteMock = vi.fn();

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: (...args: unknown[]) => getMock(...args),
		POST: (...args: unknown[]) => postMock(...args),
		DELETE: (...args: unknown[]) => deleteMock(...args),
	},
	apiErrorMessage: (error: unknown) => (error as { message?: string })?.message ?? "failed",
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	hasTrustedApiBaseUrl: () => true,
	subscribeApiBaseUrl: () => () => undefined,
}));
vi.mock("../hooks/useDaemonStatus", () => ({ useDaemonStatus: () => ({ state: "ready", port: 3001 }) }));
// The terminal itself is xterm over a live socket; the page's job is to decide
// WHETHER it is shown and on which handle, which is what these assert.
vi.mock("./WikiTerminal", () => ({
	WikiTerminal: ({ handleId }: { handleId: string }) => <div data-testid="wiki-terminal">{handleId}</div>,
}));
vi.mock("../lib/note/highlight", () => ({
	highlightCode: () => Promise.resolve(null),
	grammarFor: () => "",
}));

const VAULT = "/Users/someone/Notes";

const NOTES = [
	{ path: "index.md", size: 20, modifiedAt: new Date().toISOString() },
	{ path: "agents/compaction.md", size: 40, modifiedAt: new Date().toISOString() },
	{ path: "llm/context-window.md", size: 30, modifiedAt: new Date().toISOString() },
];

function wikiStatus(overrides: Record<string, unknown> = {}) {
	return {
		data: {
			configured: true,
			vaultPath: VAULT,
			displayPath: "~/Notes",
			harness: "claude-code",
			running: true,
			handleId: "ao-wiki",
			startedAt: new Date().toISOString(),
			...overrides,
		},
		error: undefined,
	};
}

function routeGets(status = wikiStatus(), note?: Record<string, unknown>, notes: { path: string }[] = NOTES) {
	getMock.mockImplementation((path: string) => {
		if (path === "/api/v1/wiki") return Promise.resolve(status);
		if (path === "/api/v1/wiki/files") return Promise.resolve({ data: { notes, truncated: false } });
		if (path === "/api/v1/wiki/file")
			return Promise.resolve({
				data: note ?? { path: "agents/compaction.md", content: "# Compaction\n\nbody\n", size: 40, backlinks: [] },
			});
		if (path === "/api/v1/agents")
			return Promise.resolve({
				data: {
					supported: [
						{ id: "claude-code", label: "Claude Code" },
						{ id: "codex", label: "Codex" },
					],
					installed: [{ id: "claude-code", label: "Claude Code" }],
					authorized: [],
				},
			});
		return Promise.resolve({ data: {} });
	});
}

function renderPage() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<WikiPage />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset();
	postMock.mockReset().mockResolvedValue({ data: wikiStatus().data });
	deleteMock.mockReset().mockResolvedValue({ data: wikiStatus({ running: false, handleId: "" }).data });
	localStorage.clear();
});

describe("WikiPage — before a vault is set up", () => {
	it("explains where to set the vault path instead of showing an empty page", async () => {
		routeGets(
			wikiStatus({ configured: false, vaultPath: "", harness: "", running: false, handleId: "", startedAt: "" }),
		);
		renderPage();
		expect(await screen.findByText(/No vault is set up/i)).toBeInTheDocument();
		expect(screen.getByText(/Settings › System › Wiki vault/)).toBeInTheDocument();
	});
});

describe("WikiPage — the agent", () => {
	it("shows the terminal on the vault's handle while an agent runs", async () => {
		routeGets();
		renderPage();
		expect(await screen.findByTestId("wiki-terminal")).toHaveTextContent("ao-wiki");
		// The topbar shows the home-relative path, with the exact one on its title.
		expect(screen.getByText("~/Notes")).toHaveAttribute("title", VAULT);
	});

	it("shows the picker, not the terminal, with no agent running", async () => {
		routeGets(wikiStatus({ running: false, handleId: "" }));
		renderPage();
		expect(await screen.findByText(/Which agent should open your notes\?/i)).toBeInTheDocument();
		expect(screen.queryByTestId("wiki-terminal")).toBeNull();
	});

	it("starts the chosen agent", async () => {
		routeGets(wikiStatus({ running: false, handleId: "" }));
		renderPage();
		await screen.findByText(/Which agent should open your notes\?/i);
		await userEvent.click(await screen.findByRole("button", { name: /codex/i }));
		await waitFor(() => expect(postMock).toHaveBeenCalledWith("/api/v1/wiki/agent", { body: { harness: "codex" } }));
	});

	// The design's whole agent control is one pill; there must be no separate
	// Restart/Stop buttons cluttering the topbar.
	it("carries restart, switch and stop in the one pill's menu", async () => {
		routeGets();
		renderPage();
		await userEvent.click(await screen.findByRole("button", { name: /claude-code/i }));
		expect(await screen.findByRole("menuitem", { name: /restart/i })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /switch agent/i })).toBeInTheDocument();
		expect(screen.getByRole("menuitem", { name: /stop agent/i })).toBeInTheDocument();
	});

	it("stopping returns the centre to the picker and leaves the rail working", async () => {
		routeGets();
		renderPage();
		await userEvent.click(await screen.findByRole("button", { name: /claude-code/i }));
		await userEvent.click(await screen.findByRole("menuitem", { name: /stop agent/i }));

		await waitFor(() => expect(deleteMock).toHaveBeenCalledWith("/api/v1/wiki/agent", {}));
		expect(await screen.findByText(/Which agent should open your notes\?/i)).toBeInTheDocument();
		// The vault is still listed: reading notes never needed an agent.
		expect(screen.getByText("index.md")).toBeInTheDocument();
	});
});

describe("WikiPage — reading a note", () => {
	it("opens a clicked note in the CENTRE, replacing the terminal", async () => {
		routeGets();
		renderPage();
		await screen.findByTestId("wiki-terminal");

		await userEvent.click(await screen.findByRole("button", { name: /index\.md/ }));

		expect(await screen.findByRole("heading", { level: 1, name: "Compaction" })).toBeInTheDocument();
		expect(screen.queryByTestId("wiki-terminal")).toBeNull();
	});

	it("closing the note returns to the still-running terminal", async () => {
		routeGets();
		renderPage();
		await userEvent.click(await screen.findByRole("button", { name: /index\.md/ }));
		await screen.findByRole("heading", { level: 1, name: "Compaction" });

		await userEvent.click(screen.getByRole("button", { name: "Close note" }));
		expect(await screen.findByTestId("wiki-terminal")).toHaveTextContent("ao-wiki");
	});

	it("keeps the agent pill reachable while a note is open", async () => {
		routeGets();
		renderPage();
		await userEvent.click(await screen.findByRole("button", { name: /index\.md/ }));
		await screen.findByRole("heading", { level: 1, name: "Compaction" });
		expect(screen.getByRole("button", { name: /claude-code/i })).toBeInTheDocument();
	});

	it("lists the notes that link here", async () => {
		routeGets(wikiStatus(), {
			path: "agents/compaction.md",
			content: "# Compaction\n",
			size: 12,
			backlinks: ["index.md", "llm/context-window.md"],
		});
		renderPage();
		await userEvent.click(await screen.findByRole("button", { name: /index\.md/ }));
		expect(await screen.findByText("Linked from")).toBeInTheDocument();
		// Named the way the note itself is, not the way a wikilink is spelled:
		// the brackets are syntax, and the vault's own editor never shows them.
		expect(screen.getByRole("button", { name: "context-window" })).toBeInTheDocument();
	});

	it("back is disabled with nowhere to go, rather than disappearing", async () => {
		routeGets();
		renderPage();
		await userEvent.click(await screen.findByRole("button", { name: /index\.md/ }));
		expect(await screen.findByRole("button", { name: "Back" })).toBeDisabled();
		expect(screen.getByRole("button", { name: "Forward" })).toBeDisabled();
	});
});

describe("WikiPage — the tree's sort direction", () => {
	// Two levels deep on purpose: a toggle that only reordered the root would
	// pass a shallower fixture and still leave nested folders untouched.
	const NESTED = [
		{ path: "work/nested/alpha.md", size: 10, modifiedAt: new Date().toISOString() },
		{ path: "work/nested/zeta.md", size: 10, modifiedAt: new Date().toISOString() },
		{ path: "work/nested/inner/deep.md", size: 10, modifiedAt: new Date().toISOString() },
	];

	function rowNames(container: HTMLElement): string[] {
		return Array.from(container.querySelectorAll(".wiki-rail__tree .wiki-rail__name")).map(
			(node) => node.textContent ?? "",
		);
	}

	async function openNested() {
		await userEvent.click(await screen.findByRole("button", { name: /^nested/ }));
	}

	it("reverses the order inside a nested folder, not just at the root", async () => {
		routeGets(wikiStatus(), undefined, NESTED);
		const { container } = renderPage();
		await openNested();
		expect(rowNames(container)).toEqual(["work", "nested", "inner", "alpha.md", "zeta.md"]);

		await userEvent.click(screen.getByRole("button", { name: /Sorted A to Z/ }));

		expect(rowNames(container)).toEqual(["work", "nested", "zeta.md", "alpha.md", "inner"]);
	});

	it("keeps the chosen direction across a remount, the way the folders are kept", async () => {
		routeGets(wikiStatus(), undefined, NESTED);
		const first = renderPage();
		await openNested();
		await userEvent.click(screen.getByRole("button", { name: /Sorted A to Z/ }));
		expect(rowNames(first.container)).toEqual(["work", "nested", "zeta.md", "alpha.md", "inner"]);
		first.unmount();

		const second = renderPage();
		await screen.findByRole("button", { name: /Sorted Z to A/ });
		await waitFor(() => expect(rowNames(second.container)).toEqual(["work", "nested", "zeta.md", "alpha.md", "inner"]));
	});

	it("says which way the tree runs rather than making you click to find out", async () => {
		routeGets(wikiStatus(), undefined, NESTED);
		renderPage();
		const button = await screen.findByRole("button", { name: /Sorted A to Z/ });
		expect(button).toHaveAttribute("aria-pressed", "false");

		await userEvent.click(button);

		expect(screen.getByRole("button", { name: /Sorted Z to A/ })).toHaveAttribute("aria-pressed", "true");
		expect(localStorage.getItem("ao.wiki.sort")).toBe("desc");
	});
});

describe("WikiPage — the rail's search", () => {
	it("finds a note by name", async () => {
		routeGets();
		renderPage();
		await userEvent.click(await screen.findByRole("button", { name: /^Search$/ }));
		await userEvent.type(screen.getByPlaceholderText(/Find a note/i), "context");
		expect(await screen.findByRole("button", { name: /llm\/context-window\.md/ })).toBeInTheDocument();
	});
});

describe("elideVaultPath", () => {
	it("leaves a short path alone", () => {
		expect(elideVaultPath("~/Notes/Vault")).toBe("~/Notes/Vault");
	});

	// The LAST segment is the vault's name — the part worth reading — so the
	// middle goes, not the tail.
	it("elides the middle of a deep path and keeps the name", () => {
		expect(elideVaultPath("/private/tmp/a/b/c/scratchpad/vault")).toBe("/private/…/scratchpad/vault");
	});

	it("keeps a leading slash where there was one", () => {
		expect(elideVaultPath("a/b/c/d/e/f")).toBe("a/…/e/f");
	});
});

describe("resolveNotePath — a wikilink names a note, not a path", () => {
	const files = { notes: NOTES, truncated: false };

	it("resolves a bare basename", () => {
		expect(resolveNotePath("compaction", files)).toBe("agents/compaction.md");
	});

	it("resolves a full path with and without its extension", () => {
		expect(resolveNotePath("agents/compaction.md", files)).toBe("agents/compaction.md");
		expect(resolveNotePath("agents/compaction", files)).toBe("agents/compaction.md");
	});

	it("is case-insensitive", () => {
		expect(resolveNotePath("Context-Window", files)).toBe("llm/context-window.md");
	});

	it("returns null for a note that is not there", () => {
		expect(resolveNotePath("nothing", files)).toBeNull();
		expect(resolveNotePath("", files)).toBeNull();
	});
});

describe("the vault tree", () => {
	it("puts folders before files, each alphabetical", () => {
		const tree = buildTree([{ path: "zeta.md" }, { path: "alpha.md" }, { path: "work/b.md" }, { path: "agents/a.md" }]);
		expect(tree.map((node) => node.name)).toEqual(["agents", "work", "alpha.md", "zeta.md"]);
	});

	// The toggle inverts the one rule the tree already has — it does not sort on
	// something else — and it has to reach every level, not just the top one.
	it("runs backwards at every level when the order is reversed", () => {
		const notes = [
			{ path: "zeta.md" },
			{ path: "alpha.md" },
			{ path: "work/b.md" },
			{ path: "work/a.md" },
			{ path: "work/inner/deep.md" },
			{ path: "agents/a.md" },
		];
		const tree = buildTree(notes, "desc");
		expect(tree.map((node) => node.name)).toEqual(["zeta.md", "alpha.md", "work", "agents"]);
		const work = tree.find((node) => node.name === "work");
		expect(work?.kind === "folder" && work.children.map((child) => child.name)).toEqual(["b.md", "a.md", "inner"]);
	});

	it("counts every file beneath a folder, not just its direct children", () => {
		const tree = buildTree([{ path: "a/b/one.md" }, { path: "a/two.md" }]);
		const first = tree[0];
		expect(first.kind === "folder" && first.noteCount).toBe(2);
	});

	it("summarises notes and folders the way the rail says it", () => {
		expect(summarise([{ path: "a/one.md" }, { path: "a/b/two.md" }, { path: "c/image.png" }])).toEqual({
			notes: 2,
			folders: 3,
		});
	});
});

describe("compactAge", () => {
	const now = Date.parse("2026-09-02T12:00:00Z");

	it("is terse enough for a 30px column", () => {
		expect(compactAge("2026-09-02T11:30:00Z", now)).toBe("30m");
		expect(compactAge("2026-09-02T02:00:00Z", now)).toBe("10h");
		expect(compactAge("2026-08-31T12:00:00Z", now)).toBe("2d");
		expect(compactAge("2026-08-12T12:00:00Z", now)).toBe("3w");
	});

	it("says nothing when there is no timestamp", () => {
		expect(compactAge(undefined, now)).toBe("");
		expect(compactAge("not a date", now)).toBe("");
	});
});

describe("runningFor", () => {
	const now = Date.parse("2026-09-02T12:00:00Z");

	it("reads as hours and minutes", () => {
		expect(runningFor("2026-09-02T09:46:00Z", now)).toBe("Running 2h 14m");
		expect(runningFor("2026-09-02T11:30:00Z", now)).toBe("Running 30m");
		expect(runningFor("2026-09-02T11:59:50Z", now)).toBe("Just started");
	});

	it("degrades without a start time rather than showing a wrong number", () => {
		expect(runningFor(undefined, now)).toBe("Running");
	});
});
