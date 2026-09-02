import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { WikiNoteView } from "./WikiNoteView";
import type { WikiNote } from "../hooks/useWiki";

const putMock = vi.fn();

vi.mock("../lib/api-client", () => ({
	apiClient: {
		GET: vi.fn(),
		POST: vi.fn(),
		PUT: (...args: unknown[]) => putMock(...args),
		DELETE: vi.fn(),
	},
	apiErrorMessage: (error: unknown, fallback = "failed") => (error as { message?: string })?.message ?? fallback,
	getApiBaseUrl: () => "http://127.0.0.1:3001",
}));
vi.mock("../lib/note/highlight", () => ({
	highlightCode: () => Promise.resolve(null),
	grammarFor: () => "",
}));

const CONTENT = [
	"---",
	"title: MOBILITY-4713-Webview-Zoom - Tasks",
	"type: tasks",
	"---",
	"",
	"# MOBILITY-4713-Webview-Zoom - Tasks",
	"",
	"Prose with a [[STAR-2195-Navigate]] link in it.",
	"",
	"## Tasks",
	"",
	"- Investigate",
	"  - [ ] reproduce on device",
	"  - [x] read the webview docs",
	"",
	"```ts",
	"const zoom = 1.0;",
	"```",
	"",
	"| a | b |",
	"| - | - |",
	"| 1 | 2 |",
	"",
].join("\n");

function note(overrides: Partial<WikiNote> = {}): WikiNote {
	return {
		path: "Projects/MOBILITY-4713-Webview-Zoom/_tasks.md",
		content: CONTENT,
		size: CONTENT.length,
		contentHash: "sha256:before",
		backlinks: [],
		modifiedAt: new Date().toISOString(),
		...overrides,
	};
}

function renderNote(overrides: Partial<WikiNote> = {}, onReload = vi.fn()) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={client}>
			<WikiNoteView
				note={note(overrides)}
				loading={false}
				theme="dark"
				back={null}
				forward={null}
				onClose={vi.fn()}
				onReload={onReload}
				onOpenNote={vi.fn()}
				onOpenTag={vi.fn()}
			/>
		</QueryClientProvider>,
	);
	return { onReload };
}

/** The content the one PUT so far was asked to write. */
function written(): string {
	expect(putMock).toHaveBeenCalledTimes(1);
	return (putMock.mock.calls[0][1] as { body: { content: string } }).body.content;
}

beforeEach(() => {
	putMock.mockReset();
	putMock.mockResolvedValue({ data: { path: "x", contentHash: "sha256:after", size: 1, modifiedAt: "" } });
});

/**
 * 🗝 Every assertion here is about the BYTES. These are somebody's personal
 * notes with no backup, so "it saved" is not the property under test — "it
 * saved exactly this and nothing else" is.
 */
