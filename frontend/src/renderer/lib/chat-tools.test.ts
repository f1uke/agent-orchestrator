import { describe, expect, it } from "vitest";
import type { TaskState } from "./chat-event";
import type { BackgroundTask, ToolCallItem } from "./chat-fold";
import {
	describeTool,
	processKind,
	type ProcessKind,
	processSentence,
	liveVerb,
	shortenPaths,
	summarizeTools,
	taskLabel,
	toolFailed,
	waitingVerb,
} from "./chat-tools";

const ROOT = "/work/project";

function call(
	name: string,
	input: unknown,
	result?: { isError?: boolean; content?: unknown },
	id = name,
): ToolCallItem {
	return {
		kind: "tool_call",
		id,
		name,
		parentToolUseId: "",
		input,
		startedTs: "",
		result: result ? { isError: result.isError ?? false, content: result.content ?? "" } : undefined,
	};
}

function task(state: TaskState, extra: Partial<BackgroundTask> = {}): BackgroundTask {
	return { id: "t1", toolUseId: "tu", description: "", taskType: "local_bash", state, ...extra };
}

describe("shortenPaths", () => {
	it.each([
		["/work/project/src/a.go", "src/a.go"],
		["/work/project", "."],
		["cd /work/project && go test ./...", "cd . && go test ./..."],
		['grep -n x "/work/project/a b.go"', 'grep -n x "a b.go"'],
		["/work/project-two/src/a.go", "/work/project-two/src/a.go"],
		["/other/work/project/src/a.go", "/other/work/project/src/a.go"],
		["/work/projects", "/work/projects"],
		["/work/project.", "/work/project."],
	])("%s becomes %s", (text, want) => {
		expect(shortenPaths(text, ROOT)).toBe(want);
	});

	it("tolerates a trailing slash on the root and leaves text alone without one", () => {
		expect(shortenPaths("/work/project/x", `${ROOT}/`)).toBe("x");
		expect(shortenPaths("/work/project/x", "")).toBe("/work/project/x");
		expect(shortenPaths("/work/project/x", undefined)).toBe("/work/project/x");
	});
});

describe("describeTool path shortening", () => {
	it("shortens a Bash command in the head and the body", () => {
		const view = describeTool(call("Bash", { command: "cd /work/project && go test ./..." }), { root: ROOT });
		expect(view.title).toBe("cd . && go test ./...");
		expect(view.body).toMatchObject({ kind: "bash", command: "cd . && go test ./..." });
	});

	it.each([
		["Read", { file_path: "/work/project/ledger/ledger.go" }, "ledger/ledger.go"],
		["Edit", { file_path: "/work/project/a.go", old_string: "a", new_string: "b" }, "a.go"],
		["Write", { file_path: "/work/project/new.go", content: "x" }, "new.go"],
	])("keeps the shortened %s path for the tooltip and shows the base name", (name, input, path) => {
		const view = describeTool(call(name, input), { root: ROOT });
		expect(view.path).toBe(path);
		expect(view.arg).toBe(path.split("/").at(-1));
	});

	it.each([
		["Grep", { pattern: "Transfer", path: "/work/project/ledger" }, '"Transfer" in ledger'],
		["Glob", { pattern: "*.go", path: "/work/project" }, "*.go in ."],
	])("shortens the %s argument", (name, input, want) => {
		expect(describeTool(call(name, input), { root: ROOT }).arg).toBe(want);
	});

	it("keeps an absolute path when no root is given", () => {
		expect(describeTool(call("Read", { file_path: "/work/project/a.go" })).path).toBe("/work/project/a.go");
	});
});

