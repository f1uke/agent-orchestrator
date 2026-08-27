import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock } = vi.hoisted(() => ({ getMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { SearchPanel } from "./SearchPanel";
import { TooltipProvider } from "./ui/tooltip";

type Query = Record<string, unknown>;

/** The queries the panel actually sent, in order — the debounce's own evidence. */
let sent: Query[] = [];
let body: Record<string, unknown> = {};
/** Set to hold a response in flight, so a test can outrun the search. */
let respond: ((value: { data: unknown }) => void) | null = null;

const hit = (over: Record<string, unknown> = {}) => ({
	line: 12,
	// "class LoginViewModel {" — `ViewModel` is the 12th UTF-16 unit (1-based
	// column) and sits at offsets 11..20 (0-based) in the preview.
	column: 12,
	endColumn: 21,
	preview: "class LoginViewModel {",
	previewStart: 11,
	previewEnd: 20,
	...over,
});

function results(over: Record<string, unknown> = {}) {
	return {
		available: true,
		query: "ViewModel",
		files: [{ path: "App/Login.swift", matches: [hit()], total: 1, truncated: false }],
		totalMatches: 1,
		totalFiles: 1,
		filesSearched: 4488,
		truncated: false,
		...over,
	};
}

beforeEach(() => {
	sent = [];
	respond = null;
	body = results();
	getMock.mockReset().mockImplementation(async (path: string, init: { params?: { query?: Query } }) => {
		if (!path.includes("/workspace/search")) return { data: null };
		sent.push(init.params?.query ?? {});
		if (respond) return new Promise((resolve) => (respond = resolve));
		return { data: body };
	});
});

function renderPanel(props: Partial<React.ComponentProps<typeof SearchPanel>> = {}) {
	const onOpenHit = props.onOpenHit ?? vi.fn();
	const onExit = props.onExit ?? vi.fn();
	// The panel only ever renders inside FilesPanel's provider, which is where
	// its option toggles get their tooltips from.
	render(
		<TooltipProvider delayDuration={0}>
			<SearchPanel active onExit={onExit} onOpenHit={onOpenHit} sessionId="ao-1" {...props} />
		</TooltipProvider>,
	);
	return { onOpenHit, onExit };
}

const box = () => screen.getByRole("searchbox", { name: "Search in project" });

describe("SearchPanel", () => {
	it("takes focus on arrival, so ⌘⇧F can be typed into straight away", async () => {
		renderPanel();
		await waitFor(() => expect(box()).toHaveFocus());
	});

	it("arrives pre-filled from the editor's selection, selected so the next keystroke replaces it", async () => {
		renderPanel({ seed: "ViewModel" });
		await waitFor(() => expect(box()).toHaveValue("ViewModel"));
		// #247's trap in reverse: keeping the query is deliberate (Xcode re-runs
		// the last search), which is exactly why it has to arrive SELECTED.
		const input = box() as HTMLInputElement;
		expect(input.selectionStart).toBe(0);
		expect(input.selectionEnd).toBe("ViewModel".length);
	});

	it("searches what was typed and lists the matches under their file", async () => {
		renderPanel();
		await userEvent.type(box(), "ViewModel");
		await waitFor(() => expect(screen.getByTitle("App/Login.swift")).toBeInTheDocument());
		// The header splits the path so the filename is what the eye scans; the
		// match row draws the line with the hit marked inside it.
		expect(screen.getByText("Login.swift")).toBeInTheDocument();
		expect(screen.getByText("App")).toBeInTheDocument();
		expect(screen.getByText("ViewModel").tagName).toBe("MARK");
		expect(screen.getByText("1 result in 1 file")).toBeInTheDocument();
	});

	it("does not ask once per keystroke — a fast typist gets ONE search", async () => {
		// The measured cost of one full search on a real 7,000-file project is
		// 0.79 CPU-seconds. Nine of them for one word is the thing the debounce and
		// the abort exist to prevent.
		renderPanel();
		await userEvent.type(box(), "ViewModel");
		await waitFor(() => expect(sent.filter((q) => q.q !== "")).toHaveLength(1));
		expect(sent.at(-1)?.q).toBe("ViewModel");
	});

	it("opens a hit at its line AND column", async () => {
		const { onOpenHit } = renderPanel();
		await userEvent.type(box(), "ViewModel");
		await waitFor(() => expect(screen.getByTitle("App/Login.swift:12:12")).toBeInTheDocument());
		await userEvent.click(screen.getByTitle("App/Login.swift:12:12"));
		// Line 1 would be the bug: the reader asked for a MATCH, not a file.
		expect(onOpenHit).toHaveBeenCalledWith({ path: "App/Login.swift", line: 12, column: 12 });
	});

	it("folds a file away and back", async () => {
		body = results({
			files: [{ path: "App/Login.swift", matches: [hit(), hit({ line: 40 })], total: 2, truncated: false }],
			totalMatches: 2,
		});
		renderPanel();
		await userEvent.type(box(), "ViewModel");
		await waitFor(() => expect(screen.getAllByRole("treeitem")).toHaveLength(3));
		await userEvent.click(screen.getByTitle("App/Login.swift"));
		expect(screen.getAllByRole("treeitem")).toHaveLength(1);
		await userEvent.click(screen.getByTitle("App/Login.swift"));
		expect(screen.getAllByRole("treeitem")).toHaveLength(3);
	});

	it("sends each toggle, so the panel and the engine agree on the question", async () => {
		renderPanel();
		await userEvent.type(box(), "ViewModel");
		await waitFor(() => expect(sent.length).toBeGreaterThan(0));
		await userEvent.click(screen.getByRole("button", { name: "Match case" }));
		await userEvent.click(screen.getByRole("button", { name: "Whole word" }));
		await userEvent.click(screen.getByRole("button", { name: "Regular expression" }));
		await waitFor(() => expect(sent.at(-1)).toMatchObject({ matchCase: true, wholeWord: true, regex: true }));
		expect(screen.getByRole("button", { name: "Match case" })).toHaveAttribute("aria-pressed", "true");
	});

	it("shows the include/exclude fields only once asked for, and sends them", async () => {
		renderPanel();
		await userEvent.type(box(), "ViewModel");
		expect(screen.queryByLabelText("Files to include")).not.toBeInTheDocument();
		await userEvent.click(screen.getByRole("button", { name: "Files to include and exclude" }));
		await userEvent.type(screen.getByLabelText("Files to include"), "*.swift");
		await userEvent.type(screen.getByLabelText("Files to exclude"), "Pods");
		await waitFor(() => expect(sent.at(-1)).toMatchObject({ include: "*.swift", exclude: "Pods" }));
	});

	it("says a pattern is unfinished rather than failing the request", async () => {
		body = results({ files: [], totalMatches: 0, totalFiles: 0, invalidRegex: "missing closing )" });
		renderPanel();
		await userEvent.click(screen.getByRole("button", { name: "Regular expression" }));
		await userEvent.type(box(), "class (");
		await waitFor(() => expect(screen.getByText(/Invalid pattern: missing closing/)).toBeInTheDocument());
		// A half-typed regex must not paint the "nothing here" state — it is a
		// state of the box between two working keystrokes.
		expect(screen.queryByText("No results")).not.toBeInTheDocument();
	});

	it("says how much of the tree it looked at when nothing matched", async () => {
		body = results({ files: [], totalMatches: 0, totalFiles: 0 });
		renderPanel();
		await userEvent.type(box(), "zzqqxx");
		await waitFor(() => expect(screen.getByText("No results in 4,488 files")).toBeInTheDocument());
		// One line, not a full-height centred state: every prefix of a word that
		// has not matched yet lands here, and a block that flashes in and out
		// between keystrokes is the wrong weight for a state you type through.
		expect(document.querySelector(".files-panel__empty")).toBeNull();
	});

	it("says out loud when the list is a prefix of what was found", async () => {
		body = results({ totalMatches: 12847, totalFiles: 1203, truncated: true });
		renderPanel();
		await userEvent.type(box(), "self");
		await waitFor(() => expect(screen.getByText(/12,847 results in 1,203 files — showing 1/)).toBeInTheDocument());
	});

	it("leaves the previous results on screen while the next search is in flight", async () => {
		renderPanel();
		await userEvent.type(box(), "ViewModel");
		await waitFor(() => expect(screen.getByTitle("App/Login.swift")).toBeInTheDocument());
		respond = () => undefined;
		await userEvent.type(box(), "s");
		// Blanking the list between two keystrokes is a flicker, not information.
		expect(screen.getByTitle("App/Login.swift")).toBeInTheDocument();
	});

	it("says a cleaned-up worktree is gone rather than reporting no results", async () => {
		body = {
			available: false,
			reason: "no_workspace",
			query: "",
			files: [],
			totalMatches: 0,
			totalFiles: 0,
			filesSearched: 0,
			truncated: false,
		};
		renderPanel();
		await waitFor(() => expect(screen.getByText("Worktree no longer on disk")).toBeInTheDocument());
	});

	it("asks nothing at all while another mode is on screen", async () => {
		renderPanel({ active: false });
		await new Promise((resolve) => setTimeout(resolve, 30));
		expect(getMock).not.toHaveBeenCalled();
	});

	it("Escape hands the rail back to the mode it came from", async () => {
		const { onExit } = renderPanel();
		await userEvent.type(box(), "{Escape}");
		expect(onExit).toHaveBeenCalled();
	});

	it("marks the hit that is open in the editor", async () => {
		renderPanel({ selected: { path: "App/Login.swift", line: 12 } });
		await userEvent.type(box(), "ViewModel");
		await waitFor(() => expect(screen.getByTitle("App/Login.swift:12:12")).toHaveAttribute("aria-current", "true"));
	});
});
