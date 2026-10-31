import { cleanup, render, screen, waitFor, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { image, resolveCalls, stubChatFiles } from "../test/chat-files-stub";
import { ChatRefsProvider, RefImages } from "./RefImages";

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const show = (source: string) =>
	render(
		<ChatRefsProvider sessionId="s 1">
			<p>{source}</p>
			<RefImages source={source} />
		</ChatRefsProvider>,
	);

describe("RefImages", () => {
	it("draws one thumbnail for every match of a path and of a glob, with the file name under it", async () => {
		stubChatFiles({
			"shots/a.png": [image("shots/a.png")],
			"shots/clash-*.png": [image("shots/clash-1.png"), image("shots/clash-2.png")],
		});
		show("See `shots/a.png` and `shots/clash-*.png`.");
		const row = await screen.findByTestId("ref-images");
		await waitFor(() => expect(within(row).getAllByRole("img")).toHaveLength(3));
		expect(
			within(row)
				.getAllByRole("img")
				.map((img) => img.getAttribute("alt")),
		).toEqual(["a.png", "clash-1.png", "clash-2.png"]);
		const caption = within(row).getByText("clash-2.png");
		expect(caption).toHaveAttribute("title", "shots/clash-2.png");
	});

	it("fetches the bytes from the session's file route, with each path segment escaped", async () => {
		const fetchMock = stubChatFiles({ "x.png": [image("plans/a b/c#1.png", "worktree")] });
		show("`x.png`");
		await screen.findByRole("img", { name: "c#1.png" });
		expect(fetchMock.mock.calls.map(([url]) => String(url))).toContain(
			"/api/v1/sessions/s%201/chat/files/worktree/plans/a%20b/c%231.png",
		);
	});

	it("lists a file once when two refs reach it, in ref order", async () => {
		stubChatFiles({
			"shots/b.png": [image("shots/b.png")],
			"shots/*.png": [image("shots/a.png"), image("shots/b.png")],
		});
		show("`shots/b.png` `shots/*.png`");
		await waitFor(() => expect(screen.getAllByRole("img")).toHaveLength(2));
		expect(screen.getAllByRole("img").map((img) => img.getAttribute("alt"))).toEqual(["b.png", "a.png"]);
	});

	it("draws nothing for a ref that names nothing, and keeps the text", async () => {
		const fetchMock = stubChatFiles({});
		show("Missing: `shots/gone.png`");
		await waitFor(() => expect(resolveCalls(fetchMock)).toEqual([["shots/gone.png"]]));
		await new Promise((r) => setTimeout(r, 20));
		expect(screen.queryByTestId("ref-images")).toBeNull();
		expect(screen.queryByRole("img")).toBeNull();
		expect(screen.getByText("Missing: `shots/gone.png`")).toBeInTheDocument();
	});

	it("draws no row for a non-image match", async () => {
		stubChatFiles({ "notes/a.png": [{ ...image("notes/a.md"), kind: "markdown", mime: "text/markdown" }] });
		show("`notes/a.png`");
		await new Promise((r) => setTimeout(r, 20));
		expect(screen.queryByTestId("ref-images")).toBeNull();
	});

	it("makes no call for text without a ref", async () => {
		const fetchMock = stubChatFiles({});
		show("Nothing to see, only words.");
		await new Promise((r) => setTimeout(r, 20));
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it("asks once for a ref however often the text is drawn again", async () => {
		const fetchMock = stubChatFiles({ "shots/a.png": [image("shots/a.png")] });
		const { rerender } = show("`shots/a.png`");
		await screen.findByRole("img", { name: "a.png" });
		rerender(
			<ChatRefsProvider sessionId="s 1">
				<RefImages source={"`shots/a.png` and more"} />
				<RefImages source={"`shots/a.png`"} />
			</ChatRefsProvider>,
		);
		await screen.findAllByRole("img", { name: "a.png" });
		expect(resolveCalls(fetchMock)).toEqual([["shots/a.png"]]);
	});

	it("opens one lightbox for the block that pages through its images, and closes on Esc", async () => {
		stubChatFiles({ "shots/*.png": [image("shots/a.png"), image("shots/b.png")] });
		const user = userEvent.setup();
		show("`shots/*.png`");
		await waitFor(() => expect(screen.getAllByRole("img")).toHaveLength(2));
		await user.click(screen.getByRole("button", { name: "View b.png" }));
		const dialog = await screen.findByRole("dialog");
		expect(within(dialog).getByText("2 / 2")).toBeInTheDocument();
		await user.keyboard("{ArrowRight}");
		expect(within(dialog).getByText("1 / 2")).toBeInTheDocument();
		await user.keyboard("{Escape}");
		await waitFor(() => expect(screen.queryByRole("dialog")).toBeNull());
	});

	it("draws nothing outside a chat session", () => {
		const { container } = render(<RefImages source="`shots/a.png`" />);
		expect(container).toBeEmptyDOMElement();
	});
});