describe("describeTool Read of an image", () => {
	const png = { type: "image", source: { type: "base64", media_type: "image/png", data: "iVBORw==" } };
	const read = (content: unknown) => call("Read", { file_path: "/work/project/docs/shot.png" }, { content });

	it("shows the image bytes with the path instead of an empty output", () => {
		const view = describeTool(read([png]), { root: ROOT });
		expect(view.body).toEqual({
			kind: "image",
			images: [{ mediaType: "image/png", base64: "iVBORw==" }],
			path: "docs/shot.png",
		});
		expect(view.status).toEqual({ tone: "ok", text: "image" });
	});

	it("counts several images in the status", () => {
		expect(describeTool(read([png, png]), { root: ROOT }).status).toEqual({ tone: "ok", text: "2 images" });
	});

	it("keeps text that is not numbered as output", () => {
		const view = describeTool(read("package x\nfunc x() {}\n"), { root: ROOT });
		expect(view.body).toEqual({ kind: "output", text: "package x\nfunc x() {}\n" });
		expect(view.status).toEqual({ tone: "ok", text: "2 lines" });
	});

	it("keeps a Read whose image blocks are all unusable as output", () => {
		const unknown = { type: "image", source: { type: "base64", media_type: "image/tiff", data: "AA" } };
		expect(describeTool(read([unknown]), { root: ROOT }).body.kind).toBe("output");
	});
});

describe("describeTool code cards", () => {
	const hunk = { oldStart: 456, oldLines: 3, newStart: 456, newLines: 4, lines: [" a", "-b", "+c", "+d", " e"] };
	const edit = (extra: { structuredPatch?: (typeof hunk)[] } = {}) =>
		call("Edit", { file_path: "/work/project/report/report.go", old_string: "a\nb\ne", new_string: "a\nc\nd\ne" }, {
			content: "updated",
			...extra,
		} as { content: string });

	it("reads a Write as Created with the base name and the line count", () => {
		const content = Array.from({ length: 15 }, (_, i) => `<l${i}/>`).join("\n");
		const view = describeTool(call("Write", { file_path: "/work/project/docs/demo-flow.svg", content }), {
			root: ROOT,
		});
		expect(view).toMatchObject({
			title: "Created",
			arg: "demo-flow.svg",
			path: "docs/demo-flow.svg",
			status: { tone: "diff", added: 15, removed: 0 },
			body: { kind: "code", mode: "write", view: { language: "xml", hidden: 0 } },
		});
	});

	it("reads an Edit as Edited, counting the lines of its patch", () => {
		const view = describeTool(
			{ ...edit(), result: { isError: false, content: "updated", structuredPatch: [hunk] } },
			{ root: ROOT },
		);
		expect(view).toMatchObject({ title: "Edited", arg: "report.go", status: { tone: "diff", added: 2, removed: 1 } });
		expect(view.body).toMatchObject({ kind: "code", mode: "edit" });
		if (view.body.kind !== "code") throw new Error("not a code body");
		expect(view.body.view.lines.map((l) => l.num)).toEqual([456, 457, 457, 458, 459]);
	});

	it("falls back to the diff of the strings, numbered from 1, when the result has no patch", () => {
		const view = describeTool(edit());
		if (view.body.kind !== "code") throw new Error("not a code body");
		expect(view.body.view.lines.map((l) => l.num)).toEqual([1, 2, 2, 3, 4]);
	});

	it("reads a MultiEdit as Edited", () => {
		const view = describeTool(call("MultiEdit", { file_path: "a.go", edits: [{ old_string: "a", new_string: "b" }] }));
		expect(view).toMatchObject({ title: "Edited", body: { kind: "code", mode: "edit" } });
	});

	it("keeps an Edit whose input has no strings as a generic card", () => {
		expect(describeTool(call("Edit", { file_path: "a.go" })).body.kind).toBe("generic");
	});

	it("shows a text Read as code with the file's own line numbers", () => {
		const view = describeTool(
			call("Read", { file_path: "/work/project/src/budget.ts" }, { content: "    10\texport const a = 1;\n    11\t" }),
			{ root: ROOT },
		);
		expect(view).toMatchObject({ title: "Read", arg: "budget.ts", status: { tone: "ok", text: "2 lines" } });
		expect(view.body).toMatchObject({
			kind: "code",
			mode: "read",
			view: { language: "typescript", lines: [{ num: 10 }, { num: 11 }] },
		});
	});

	it("keeps the error text of a failed Read as output", () => {
		const view = describeTool(call("Read", { file_path: "a.go" }, { isError: true, content: "File does not exist." }));
		expect(view.body).toEqual({ kind: "output", text: "File does not exist." });
	});
});

