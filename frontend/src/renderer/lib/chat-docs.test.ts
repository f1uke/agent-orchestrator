import { describe, expect, it } from "vitest";
import {
	type ArtifactDoc,
	artifactLinks,
	artifactsOfTurn,
	docMeta,
	docsOfTurn,
	docTag,
	type FileDoc,
	fileDocOf,
	openedPaths,
	textView,
	turnDocs,
	writtenPath,
} from "./chat-docs";
import type { ChatItem, ToolCallItem } from "./chat-fold";

const say = (text: string, parentToolUseId = ""): ChatItem => ({
	kind: "assistant_text",
	messageId: "m",
	text,
	parentToolUseId,
});
const tool = (name: string, input: unknown, parentToolUseId = ""): ToolCallItem => ({
	kind: "tool_call",
	id: `t-${name}-${JSON.stringify(input)}`,
	name,
	parentToolUseId,
	input,
	startedTs: "",
});

const artifact = (url: string, title = "claude.ai artifact"): ArtifactDoc => ({ source: "artifact", url, title });

describe("writtenPath", () => {
	it("is the file of a top-level Write, Edit or MultiEdit", () => {
		expect(writtenPath(tool("Write", { file_path: "/w/a.md" }))).toBe("/w/a.md");
		expect(writtenPath(tool("Edit", { file_path: "/w/a.md" }))).toBe("/w/a.md");
		expect(writtenPath(tool("MultiEdit", { file_path: "/w/a.md" }))).toBe("/w/a.md");
	});

	it("is nothing for another tool, a subagent's call, or an input without a file", () => {
		expect(writtenPath(tool("Read", { file_path: "/w/a.md" }))).toBeUndefined();
		expect(writtenPath(tool("Write", { file_path: "/w/a.md" }, "agent-1"))).toBeUndefined();
		expect(writtenPath(tool("Write", {}))).toBeUndefined();
		expect(writtenPath(tool("Write", "nope"))).toBeUndefined();
	});
});

describe("docsOfTurn", () => {
	it("takes the file of a Write, an Edit and a MultiEdit, in call order, once", () => {
		const items = [
			tool("Write", { file_path: "/work/project/docs/plan.md", content: "x" }),
			tool("Edit", { file_path: "/work/project/docs/plan.md", old_string: "a", new_string: "b" }),
			tool("MultiEdit", { file_path: "/work/project/docs/spec.html", edits: [] }),
		];
		expect(docsOfTurn(items)).toEqual(["/work/project/docs/plan.md", "/work/project/docs/spec.html"]);
	});

	it("takes the documents a reply names, in order with the calls around it", () => {
		const items = [
			tool("Write", { file_path: "/work/project/docs/plan.md" }),
			say("Plan in `docs/plan.md`, survey in `plans/demo--survey.md`, and [the log](logs/export.log)."),
			say("See [tmp/batch.json](tmp/batch.json) and ~/notes/today.md."),
		];
		expect(docsOfTurn(items)).toEqual([
			"/work/project/docs/plan.md",
			"docs/plan.md",
			"plans/demo--survey.md",
			"logs/export.log",
			"tmp/batch.json",
			"~/notes/today.md",
		]);
	});

	it("skips URLs, globs, commands and source files", () => {
		const items = [
			say("`https://example.com/a.md` `docs/*.md` `go test ./...` `main.go` `v1.2` `.md` `shots/*.png`"),
			say("and [site](https://example.com/page.html)"),
		];
		expect(docsOfTurn(items)).toEqual([]);
	});

	it("ignores a subagent's own calls and replies, an unrelated tool, and a Write of source code", () => {
		const items = [
			tool("Write", { file_path: "/work/a.md" }, "agent-1"),
			say("`sub/agent.md`", "agent-1"),
			tool("Read", { file_path: "/work/read.md" }),
			tool("Write", { file_path: "/work/main.go" }),
			tool("Write", {}),
		];
		expect(docsOfTurn(items)).toEqual([]);
	});

	it("takes a top-level Write of an image or a video, not only a document", () => {
		expect(
			docsOfTurn([tool("Write", { file_path: "out/chart.png" }), tool("Write", { file_path: "out/run.mp4" })]),
		).toEqual(["out/chart.png", "out/run.mp4"]);
	});

	it("takes the images and videos a reply names beside its documents, but no glob", () => {
		expect(
			docsOfTurn([say("Clip `out/clip.mp4`, chart `out/diagram.png`, notes `docs/n.md`, all `shots/*.png`.")]),
		).toEqual(["docs/n.md", "out/clip.mp4", "out/diagram.png"]);
	});

	it("takes the files a top-level Bash call opens, in call order with the rest", () => {
		const items = [
			tool("Write", { file_path: "docs/plan.md" }),
			tool("Bash", { command: "open out/v.mp4 out/s.png" }),
			say("Done, see `docs/plan.md`."),
		];
		expect(docsOfTurn(items)).toEqual(["docs/plan.md", "out/v.mp4", "out/s.png"]);
	});

	it("never takes a Read, a subagent's open, or a Bash call without a command", () => {
		const items = [
			tool("Read", { file_path: "shots/1.png" }),
			tool("Bash", { command: "open v.mp4" }, "agent-1"),
			tool("Bash", {}),
			tool("Bash", { command: 7 }),
		];
		expect(docsOfTurn(items)).toEqual([]);
	});

	it("keeps what the daemon accepts: at most 64 refs, each at most 1024 bytes", () => {
		const many = Array.from({ length: 70 }, (_, i) => `\`docs/${i}.md\``).join(" ");
		const refs = docsOfTurn([
			say(many),
			say("`more/a.md` `more/b.md`"),
			tool("Write", { file_path: `/${"a".repeat(1100)}.md` }),
		]);
		expect(refs).toHaveLength(64);
		expect(refs[0]).toBe("docs/0.md");
		expect(refs).not.toContain("more/a.md");
	});
});

