import { render, screen, fireEvent } from "@testing-library/react";
import { describe, expect, it, vi, beforeEach } from "vitest";
import type { AdfNode } from "../hooks/useSessionJiraContext";
import { JiraAdf } from "./JiraAdf";
import { JiraMediaProvider } from "./JiraMedia";

function renderAdf(nodes: AdfNode[]) {
	return render(<JiraAdf nodes={nodes} />).container;
}

describe("JiraAdf", () => {
	it("renders nothing for empty input", () => {
		const { container } = render(<JiraAdf nodes={[]} />);
		expect(container.querySelector(".jira-adf")).toBeNull();
	});

	it("renders bold paragraphs (the ad-hoc 'headings') as <strong>, not <h4>", () => {
		const c = renderAdf([
			{ type: "paragraph", content: [{ type: "text", text: "Background", marks: [{ type: "strong" }] }] },
		]);
		expect(c.querySelector("p strong")?.textContent).toBe("Background");
		expect(c.querySelector("h4")).toBeNull();
	});

	it("renders nested bullet lists and inline code", () => {
		const c = renderAdf([
			{
				type: "bulletList",
				content: [
					{
						type: "listItem",
						content: [
							{
								type: "paragraph",
								content: [
									{ type: "text", text: "open " },
									{ type: "text", text: "/x", marks: [{ type: "code" }] },
								],
							},
							{
								type: "bulletList",
								content: [
									{ type: "listItem", content: [{ type: "paragraph", content: [{ type: "text", text: "child" }] }] },
								],
							},
						],
					},
				],
			},
		]);
		expect(c.querySelectorAll("ul").length).toBe(2);
		expect(c.querySelector("code")?.textContent).toBe("/x");
		expect(c.textContent).toContain("child");
	});

	it("renders a link mark with its href, opened externally", () => {
		const c = renderAdf([
			{
				type: "paragraph",
				content: [{ type: "text", text: "here", marks: [{ type: "link", href: "https://x.test/y" }] }],
			},
		]);
		const a = c.querySelector("a");
		expect(a?.getAttribute("href")).toBe("https://x.test/y");
		expect(a?.getAttribute("target")).toBe("_blank");
		expect(a?.getAttribute("rel")).toContain("noopener");
	});

	it("renders a smart link (inlineCard) labeled by hostname", () => {
		const c = renderAdf([
			{ type: "paragraph", content: [{ type: "inlineCard", attrs: { url: "https://www.example.com/design/abc" } }] },
		]);
		const a = c.querySelector("a.jira-adf__smartlink");
		expect(a?.getAttribute("href")).toBe("https://www.example.com/design/abc");
		expect(a?.textContent).toContain("example.com");
		expect(a?.textContent).not.toContain("www.");
	});

	it("renders a media node as an attachment chip with its filename", () => {
		const c = renderAdf([{ type: "mediaSingle", content: [{ type: "media", attrs: { filename: "shot.png" } }] }]);
		expect(c.querySelector(".jira-adf__att")?.textContent).toContain("shot.png");
	});

	it("renders taskItems as a checklist reflecting DONE/TODO state", () => {
		const c = renderAdf([
			{
				type: "taskList",
				content: [
					{ type: "taskItem", attrs: { state: "DONE" }, content: [{ type: "text", text: "done one" }] },
					{ type: "taskItem", attrs: { state: "TODO" }, content: [{ type: "text", text: "todo one" }] },
				],
			},
		]);
		const boxes = c.querySelectorAll(".jira-adf__checkbox");
		expect(boxes.length).toBe(2);
		expect(boxes[0].getAttribute("data-checked")).toBe("true");
		expect(boxes[1].getAttribute("data-checked")).toBe("false");
		expect(c.textContent).toContain("done one");
	});

	it("renders a table inside a horizontally scrollable wrapper", () => {
		const c = renderAdf([
			{
				type: "table",
				content: [
					{
						type: "tableRow",
						content: [
							{ type: "tableHeader", content: [{ type: "paragraph", content: [{ type: "text", text: "H" }] }] },
						],
					},
					{
						type: "tableRow",
						content: [{ type: "tableCell", content: [{ type: "paragraph", content: [{ type: "text", text: "C" }] }] }],
					},
				],
			},
		]);
		expect(c.querySelector(".jira-adf__table-wrap")).not.toBeNull();
		expect(c.querySelector("th")?.textContent).toBe("H");
		expect(c.querySelector("td")?.textContent).toBe("C");
	});

	it("surfaces the children of an unknown node instead of dropping them", () => {
		const c = renderAdf([
			{
				type: "someFutureBlock",
				content: [{ type: "paragraph", content: [{ type: "text", text: "kept" }] }],
			} as AdfNode,
		]);
		expect(c.textContent).toContain("kept");
	});
});

describe("JiraAdf inline media (with JiraMediaProvider)", () => {
	beforeEach(() => {
		global.URL.createObjectURL = vi.fn(() => "blob:mock");
		global.URL.revokeObjectURL = vi.fn();
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => ({ ok: true, blob: async () => new Blob(["x"]) })) as unknown as typeof fetch,
		);
	});

	const mediaNodes: AdfNode[] = [
		{ type: "mediaSingle", content: [{ type: "media", attrs: { filename: "shot.png" } }] } as AdfNode,
	];

	function renderWithProvider(attachments: { id: string; filename?: string; mimeType?: string }[]) {
		return render(
			<JiraMediaProvider sessionId="proj-1" description={mediaNodes} attachments={attachments}>
				<JiraAdf nodes={mediaNodes} />
			</JiraMediaProvider>,
		);
	}

	it("renders an inline preview (openable) for a media node matched by filename", async () => {
		const { container } = renderWithProvider([{ id: "173517", filename: "shot.png", mimeType: "image/png" }]);
		expect(await screen.findByRole("button", { name: /View shot\.png/i })).toBeInTheDocument();
		// The plain filename chip is NOT rendered when a preview resolves.
		expect(container.querySelector(".jira-adf__att")).toBeNull();
		expect(container.querySelector(".jira-adf__media-thumb")).not.toBeNull();
	});

	it("opens the shared lightbox on click", async () => {
		renderWithProvider([{ id: "173517", filename: "shot.png", mimeType: "image/png" }]);
		fireEvent.click(await screen.findByRole("button", { name: /View shot\.png/i }));
		expect(await screen.findByLabelText(/Evidence viewer/i)).toBeInTheDocument();
	});

	it("falls back to the filename chip when no attachment matches", () => {
		const { container } = renderWithProvider([{ id: "999", filename: "other.png", mimeType: "image/png" }]);
		expect(container.querySelector(".jira-adf__att")?.textContent).toContain("shot.png");
		expect(container.querySelector(".jira-adf__media-thumb")).toBeNull();
	});
});