describe("describeTool background Bash", () => {
	const bg = (result?: boolean) =>
		call(
			"Bash",
			{ command: "./slow.sh", run_in_background: true },
			result ? { content: "Command running in background" } : undefined,
		);

	it("is running while its result has not come", () => {
		expect(describeTool(bg(false), { task: task("running") }).status).toEqual({ tone: "running" });
	});

	it.each<[TaskState, number | undefined, object]>([
		["running", undefined, { tone: "running", text: "running in background" }],
		["done", 0, { tone: "ok", text: "done · exit 0" }],
		["done", undefined, { tone: "ok", text: "done" }],
		["error", 1, { tone: "error", text: "failed · exit 1" }],
		["stopped", undefined, { tone: "idle", text: "stopped" }],
	])("takes its task's %s state (exit %s)", (state, exitCode, want) => {
		expect(describeTool(bg(true), { task: task(state, { exitCode }) }).status).toEqual(want);
	});

	it("does not claim it is running without a task", () => {
		expect(describeTool(bg(true)).status).toEqual({ tone: "ok", text: "started in background" });
	});

	it("leaves a foreground Bash reading done once it has a result", () => {
		const view = describeTool(call("Bash", { command: "ls" }, { content: "x" }), { task: task("running") });
		expect(view.status).toEqual({ tone: "ok", text: "done" });
	});
});

describe("describeTool Agent with a task", () => {
	const agent = call(
		"Agent",
		{ subagent_type: "Explore", description: "docs" },
		{ content: "Async agent launched successfully." },
	);

	it("is running until its task ends, though the launch result came at once", () => {
		expect(describeTool(agent, { task: task("running", { taskType: "local_agent" }) }).status).toEqual({
			tone: "running",
		});
	});

	it("is done with the task's tool count and shows the task's final text", () => {
		const view = describeTool(agent, {
			task: task("done", {
				taskType: "local_agent",
				summary: "Two files.",
				usage: { totalTokens: 1, toolUses: 2, durationMs: 3 },
			}),
		});
		expect(view.status).toEqual({ tone: "ok", text: "done · 2 tool uses" });
		expect(view.body).toEqual({ kind: "output", text: "Two files." });
	});

	it.each<[TaskState, object]>([
		["error", { tone: "error", text: "failed" }],
		["stopped", { tone: "idle", text: "stopped" }],
	])("takes %s from its task", (state, want) => {
		expect(describeTool(agent, { task: task(state, { taskType: "local_agent" }) }).status).toEqual(want);
	});

	it("falls back to the result and the counted tools without a task", () => {
		const view = describeTool(agent, { subagentTools: 3 });
		expect(view.status).toEqual({ tone: "ok", text: "done · 3 tool uses" });
		expect(view.body).toEqual({ kind: "output", text: "Async agent launched successfully." });
	});
});

describe("describeTool todo tools", () => {
	it("titles TaskCreate and TaskUpdate as todos with a short argument", () => {
		const create = describeTool(call("TaskCreate", { subject: "Find failing tests", description: "d" }));
		expect(create).toMatchObject({ family: "todo", title: "Add todo", arg: "Find failing tests" });
		const update = describeTool(call("TaskUpdate", { taskId: "1", status: "completed" }));
		expect(update).toMatchObject({ family: "todo", title: "Update todo", arg: "#1 completed" });
	});

	it("titles TodoWrite as todos with the item count", () => {
		const view = describeTool(call("TodoWrite", { todos: [{}, {}] }));
		expect(view).toMatchObject({ family: "todo", title: "Todos", arg: "2 items" });
	});
});