describe("openedPaths", () => {
	it("takes every showable word after open", () => {
		expect(openedPaths("open a.mp4 b.png")).toEqual(["a.mp4", "b.png"]);
		expect(openedPaths("/usr/bin/open docs/n.md")).toEqual(["docs/n.md"]);
	});

	it("skips open's flags and what they consume, and stops at --args", () => {
		expect(openedPaths("open -a Preview x.png")).toEqual(["x.png"]);
		expect(openedPaths("open -g -n -b com.example.App --env K=v.png x.png")).toEqual(["x.png"]);
		expect(openedPaths("open --stdout o.log --stderr e.log y.mp4")).toEqual(["y.mp4"]);
		expect(openedPaths("open -a App --args z.png")).toEqual([]);
	});

	it("keeps variable assignments of the same command and expands them in later words", () => {
		expect(openedPaths('D=$HOME/k/p; open $D/v.mp4 "$D/s.png"')).toEqual(["~/k/p/v.mp4", "~/k/p/s.png"]);
		expect(openedPaths("export OUT=${HOME}/r\nopen ${OUT}/a.png")).toEqual(["~/r/a.png"]);
		expect(openedPaths("d=~/k && open $d/a.png")).toEqual(["~/k/a.png"]);
		expect(openedPaths("A=/x/y; B=$A/z; open $B/c.mov")).toEqual(["/x/y/z/c.mov"]);
	});

	it("reads quotes, and drops a single-quoted $ that stays text", () => {
		expect(openedPaths("open 'my dir/a.png' \"my dir/b.png\"")).toEqual(["my dir/a.png", "my dir/b.png"]);
		expect(openedPaths("D=/k; open '$D/a.png'")).toEqual([]);
	});

	it("joins a relative path onto an absolute or ~ directory set by cd", () => {
		expect(openedPaths("cd ~/k/plans && open d.png")).toEqual(["~/k/plans/d.png"]);
		expect(openedPaths("cd /w/run/; open ./a.mp4")).toEqual(["/w/run/a.mp4"]);
		expect(openedPaths("cd ~/k && cd plans && open d.png")).toEqual(["~/k/plans/d.png"]);
		expect(openedPaths("cd ~/k && open /abs/e.png")).toEqual(["/abs/e.png"]);
	});

	it("drops a relative path when the directory is not known", () => {
		expect(openedPaths("cd $UNSET && open d.png")).toEqual([]);
		expect(openedPaths("cd sub && open d.png")).toEqual([]);
	});

	it("splits simple commands on newline ; && || | and &", () => {
		const command = "echo hi; open a.png && open b.png || open c.png\nls | open d.png & open e.png";
		expect(openedPaths(command)).toEqual(["a.png", "b.png", "c.png", "d.png", "e.png"]);
	});

	it("drops a word with an unresolved variable, a glob character, a URL or a command substitution", () => {
		expect(openedPaths("open $UNSET/x.png")).toEqual([]);
		expect(openedPaths("open ${UNSET}/x.png")).toEqual([]);
		expect(openedPaths("open https://x.test/a.png file:///tmp/a.png")).toEqual([]);
		expect(openedPaths("open shots/*.png shots/a?.png shots/[ab].png")).toEqual([]);
		expect(openedPaths("open $(ls)/x.png `ls`/y.png")).toEqual([]);
		expect(openedPaths("D=$(mktemp -d); open $D/x.png")).toEqual([]);
		expect(openedPaths("D=`pwd`; open $D/x.png")).toEqual([]);
	});

	it("keeps only showable paths", () => {
		expect(openedPaths("open main.go app.zip http://x.test out/a.png")).toEqual(["out/a.png"]);
	});

	it("skips the body of a heredoc but reads the command line that starts it", () => {
		const command = ["cat <<'EOF' > notes.md", "open x.png", "EOF", "open after.png"].join("\n");
		expect(openedPaths(command)).toEqual(["after.png"]);
		expect(openedPaths("python3 - <<EOF\nopen('y.png')\nEOF")).toEqual([]);
	});

	it("is not a source for commands other than open", () => {
		expect(openedPaths("cat a.png; cp a.png b.png; echo open")).toEqual([]);
		expect(openedPaths("# open x.png")).toEqual([]);
	});

	it("reads an open that follows a keyword or a continued line", () => {
		expect(openedPaths("for f in a; do open a.png; done")).toEqual(["a.png"]);
		expect(openedPaths("open a.png \\\n  b.png")).toEqual(["a.png", "b.png"]);
	});

	it("keeps what the daemon accepts: at most 64 refs, each at most 1024 bytes, once each", () => {
		const many = Array.from({ length: 70 }, (_, i) => `d/${i}.png`).join(" ");
		const refs = openedPaths(`open ${many} d/0.png ${"a".repeat(1100)}.png`);
		expect(refs).toHaveLength(64);
		expect(refs[0]).toBe("d/0.png");
		expect(new Set(refs).size).toBe(64);
	});
});

