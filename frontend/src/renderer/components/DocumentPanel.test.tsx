import { act, fireEvent, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../lib/api-client";
import type { FileDoc } from "../lib/chat-docs";
import { previewDocument } from "../lib/chat-preview";
import { WIDGET_DOC_TYPE, WIDGET_FRAME_URL } from "../lib/chat-widget";
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
		expect(screen.getByText("Open a document or media from the chat to see it here.")).toBeInTheDocument();
	});

	it("lists the session's documents while none is open, and opens the one clicked", async () => {
		const onOpen = vi.fn();
		const newest = doc({ path: "tmp/batch.json", kind: "json", title: "batch.json", size: 900 });
		const older = doc({ root: "knowledge", path: "plans/x--survey.md", title: "x--survey.md" });
		render(<DocumentPanel active doc={null} docs={[newest, older]} onClose={vi.fn()} onOpen={onOpen} sessionId="s1" />);
		const list = screen.getByRole("list", { name: "Results in this session" });
		const items = within(list).getAllByRole("listitem");
		expect(items.map((item) => item.textContent)).toEqual([
			expect.stringContaining("batch.json"),
			expect.stringContaining("x--survey.md"),
		]);
		await userEvent.setup().click(within(list).getByRole("button", { name: "Open x--survey.md" }));
		expect(onOpen).toHaveBeenCalledWith(older);
		expect(screen.queryByText(/Open a document or media/)).toBeNull();
	});

	it("lists a document, an image with a small lazy thumbnail and a video, in the order given", async () => {
		const onOpen = vi.fn();
		const clip = doc({ path: "out/clip.mp4", kind: "video", title: "clip.mp4", size: 3_145_728 });
		const shot = doc({ path: "out/s h.png", kind: "image", title: "s h.png", size: 4096 });
		const plan = doc({});
		render(
			<DocumentPanel active doc={null} docs={[clip, shot, plan]} onClose={vi.fn()} onOpen={onOpen} sessionId="s1" />,
		);
		const items = within(screen.getByRole("list", { name: "Results in this session" })).getAllByRole("listitem");
		expect(items.map((item) => item.textContent)).toEqual([
			"MP4clip.mp4worktree · out/clip.mp4 · 3.0 MB",
			"PNGs h.pngworktree · out/s h.png · 4 KB",
			"MDplan.mdworktree · docs/plan.md · 2 KB",
		]);
		const thumbs = screen.getAllByRole("presentation", { hidden: true });
		expect(thumbs).toHaveLength(1);
		expect(thumbs[0]).toHaveAttribute("loading", "lazy");
		expect(thumbs[0]).toHaveAttribute("src", `${DAEMON}/worktree/out/s%20h.png`);
		expect(within(items[0]).queryByRole("presentation", { hidden: true })).toBeNull();
		await userEvent.setup().click(within(items[0]).getByRole("button", { name: "Open clip.mp4" }));
		expect(onOpen).toHaveBeenCalledWith(clip);
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

	describe("image", () => {
		const shot = doc({ kind: "image", path: "docs/shot.png", title: "shot.png" });

		it("loads straight from the daemon, with no fetch of its own", () => {
			panel(shot);
			expect(screen.getByRole("img", { name: "shot.png" })).toHaveAttribute("src", `${DAEMON}/worktree/docs/shot.png`);
			expect(fetchMock).not.toHaveBeenCalled();
		});

		it("fits the panel first, and the button and a click on the image switch to full size and back", async () => {
			panel(shot);
			const user = userEvent.setup();
			const img = screen.getByRole("img", { name: "shot.png" });
			expect(img).toHaveAttribute("data-fit", "fit");
			await user.click(screen.getByRole("button", { name: "Full size" }));
			expect(img).toHaveAttribute("data-fit", "full");
			expect(screen.getByRole("button", { name: "Fit" })).toBeInTheDocument();
			await user.click(img);
			expect(img).toHaveAttribute("data-fit", "fit");
			await user.click(img);
			expect(img).toHaveAttribute("data-fit", "full");
		});

		it("offers Open in browser", () => {
			panel(shot);
			expect(screen.getByRole("link", { name: /Open in browser/ })).toHaveAttribute(
				"href",
				`${DAEMON}/worktree/docs/shot.png`,
			);
		});

		it("says so when the image does not load", () => {
			panel(shot);
			fireEvent.error(screen.getByRole("img", { name: "shot.png" }));
			expect(screen.getByRole("alert")).toHaveTextContent(/Could not open shot\.png/);
		});
	});

	describe("video", () => {
		const clip = doc({ kind: "video", path: "out/clip.mp4", title: "clip.mp4", size: 5_000_000 });
		const videoOf = () => {
			const video = document.querySelector("video");
			if (!video) throw new Error("no video");
			return video;
		};

		it("streams from the daemon URL with controls and metadata only, with no fetch of its own", () => {
			panel(clip);
			const video = videoOf();
			expect(video).toHaveAttribute("src", `${DAEMON}/worktree/out/clip.mp4`);
			expect(video).toHaveAttribute("controls");
			expect(video).toHaveAttribute("preload", "metadata");
			expect(fetchMock).not.toHaveBeenCalled();
		});

		it("offers Open in browser", () => {
			panel(clip);
			expect(screen.getByRole("link", { name: /Open in browser/ })).toHaveAttribute(
				"href",
				`${DAEMON}/worktree/out/clip.mp4`,
			);
		});

		it("says so when the video does not load", () => {
			panel(clip);
			fireEvent.error(videoOf());
			expect(screen.getByRole("alert")).toHaveTextContent(/Could not open clip\.mp4/);
			expect(document.querySelector("video")).toBeNull();
		});
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

		it.each([["pdf", "docs/spec.pdf", "spec.pdf"]] as const)(
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

		it("gives a pdf file a native view of its own, keyed apart from the Browser tab's", async () => {
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
			panel(doc({ kind: "pdf", path: "docs/spec.pdf", title: "spec.pdf" }));
			expect(document.querySelector("[data-doc-slot]")).not.toBeNull();
			expect(document.querySelector("iframe")).toBeNull();
			await waitFor(() => expect(ensure).toHaveBeenCalledWith("doc:s1"));
			await waitFor(() =>
				expect(navigate).toHaveBeenCalledWith({ viewId: "1:doc:s1", url: `${DAEMON}/worktree/docs/spec.pdf` }),
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

	describe("html preview", () => {
		const page = doc({ kind: "html", path: "out/page.html", title: "page.html" });
		const FILE = '<!doctype html><html><body><button onclick="go()">Go</button></body></html>';

		function frameOf() {
			const frame = document.querySelector("iframe");
			if (!frame) throw new Error("no preview frame");
			return frame;
		}

		async function loadedFrame() {
			const frame = await waitFor(frameOf);
			const post = vi.fn();
			Object.defineProperty(frame, "contentWindow", { configurable: true, value: { postMessage: post } });
			act(() => {
				frame.dispatchEvent(new Event("load"));
			});
			return post;
		}

		it("draws the file live in a frame with scripts on and no way back to the app's origin", async () => {
			text(FILE);
			panel(page);
			expect(screen.getByRole("tab", { name: "Preview" })).toHaveAttribute("aria-selected", "true");
			const post = await loadedFrame();
			const frame = frameOf();
			expect(frame).toHaveAttribute("sandbox", "allow-scripts");
			expect(frame).toHaveAttribute("src", WIDGET_FRAME_URL);
			expect(frame).toHaveAttribute("title", "Preview of page.html");
			await waitFor(() =>
				expect(post).toHaveBeenCalledWith({ type: WIDGET_DOC_TYPE, html: previewDocument(FILE) }, "*"),
			);
			expect(fetchMock).toHaveBeenCalledWith(`${DAEMON}/worktree/out/page.html`, expect.anything());
			expect(screen.getByRole("link", { name: /Open in browser/ })).toBeInTheDocument();
		});

		it("shows the markup as text under Source, where none of it runs", async () => {
			text(FILE);
			panel(page);
			await userEvent.setup().click(screen.getByRole("tab", { name: "Source" }));
			expect(await screen.findByText(/onclick/)).toBeInTheDocument();
			expect(document.querySelector("iframe")).toBeNull();
		});

		it("reads the file again when the chat writes it again", async () => {
			text(FILE);
			const view = render(<DocumentPanel active doc={page} onClose={vi.fn()} revision={1} sessionId="s1" />);
			await loadedFrame();
			expect(fetchMock).toHaveBeenCalledTimes(1);
			text("<p>second</p>");
			view.rerender(<DocumentPanel active doc={page} onClose={vi.fn()} revision={2} sessionId="s1" />);
			const post = await loadedFrame();
			expect(fetchMock).toHaveBeenCalledTimes(2);
			await waitFor(() =>
				expect(post).toHaveBeenCalledWith({ type: WIDGET_DOC_TYPE, html: previewDocument("<p>second</p>") }, "*"),
			);
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