describe("process summaries", () => {
	const calls = [
		call("Bash", { command: "a" }, { content: "" }, "b1"),
		call("Bash", { command: "b" }, { isError: true, content: "" }, "b2"),
		call("Read", { file_path: "/x/a.go" }, undefined, "r1"),
		call("Read", { file_path: "/x/a.go" }, undefined, "r2"),
		call("Edit", { file_path: "/x/a.go" }, undefined, "e1"),
		call("Edit", { file_path: "/x/a.go" }, undefined, "e2"),
		call("TaskUpdate", { taskId: "1" }, undefined, "u1"),
		call("TaskUpdate", { taskId: "2" }, undefined, "u2"),
		call("ToolSearch", {}, undefined, "s1"),
	];

	it("counts each kind once per command and once per file", () => {
		const summary = summarizeTools(calls);
		expect(summary.counts).toEqual([
			{ kind: "commands", count: 2 },
			{ kind: "reads", count: 1 },
			{ kind: "edits", count: 1 },
			{ kind: "todos", count: 2 },
			{ kind: "tools", count: 1 },
		]);
		expect(summary.failed).toBe(1);
	});

	it("says the counts as one sentence, with todos unnumbered", () => {
		expect(processSentence(summarizeTools(calls))).toBe(
			"Ran 2 commands, read a file, edited a file, updated todos, used another tool",
		);
	});

	it.each<[ProcessKind, number, string]>([
		["commands", 1, "Ran a command"],
		["commands", 3, "Ran 3 commands"],
		["reads", 1, "Read a file"],
		["reads", 2, "Read 2 files"],
		["searches", 1, "Ran a search"],
		["searches", 4, "Ran 4 searches"],
		["edits", 1, "Edited a file"],
		["writes", 2, "Wrote 2 files"],
		["agents", 1, "Started an agent"],
		["agents", 2, "Started 2 agents"],
		["web", 1, "Made a web request"],
		["skills", 1, "Ran a skill"],
		["skills", 3, "Ran skill 3 times"],
		["tools", 2, "Used 2 other tools"],
	])("says %s x%i as %j", (kind, count, want) => {
		expect(processSentence({ counts: [{ kind, count }], failed: 0 })).toBe(want);
	});

	it("counts a denied tool as failed", () => {
		const denied: ToolCallItem = {
			...call("Bash", { command: "rm" }),
			permission: { requestId: "r", status: "denied", message: "", updatedPermissions: [] },
		};
		expect(toolFailed(denied)).toBe(true);
		expect(summarizeTools([denied]).failed).toBe(1);
	});

	it.each([
		["WebFetch", "web"],
		["Glob", "searches"],
		["Task", "agents"],
		["Mystery", "tools"],
	])("files %s under %s", (name, kind) => {
		expect(processKind(name)).toBe(kind);
	});
});

describe("liveVerb", () => {
	it.each<[string, object, string]>([
		["Bash", { command: "go test ./..." }, "Running a command"],
		["Read", { file_path: "/work/project/docs/README.md" }, "Reading README.md"],
		["Skill", { skill: "artifact-design" }, "Running skill /artifact-design"],
		["Agent", { subagent_type: "Explore", description: "find callers" }, "Running agent Explore"],
		["Task", {}, "Running an agent"],
		["Grep", { pattern: "x" }, "Searching"],
		["Glob", { pattern: "*.go" }, "Searching"],
		["Edit", { file_path: "internal/webhook/sender.go" }, "Editing sender.go"],
		["Write", { file_path: "budget.go" }, "Editing budget.go"],
		["WebFetch", { url: "https://example.com" }, "Running WebFetch"],
	])("says a running %s call in plain words", (name, input, want) => {
		expect(liveVerb(call(name, input))).toBe(want);
	});
});

describe("taskLabel", () => {
	it.each([
		["local_bash", "Bash · background"],
		["local_agent", "Agent"],
		["something_new", "Task"],
		["", "Task"],
	])("%j reads %j", (type, want) => {
		expect(taskLabel(type)).toBe(want);
	});
});

describe("waitingVerb", () => {
	const command = (description = "") => task("running", { description });
	const agent = (description = "") => task("running", { taskType: "local_agent", description });
	it.each<[string, BackgroundTask[], string]>([
		[
			"a command by its description",
			[command("Run the soak test")],
			'Waiting on background command "Run the soak test"',
		],
		["a command with no description", [command()], "Waiting on 1 background command"],
		["an agent by its description", [agent("Summarize docs")], 'Waiting on agent "Summarize docs"'],
		["two commands", [command("a"), command("b")], "Waiting on 2 background commands"],
		["two agents", [agent("a"), agent("b")], "Waiting on 2 agents"],
		["a command and an agent", [command("a"), agent("b")], "Waiting on 2 background tasks"],
	])("says %s", (_, tasks, want) => {
		expect(waitingVerb(tasks)).toBe(want);
	});
});