describe("artifactLinks", () => {
	it.each([
		"https://claude.ai/artifact/MhPWw1HPaNWzw8C5zPuMQ5",
		"https://claude.ai/public/artifacts/abc-123_XY",
		"https://claude.ai/code/artifact/abc123",
	])("recognises %s", (url) => {
		expect(artifactLinks(`Open ${url}.`)).toEqual([artifact(url)]);
	});

	it("titles a markdown link by its text and keeps one card per URL", () => {
		const url = "https://claude.ai/artifact/AbC123";
		expect(artifactLinks(`${url} then [Demo design](${url}) and again ${url}`)).toEqual([artifact(url, "Demo design")]);
	});

	it("lists the links in order of appearance", () => {
		const a = "https://claude.ai/artifact/A1";
		const b = "https://claude.ai/artifact/B2";
		expect(artifactLinks(`${b} and [first](${a})`)).toEqual([artifact(b), artifact(a, "first")]);
	});

	it("ignores other claude.ai pages and other hosts", () => {
		const text = [
			"https://claude.ai/chat/abc123",
			"https://claude.ai/artifacts",
			"https://claude.ai/artifact/",
			"https://claude.ai.evil.example/artifact/abc123",
			"https://example.com/artifact/abc123",
			"http://claude.ai/artifact/abc123",
		].join(" ");
		expect(artifactLinks(text)).toEqual([]);
	});
});

describe("artifactsOfTurn", () => {
	it("collects the links of every reply of the turn, once, and leaves out a subagent's", () => {
		const url = "https://claude.ai/artifact/AbC123";
		expect(
			artifactsOfTurn([say(url), say(`again ${url}`), say("https://claude.ai/code/artifact/z9", "agent-1")]),
		).toEqual([artifact(url)]);
	});
});

