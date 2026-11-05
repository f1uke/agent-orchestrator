import { describe, expect, it } from "vitest";
import type { ChatItem, ToolCallItem } from "./chat-fold";
import { PREVIEW_CSP, previewDocument, writeRevision } from "./chat-preview";

const policyOf = (doc: string) => /<meta http-equiv="Content-Security-Policy" content="([^"]*)">/.exec(doc)?.[1];

describe("previewDocument", () => {
	it("puts the policy ahead of everything the file says, keeping its doctype first", () => {
		const file = '<!DOCTYPE html>\n<html lang="th"><head><title>t</title></head><body><script>x()</script></body></html>';
		const doc = previewDocument(file);
		expect(doc.startsWith("<!DOCTYPE html>")).toBe(true);
		expect(doc.indexOf("Content-Security-Policy")).toBeLessThan(doc.indexOf("<html"));
		expect(doc).toContain("<script>x()</script>");
		expect(policyOf(doc)).toBe(PREVIEW_CSP);
	});

	it("gives a bare fragment the policy too", () => {
		const doc = previewDocument("<p>hi</p>");
		expect(doc.startsWith("<meta http-equiv")).toBe(true);
		expect(doc.endsWith("<p>hi</p>")).toBe(true);
	});

	it("lets inline script and style run but reaches no network, so the daemon on 127.0.0.1 stays out of reach", () => {
		const parts = new Map(PREVIEW_CSP.split("; ").map((d) => [d.split(" ")[0], d] as const));
		expect(parts.get("default-src")).toBe("default-src 'none'");
		expect(parts.get("script-src")).toBe("script-src 'unsafe-inline'");
		expect(parts.has("connect-src")).toBe(false);
		expect(PREVIEW_CSP).not.toMatch(/https?:|\*|'self'/);
	});
});

const write = (id: string, path: string, over: Partial<ToolCallItem> = {}): ToolCallItem => ({
	kind: "tool_call",
	id,
	name: "Write",
	parentToolUseId: "",
	input: { file_path: path, content: "" },
	result: { content: "ok", isError: false } as ToolCallItem["result"],
	startedTs: "",
	...over,
});

describe("writeRevision", () => {
	const doc = { path: "out/page.html" };

	it("counts the finished writes that landed on the file, whether named absolute or relative", () => {
		const items: ChatItem[] = [
			write("1", "/repo/out/page.html"),
			write("2", "out/page.html", { name: "Edit" }),
			write("3", "/repo/out/other.html"),
			write("4", "/repo/xout/page.html"),
		];
		expect(writeRevision(items, doc)).toBe(2);
	});

	it("skips a write still running, one that failed, and a subagent's", () => {
		const items: ChatItem[] = [
			write("1", "/repo/out/page.html", { result: undefined }),
			write("2", "/repo/out/page.html", { result: { content: "no", isError: true } as ToolCallItem["result"] }),
			write("3", "/repo/out/page.html", { parentToolUseId: "task-1" }),
		];
		expect(writeRevision(items, doc)).toBe(0);
	});
});
