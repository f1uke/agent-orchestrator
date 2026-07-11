import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { AdfNode } from "../hooks/useSessionJiraContext";
import { JiraAdf } from "./JiraAdf";

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