describe("turnDocs", () => {
	it("is nothing when the turn names no document", () => {
		expect(turnDocs([say("All done."), tool("Bash", { command: "ls" })])).toBeUndefined();
	});

	it("carries the refs to confirm and the artifact links", () => {
		const url = "https://claude.ai/artifact/AbC123";
		expect(turnDocs([tool("Write", { file_path: "/w/docs/plan.md" }), say(`See ${url}`)])).toEqual({
			refs: ["/w/docs/plan.md"],
			artifacts: [artifact(url)],
		});
	});

	it("is a turn with only an artifact link, which needs no confirming", () => {
		const url = "https://claude.ai/artifact/AbC123";
		expect(turnDocs([say(url)])).toEqual({ refs: [], artifacts: [artifact(url)] });
	});
});

describe("file docs", () => {
	const match = {
		root: "knowledge" as const,
		path: "plans/demo--survey.md",
		kind: "markdown" as const,
		mime: "text/markdown; charset=utf-8",
		size: 14_200,
		modifiedAt: "2026-10-31T10:00:00Z",
	};

	it("takes the card's facts from the daemon's match", () => {
		const doc = fileDocOf(match);
		expect(doc).toEqual({
			source: "file",
			root: "knowledge",
			path: "plans/demo--survey.md",
			kind: "markdown",
			size: 14_200,
			title: "demo--survey.md",
		});
		expect(docMeta(doc)).toBe("knowledge · plans/demo--survey.md · 14 KB");
	});

	it.each([
		[0, "0 B"],
		[512, "512 B"],
		[2048, "2 KB"],
		[1_572_864, "1.5 MB"],
	])("writes %d bytes as %s", (size, text) => {
		expect(docMeta({ ...fileDocOf(match), size })).toMatch(new RegExp(`· ${text}$`));
	});

	it.each([
		["markdown", "a.md", "MD"],
		["html", "a.html", "HTML"],
		["pdf", "a.pdf", "PDF"],
		["json", "a.json", "JSON"],
		["log", "a.log", "LOG"],
		["diff", "a.patch", "DIFF"],
		["text", "a.csv", "TXT"],
		["image", "a.svg", "SVG"],
		["image", "a.png", "PNG"],
		["image", "a.jpeg", "JPG"],
		["video", "a.mp4", "MP4"],
		["video", "a.MOV", "MOV"],
		["video", "a.webm", "WEBM"],
	] as const)("tags a %s file %s as %s", (kind, path, tag) => {
		const doc: FileDoc = { source: "file", root: "worktree", path, kind, size: 1, title: path };
		expect(docTag(doc)).toBe(tag);
	});
});

describe("textView", () => {
	it("numbers the lines of a log from 1, with no grammar", () => {
		const view = textView({ path: "logs/export.log", kind: "log" }, "one\ntwo\n");
		expect(view.lines).toEqual([
			{ kind: "context", num: 1, text: "one" },
			{ kind: "context", num: 2, text: "two" },
		]);
		expect(view.language).toBeUndefined();
		expect(view.hidden).toBe(0);
	});

	it("pretty-prints JSON and highlights it as JSON", () => {
		const view = textView({ path: "a.json", kind: "json" }, '{"a":1,"b":[true]}');
		expect(view.lines.map((l) => l.text)).toEqual(["{", '  "a": 1,', '  "b": [', "    true", "  ]", "}"]);
		expect(view.language).toBe("json");
	});

	it("shows JSON that does not parse as it is", () => {
		const view = textView({ path: "a.json", kind: "json" }, '{"a":');
		expect(view.lines.map((l) => l.text)).toEqual(['{"a":']);
	});

	it("gives a diff the diff grammar", () => {
		expect(textView({ path: "x.patch", kind: "diff" }, "-a\n+b").language).toBe("diff");
	});

	it("keeps the first lines and counts the rest as hidden", () => {
		const view = textView({ path: "a.log", kind: "log" }, "1\n2\n3\n4\n5", 3);
		expect(view.lines).toHaveLength(3);
		expect(view.hidden).toBe(2);
	});

	it("is no lines for an empty file", () => {
		expect(textView({ path: "a.txt", kind: "text" }, "").lines).toEqual([]);
	});
});