describe("editing a note in place", () => {
	it("ticks a nested checkbox by rewriting one character", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("checkbox", { name: "Mark as done" }));

		const after = written();
		expect(after).toHaveLength(CONTENT.length);
		const changed = [...after].findIndex((char, index) => char !== CONTENT[index]);
		expect(CONTENT[changed]).toBe(" ");
		expect(after[changed]).toBe("x");
		expect(after.slice(changed + 1)).toBe(CONTENT.slice(changed + 1));
		expect(after).toContain("  - [x] reproduce on device\n");
	});

	it("unticks a done item back to an empty box", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("checkbox", { name: "Mark as not done" }));
		expect(written()).toContain("  - [ ] read the webview docs\n");
	});

	it("preconditions the save on the hash the note was read with", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("checkbox", { name: "Mark as done" }));
		const body = (putMock.mock.calls[0][1] as { body: { baseHash: string; path: string } }).body;
		expect(body.baseHash).toBe("sha256:before");
		expect(body.path).toBe("Projects/MOBILITY-4713-Webview-Zoom/_tasks.md");
	});

	it("edits a paragraph and leaves every other byte alone", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: /Prose with a/ }));
		const editor = screen.getByRole("textbox", { name: "Edit this block" });
		// The box opens on the block's own source, which for a block carrying a
		// wikilink is the markdown the note actually holds.
		expect((editor as HTMLTextAreaElement).value).toBe("Prose with a [[STAR-2195-Navigate]] link in it.");

		await user.clear(editor);
		await user.type(editor, "Rewritten prose.");
		await user.tab();

		await waitFor(() => expect(putMock).toHaveBeenCalled());
		const after = written();
		expect(after).toContain("\nRewritten prose.\n");
		expect(after).toContain("---\ntitle: MOBILITY-4713-Webview-Zoom - Tasks\ntype: tasks\n---\n");
		expect(after).toContain("```ts\nconst zoom = 1.0;\n```");
		expect(after).toContain("| 1 | 2 |");
		expect(after).toContain("  - [ ] reproduce on device\n");
		expect(after.endsWith("\n")).toBe(true);
	});

	it("edits a heading and keeps its level", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: "Tasks" }));
		const editor = screen.getByRole("textbox", { name: "Edit this block" });
		expect((editor as HTMLTextAreaElement).value).toBe("Tasks");
		await user.clear(editor);
		await user.type(editor, "Open questions{Enter}");

		await waitFor(() => expect(putMock).toHaveBeenCalled());
		expect(written()).toContain("## Open questions\n");
	});

	it("edits a nested task item without disturbing its indentation or its box", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: "reproduce on device" }));
		const editor = screen.getByRole("textbox", { name: "Edit this block" });
		await user.clear(editor);
		await user.type(editor, "reproduce on a real device{Enter}");

		await waitFor(() => expect(putMock).toHaveBeenCalled());
		expect(written()).toContain("  - [ ] reproduce on a real device\n");
	});

	it("writes nothing when the reader presses Escape", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: "Tasks" }));
		await user.type(screen.getByRole("textbox", { name: "Edit this block" }), " changed{Escape}");

		expect(putMock).not.toHaveBeenCalled();
		expect(screen.queryByRole("textbox", { name: "Edit this block" })).toBeNull();
	});

	it("writes nothing when the text comes back unchanged", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: "Tasks" }));
		await user.tab();

		expect(putMock).not.toHaveBeenCalled();
	});

	it("follows a wikilink instead of opening the editor", async () => {
		const user = userEvent.setup();
		const onOpenNote = vi.fn();
		const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
		render(
			<QueryClientProvider client={client}>
				<WikiNoteView
					note={note()}
					loading={false}
					theme="dark"
					back={null}
					forward={null}
					onClose={vi.fn()}
					onReload={vi.fn()}
					onOpenNote={onOpenNote}
					onOpenTag={vi.fn()}
				/>
			</QueryClientProvider>,
		);

		await user.click(screen.getByRole("button", { name: "STAR-2195-Navigate" }));
		expect(onOpenNote).toHaveBeenCalledWith("STAR-2195-Navigate");
		expect(screen.queryByRole("textbox", { name: "Edit this block" })).toBeNull();
	});

	it("offers no editor on a code block or a table", () => {
		renderNote();
		expect(screen.queryByRole("button", { name: /const zoom/ })).toBeNull();
		expect(screen.queryByRole("button", { name: /^1$/ })).toBeNull();
	});
});

/**
 * The vault's own agent writes these files, so a refused save is the normal
 * case. Nothing is written, the reader is told, and the only way forward is to
 * look at what is there now — the block's byte range means nothing against a
 * file that has moved, so no re-save is offered.
 */
describe("when the agent wrote the note first", () => {
	function conflict() {
		putMock.mockResolvedValue({
			error: {
				code: "WIKI_NOTE_CONFLICT",
				message: "changed",
				details: { currentHash: "sha256:theirs", currentSize: 400, currentModifiedAt: new Date().toISOString() },
			},
		});
	}

	it("surfaces the drift banner and writes nothing", async () => {
		const user = userEvent.setup();
		conflict();
		renderNote();

		await user.click(screen.getByRole("checkbox", { name: "Mark as done" }));
		expect(await screen.findByTestId("file-drift-banner")).toBeTruthy();
		expect(screen.getByText(/changed on disk while you were editing it/)).toBeTruthy();
		expect(screen.getByText(/Nothing was written/)).toBeTruthy();
		// No blind overwrite is offered — only reloading what is actually there.
		expect(screen.queryByRole("button", { name: "Review changes" })).toBeNull();
	});

	it("reloads from disk when the reader gives up their edit", async () => {
		const user = userEvent.setup();
		conflict();
		const { onReload } = renderNote();

		await user.click(screen.getByRole("checkbox", { name: "Mark as done" }));
		await screen.findByTestId("file-drift-banner");
		// The destructive button asks twice.
		await user.click(screen.getByRole("button", { name: "Discard mine and reload" }));
		await user.click(screen.getByRole("button", { name: "Really discard my edits?" }));
		expect(onReload).toHaveBeenCalled();
	});

	it("says so plainly when the note is simply gone", async () => {
		const user = userEvent.setup();
		putMock.mockResolvedValue({ error: { code: "WIKI_NOTE_NOT_FOUND", message: "gone" } });
		renderNote();

		await user.click(screen.getByRole("checkbox", { name: "Mark as done" }));
		expect(await screen.findByText(/no longer there/)).toBeTruthy();
	});
});

