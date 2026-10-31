import { act, render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../lib/api-client";
import type { FileDoc } from "../lib/chat-docs";
import { DocumentPanel } from "./DocumentPanel";

const fetchMock = vi.fn<typeof fetch>();

beforeEach(() => {
	setApiBaseUrl("http://127.0.0.1:3001");
	fetchMock.mockReset();
	vi.stubGlobal("fetch", fetchMock);
	URL.createObjectURL = vi.fn(() => "blob:doc");
	URL.revokeObjectURL = vi.fn();
});
afterEach(() => {
	vi.unstubAllGlobals();
	setApiBaseUrl(null);
});

const doc = (over: Partial<FileDoc>): FileDoc => ({
	source: "file",
	root: "worktree",
	path: "docs/plan.md",
	kind: "markdown",
	size: 2048,
	title: "plan.md",
	...over,
});

const text = (body: string) => fetchMock.mockImplementation(async () => new Response(body, { status: 200 }));

function panel(d: FileDoc | null, onClose = vi.fn()) {
	render(<DocumentPanel active doc={d} onClose={onClose} sessionId="s1" />);
	return { onClose };
}

const DAEMON = "http://127.0.0.1:3001/api/v1/sessions/s1/chat/files";

describe("DocumentPanel", () => {
	it("says what to do while no document is open", () => {
		panel(null);
		expect(screen.getByText(/Open a document from the chat/)).toBeInTheDocument();
	});

	it("names the file in the header with its place and size", async () => {
		text("# Plan");
		panel(doc({}));
		expect(screen.getByRole("heading", { name: "plan.md" })).toBeInTheDocument();
		expect(screen.getByText("worktree · docs/plan.md · 2 KB")).toBeInTheDocument();
		await screen.findByRole("heading", { name: "Plan" });
	});

	it("fetches Markdown from the daemon and renders its heading, with a script left as text", async () => {
		text("# Retry plan\n\n<script>window.pwned = true</script>\n\nDone.");
		panel(doc({}));
		expect(await screen.findByRole("heading", { name: "Retry plan" })).toBeInTheDocument();
		expect(fetchMock.mock.calls[0][0]).toBe(`${DAEMON}/worktree/docs/plan.md`);
		expect(document.querySelector("script")).toBeNull();
		expect((window as unknown as { pwned?: boolean }).pwned).toBeUndefined();
	});

	it("escapes each path segment in the file URL", async () => {
		text("# x");
		panel(doc({ root: "knowledge", path: "plans/a b/c#1.md" }));
		await screen.findByRole("heading", { name: "x" });
		expect(fetchMock.mock.calls[0][0]).toBe(`${DAEMON}/knowledge/plans/a%20b/c%231.md`);
	});

	it("pretty-prints JSON with line numbers", async () => {
		text('{"batch":3,"error":{"code":429}}');
		panel(doc({ kind: "json", path: "tmp/batch.json", title: "batch.json" }));
		await waitFor(() =>
			expect([...document.querySelectorAll(".chat-codebox__num")].map((n) => n.textContent)).toEqual([
				"1",
				"2",
				"3",
				"4",
				"5",
				"6",
			]),
		);
		expect(document.querySelector(".chat-codebox__body")?.textContent).toContain('"code": 429');
	});

	it("shows a log's lines once, without a second name over the code", async () => {
		text("start\nerror: 429\n");
		panel(doc({ kind: "log", path: "logs/export.log", title: "export.log" }));
		await waitFor(() => expect(document.querySelectorAll(".chat-codebox__num")).toHaveLength(2));
		expect(document.querySelector(".chat-codebox__head")).toBeNull();
	});

	it("says how many lines a long file leaves out", async () => {
		text(Array.from({ length: 2005 }, (_, i) => `line ${i}`).join("\n"));
		panel(doc({ kind: "log", path: "big.log", title: "big.log" }));
		expect(await screen.findByText("… 5 more lines")).toBeInTheDocument();
	});

	it("shows an image from the bytes through a blob URL", async () => {
		fetchMock.mockImplementation(async () => new Response(new TextEncoder().encode("png"), { status: 200 }));
		panel(doc({ kind: "image", path: "docs/shot.png", title: "shot.png" }));
		const img = await screen.findByRole("img", { name: "shot.png" });
		expect(img).toHaveAttribute("src", "blob:doc");
	});

	it("says so when the daemon refuses the file", async () => {
		fetchMock.mockImplementation(async () => new Response("", { status: 404 }));
		panel(doc({}));
		expect(await screen.findByRole("alert")).toHaveTextContent(/Could not open plan\.md/);
	});

	it("fetches the file again for the next document, not the last one", async () => {
		text("# First");
		const view = render(<DocumentPanel active doc={doc({})} onClose={vi.fn()} sessionId="s1" />);
		await screen.findByRole("heading", { name: "First" });
		text("# Second");
		view.rerender(
			<DocumentPanel active doc={doc({ path: "docs/b.md", title: "b.md" })} onClose={vi.fn()} sessionId="s1" />,
		);
		expect(await screen.findByRole("heading", { name: "Second" })).toBeInTheDocument();
		expect(screen.queryByRole("heading", { name: "First" })).toBeNull();
	});

	describe("html and pdf", () => {
		let original: typeof window.ao;
		beforeEach(() => {
			original = window.ao;
		});
		afterEach(() => {
			window.ao = original;
		});

		it.each([
			["html", "docs/preview.html", "preview.html"],
			["pdf", "docs/spec.pdf", "spec.pdf"],
		] as const)(
			"shows a %s file as a notice with a link outside the desktop app, never as markup",
			(kind, path, title) => {
				delete window.ao;
				panel(doc({ kind, path, title }));
				expect(screen.getByText(/opens in the desktop app/i)).toBeInTheDocument();
				const link = screen.getByRole("link", { name: /Open in browser/ });
				expect(link).toHaveAttribute("href", `${DAEMON}/worktree/${path}`);
				expect(link).toHaveAttribute("target", "_blank");
				expect(document.querySelector("iframe")).toBeNull();
				expect(fetchMock).not.toHaveBeenCalled();
			},
		);

		it("gives an html file a native view of its own, keyed apart from the Browser tab's", async () => {
			const ensure = vi.fn(async (id: string) => ({
				viewId: `1:${id}`,
				url: "",
				title: "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			}));
			const navigate = vi.fn(async ({ viewId, url }: { viewId: string; url: string }) => ({
				viewId,
				url,
				title: "",
				canGoBack: false,
				canGoForward: false,
				isLoading: false,
			}));
			window.ao = { ...window.ao!, browser: { ...window.ao!.browser, ensure, navigate } };
			panel(doc({ kind: "html", path: "docs/preview.html", title: "preview.html" }));
			expect(document.querySelector("[data-doc-slot]")).not.toBeNull();
			expect(document.querySelector("iframe")).toBeNull();
			await waitFor(() => expect(ensure).toHaveBeenCalledWith("doc:s1"));
			await waitFor(() =>
				expect(navigate).toHaveBeenCalledWith({ viewId: "1:doc:s1", url: `${DAEMON}/worktree/docs/preview.html` }),
			);
			expect(ensure).not.toHaveBeenCalledWith("s1");
		});

		it("drops the native view when the document is closed", async () => {
			const destroy = vi.fn();
			window.ao = {
				...window.ao!,
				browser: {
					...window.ao!.browser,
					destroy,
					ensure: async (id: string) => ({
						viewId: `1:${id}`,
						url: "",
						title: "",
						canGoBack: false,
						canGoForward: false,
						isLoading: false,
					}),
				},
			};
			const view = render(
				<DocumentPanel
					active
					doc={doc({ kind: "pdf", path: "a.pdf", title: "a.pdf" })}
					onClose={vi.fn()}
					sessionId="s1"
				/>,
			);
			await act(async () => undefined);
			view.unmount();
			expect(destroy).toHaveBeenCalledWith("1:doc:s1");
		});
	});

	it("closes", async () => {
		text("# Plan");
		const { onClose } = panel(doc({}));
		await userEvent.click(screen.getByRole("button", { name: "Close document" }));
		expect(onClose).toHaveBeenCalled();
		await act(async () => undefined);
	});
});
