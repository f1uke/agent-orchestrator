import { act, cleanup, render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { FileDoc, TurnDocs } from "../lib/chat-docs";
import { file, image, resolveCalls, stubChatFiles } from "../test/chat-files-stub";
import { ChatDocsProvider, ChatDocStrip, useDocOf } from "./ChatDocs";
import { ChatRefsProvider } from "./RefImages";

afterEach(() => {
	cleanup();
	vi.unstubAllGlobals();
});

const ARTIFACT = "https://claude.ai/artifact/DemoArtifact000000000001";
const settle = () => act(async () => undefined);

const survey = file("plans/demo--survey.md", "markdown", "knowledge", 14_200);
const surveyDoc: FileDoc = {
	source: "file",
	root: "knowledge",
	path: "plans/demo--survey.md",
	kind: "markdown",
	size: 14_200,
	title: "demo--survey.md",
};

function setup(
	docs: TurnDocs,
	known: Parameters<typeof stubChatFiles>[0],
	{ onOpen = vi.fn(), openDoc = null as FileDoc | null } = {},
) {
	const fetchMock = stubChatFiles(known);
	render(
		<ChatRefsProvider sessionId="s1">
			<ChatDocsProvider onOpen={onOpen} openDoc={openDoc}>
				<ChatDocStrip docs={docs} />
			</ChatDocsProvider>
		</ChatRefsProvider>,
	);
	return { onOpen, fetchMock };
}

describe("ChatDocStrip", () => {
	it("draws a card for a file the daemon confirmed, with its tag, name, place and size", async () => {
		setup({ refs: ["plans/demo--survey.md"], artifacts: [] }, { "plans/demo--survey.md": [survey] });
		const card = await screen.findByRole("group", { name: "demo--survey.md" });
		expect(within(card).getByText("MD")).toBeInTheDocument();
		expect(within(card).getByText("demo--survey.md")).toBeInTheDocument();
		expect(within(card).getByText("knowledge · plans/demo--survey.md · 14 KB")).toBeInTheDocument();
	});

	it("draws nothing, and no error, for a ref the daemon does not confirm", async () => {
		const { fetchMock } = setup({ refs: ["not-a-file.md"], artifacts: [] }, {});
		await settle();
		expect(resolveCalls(fetchMock)).toEqual([["not-a-file.md"]]);
		expect(screen.queryByTestId("chat-docs")).toBeNull();
		expect(screen.queryByRole("alert")).toBeNull();
	});

	it("leaves an image to the thumbnails under the text, so it is never drawn twice", async () => {
		const { fetchMock } = setup(
			{ refs: ["a.md", "shots/a.png"], artifacts: [] },
			{
				"a.md": [file("a.md", "markdown")],
				"shots/a.png": [image("shots/a.png")],
			},
		);
		await screen.findByRole("group", { name: "a.md" });
		expect(screen.getAllByRole("group")).toHaveLength(1);
		expect(resolveCalls(fetchMock)).toEqual([["a.md", "shots/a.png"]]);
	});

	it("opens the file in the Document tab when Open is pressed", async () => {
		const { onOpen } = setup({ refs: ["a"], artifacts: [] }, { a: [survey] });
		await userEvent.click(await screen.findByRole("button", { name: "Open demo--survey.md" }));
		expect(onOpen).toHaveBeenCalledWith(surveyDoc);
	});

	it("marks the card whose file is open, and no other", async () => {
		setup(
			{ refs: ["a", "b"], artifacts: [] },
			{ a: [survey], b: [file("docs/b.md", "markdown")] },
			{ openDoc: surveyDoc },
		);
		const cards = await screen.findAllByRole("group");
		expect(cards.map((c) => c.getAttribute("data-open"))).toEqual(["true", null]);
	});

	it("lists a file once when two refs name it, in ref order", async () => {
		setup(
			{ refs: ["a", "b", "c"], artifacts: [] },
			{
				a: [survey],
				b: [file("docs/b.md", "markdown")],
				c: [survey],
			},
		);
		await screen.findAllByRole("group");
		expect(screen.getAllByRole("group").map((c) => c.getAttribute("aria-label"))).toEqual(["demo--survey.md", "b.md"]);
	});

	it("draws an artifact card at once, with no daemon call, that opens the link in the browser and not the panel", async () => {
		const { onOpen, fetchMock } = setup(
			{ refs: [], artifacts: [{ source: "artifact", url: ARTIFACT, title: "Demo design system" }] },
			{},
		);
		const card = screen.getByRole("group", { name: "Demo design system" });
		expect(within(card).getByText(/needs your claude\.ai login/)).toBeInTheDocument();
		const link = within(card).getByRole("link", { name: /Open in browser/ });
		expect(link).toHaveAttribute("href", ARTIFACT);
		expect(link).toHaveAttribute("target", "_blank");
		expect(link).toHaveAttribute("rel", expect.stringContaining("noopener"));
		await userEvent.click(link);
		expect(onOpen).not.toHaveBeenCalled();
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it("puts the artifact card after the file cards", async () => {
		setup(
			{ refs: ["a"], artifacts: [{ source: "artifact", url: ARTIFACT, title: "Demo design system" }] },
			{ a: [survey] },
		);
		await screen.findByRole("group", { name: "demo--survey.md" });
		expect(screen.getAllByRole("group").map((c) => c.getAttribute("aria-label"))).toEqual([
			"demo--survey.md",
			"Demo design system",
		]);
	});

	it("draws nothing outside a chat session", () => {
		const { container } = render(<ChatDocStrip docs={{ refs: ["a.md"], artifacts: [] }} />);
		expect(container).toBeEmptyDOMElement();
	});
});

describe("useDocOf", () => {
	function Probe({ path }: { path: string }) {
		const { doc, open } = useDocOf(path);
		return (
			<button disabled={!open} onClick={open} type="button">
				{doc ? `open ${doc.title}` : "none"}
			</button>
		);
	}

	const inSession = (path: string, onOpen = vi.fn()) =>
		render(
			<ChatRefsProvider sessionId="s1">
				<ChatDocsProvider onOpen={onOpen}>
					<Probe path={path} />
				</ChatDocsProvider>
			</ChatRefsProvider>,
		);

	it("finds the file a path names once the daemon confirms it, and opens it", async () => {
		const found = file("docs/plan.md", "markdown");
		stubChatFiles({ "/work/docs/plan.md": [found] });
		const onOpen = vi.fn();
		inSession("/work/docs/plan.md", onOpen);
		await userEvent.click(await screen.findByRole("button", { name: "open plan.md" }));
		expect(onOpen).toHaveBeenCalledWith({
			source: "file",
			root: "worktree",
			path: "docs/plan.md",
			kind: "markdown",
			size: 2048,
			title: "plan.md",
		});
	});

	it("offers nothing before the daemon answers, or when it names nothing", async () => {
		stubChatFiles({});
		inSession("/work/docs/gone.md");
		expect(screen.getByRole("button", { name: "none" })).toBeDisabled();
		await settle();
		expect(screen.getByRole("button", { name: "none" })).toBeDisabled();
	});

	it("asks nothing for a path that is not a document", async () => {
		const fetchMock = stubChatFiles({});
		inSession("/work/main.go");
		await settle();
		expect(fetchMock).not.toHaveBeenCalled();
	});

	it("offers nothing outside a document provider", () => {
		render(<Probe path="x.md" />);
		expect(screen.getByRole("button", { name: "none" })).toBeDisabled();
	});
});