/**
 * A CRLF note is read-only in full: `marked` normalises the endings away before
 * lexing, so no block's bytes can be located, and a save would silently convert
 * the whole file. Nothing offers to edit, and nothing can be written.
 */
describe("a note whose bytes cannot be mapped", () => {
	it("renders a CRLF note read-only", () => {
		renderNote({ content: CONTENT.replace(/\n/g, "\r\n") });
		expect(screen.queryByRole("checkbox")).toBeNull();
		expect(screen.queryByRole("button", { name: "Tasks" })).toBeNull();
	});
});

/**
 * The Properties panel is a SECOND write path into the same file, and the
 * assertions are the same ones: one key's value changes, and every other byte
 * — the other keys, their order, their quoting, and the whole body — is
 * identical.
 */
describe("a note's properties", () => {
	it("shows every frontmatter key, with a count in the status line", () => {
		renderNote();
		expect(screen.getByText("Properties")).toBeTruthy();
		expect(screen.getByText("title")).toBeTruthy();
		expect(screen.getByText("type")).toBeTruthy();
		expect(screen.getByText(/2 properties/)).toBeTruthy();
	});

	it("rewrites one key's value and leaves the body and the other keys alone", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: "tasks" }));
		const input = screen.getByRole("textbox", { name: "Edit type" });
		await user.clear(input);
		await user.type(input, "notes{Enter}");

		await waitFor(() => expect(putMock).toHaveBeenCalled());
		const after = written();
		expect(after).toContain("---\ntitle: MOBILITY-4713-Webview-Zoom - Tasks\ntype: notes\n---\n");
		// The body is byte-identical: everything from the closing fence on.
		expect(after.slice(after.indexOf("\n---\n") + 5)).toBe(CONTENT.slice(CONTENT.indexOf("\n---\n") + 5));
	});

	it("adds a property at the end of the block without moving the body", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: /Add property/ }));
		await user.type(screen.getByRole("textbox", { name: "New property name" }), "status");
		await user.type(screen.getByRole("textbox", { name: "New property value" }), "in progress{Enter}");

		await waitFor(() => expect(putMock).toHaveBeenCalled());
		const after = written();
		expect(after).toContain("type: tasks\nstatus: in progress\n---\n");
		expect(after.slice(after.indexOf("\n---\n") + 5)).toBe(CONTENT.slice(CONTENT.indexOf("\n---\n") + 5));
	});

	it("refuses a duplicate key and says so instead of writing", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: /Add property/ }));
		await user.type(screen.getByRole("textbox", { name: "New property name" }), "type");
		await user.type(screen.getByRole("textbox", { name: "New property value" }), "x{Enter}");

		expect(await screen.findByText(/already has/)).toBeTruthy();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("writes nothing when a value comes back unchanged", async () => {
		const user = userEvent.setup();
		renderNote();

		await user.click(screen.getByRole("button", { name: "tasks" }));
		await user.tab();
		expect(putMock).not.toHaveBeenCalled();
	});

	it("offers a panel with just Add property on a note that has no frontmatter", () => {
		renderNote({ content: "# Bare\n\nJust prose.\n" });
		expect(screen.getByText(/no properties yet/)).toBeTruthy();
		expect(screen.getByRole("button", { name: /Add property/ })).toBeTruthy();
	});

	it("locks a value whose YAML shape cannot be rewritten in place", () => {
		renderNote({ content: "---\nnote: |\n  a block scalar\ntitle: T\n---\n\nBody.\n" });
		// The block scalar has no editable control; the plain key beside it does.
		expect(screen.queryByRole("button", { name: /^\|$/ })).toBeNull();
		expect(screen.getByRole("button", { name: "T" })).toBeTruthy();
	});
});
