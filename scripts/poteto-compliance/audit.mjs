import { playbookRead, todoText } from "../playbook-adherence/transcript.mjs";

const SYSTEM_PREFIXES = [
	"<task-notification",
	"<command-",
	"<local-command",
	"This session is being continued",
	"Another Claude session",
	"[Request interrupted",
	"[Image",
	"Base directory for this skill",
];

function userText(content) {
	if (typeof content === "string") return content;
	if (!Array.isArray(content) || content.some((b) => b.type === "tool_result")) return null;
	return content
		.filter((b) => b.type === "text")
		.map((b) => b.text)
		.join("\n");
}

export function timeline(events) {
	const out = [];
	const byId = new Map();
	for (const e of events) {
		if (e.isSidechain || e.parent_tool_use_id) continue;
		if (e.type === "assistant") {
			for (const b of e.message?.content ?? []) {
				if (b.type === "tool_use") {
					const step = { kind: "tool", name: b.name, input: b.input ?? {}, result: "" };
					byId.set(b.id, step);
					out.push(step);
				} else if (b.type === "text" && b.text.trim()) out.push({ kind: "text", text: b.text.trim() });
			}
		} else if (e.type === "user") {
			const content = e.message?.content;
			if (Array.isArray(content))
				for (const b of content)
					if (b.type === "tool_result" && byId.has(b.tool_use_id))
						byId.get(b.tool_use_id).result =
							typeof b.content === "string" ? b.content : (b.content ?? []).map((c) => c.text ?? "").join("\n");
			const raw = userText(content)?.trim();
			// A brief sent as "/pstack:poteto-mode <brief>" arrives as the command's args.
			const text = raw?.match(/<command-args>([\s\S]*?)<\/command-args>/)?.[1].trim() || raw;
			if (!text || e.isMeta || e.isCompactSummary || SYSTEM_PREFIXES.some((p) => text.startsWith(p))) continue;
			out.push({ kind: "human", text });
		}
	}
	return out.map((step, i) => ({ ...step, i }));
}

const commandOf = (s) => (s.kind === "tool" && s.name === "Bash" ? String(s.input.command ?? "") : "");
const pathOf = (s) => String(s.input?.file_path ?? s.input?.notebook_path ?? "");
const skillOf = (s) =>
	s.kind === "tool" && s.name === "Skill" ? String(s.input.skill ?? "").replace(/^pstack:/, "") : "";

const NOT_REPO = /\.claude\/|\.ao\/knowledge|\/tmp\/|\/private\/|scratchpad|todo\.md$/;
const TEST_FILE =
	/(^|\/)(test|tests|__tests__|spec|[A-Za-z]+Tests)\/|[._-](test|spec)\.[a-z]+$|_test\.go$|Tests?\.swift$/;

export const isTestFile = (path) => TEST_FILE.test(path);
const isRepoEdit = (s) =>
	s.kind === "tool" && ["Edit", "Write", "MultiEdit", "NotebookEdit"].includes(s.name) && !NOT_REPO.test(pathOf(s));
const isSourceEdit = (s) => isRepoEdit(s) && !isTestFile(pathOf(s));
const isCodeDelegate = (s) => s.kind === "tool" && s.name === "Agent" && s.input.isolation === "worktree";

const isCommit = (s) => /\bgit\s+(-C\s+\S+\s+)?commit\b/.test(commandOf(s));
const isPush = (s) => /\bgit\s+(-C\s+\S+\s+)?push\b/.test(commandOf(s));
const isPrOpen = (s) => /\b(gh\s+pr\s+create|glab\s+mr\s+create)\b/.test(commandOf(s)) || /^open-mr/.test(skillOf(s));
const isCrewReview = (s) => /\bao\s+crew\s+review\b/.test(commandOf(s));
const MERGE =
	/\b(gh\s+pr\s+(merge|ready)|glab\s+mr\s+(merge|approve)|glab\s+mr\s+update\b[^\n]*--ready|merge_when_pipeline_succeeds|--auto-merge|gh\s+pr\s+merge)\b/;
const isMerge = (s) => MERGE.test(commandOf(s));
const OUTWARD_CMD =
	/\b(git\s+(-C\s+\S+\s+)?push|gh\s+pr\s+(create|edit|comment|review|close|merge|ready)|gh\s+issue\s+(create|comment|edit)|glab\s+mr\s+(create|update|note|merge|approve|close)|glab\s+api\s+[^\n]*-X\s*(POST|PUT|DELETE))\b/;
const OUTWARD_MCP = /atlassian__(create|edit|addOrEdit|transition|update|executeWrite|executeDestructive)/;
const isOutward = (s) =>
	OUTWARD_CMD.test(commandOf(s)) || /^open-mr/.test(skillOf(s)) || (s.kind === "tool" && OUTWARD_MCP.test(s.name));

const EXEC_PROGRAMS =
	/^(node|python3?|go|npm|npx|pnpm|yarn|bun|deno|xcodebuild|xcrun|swift|maestro|curl|make|cargo|pytest|jest|vitest|playwright|bash|sh|zsh|bin\/flow|\S*\/bin\/flow|\.\/\S+|\S+\.sh)$/;
export function isScriptRun(s) {
	if (s.kind === "tool" && s.name === "Skill") return /^(run|verify|finnomena-app-e2e)$/.test(skillOf(s));
	if (/\bao\s+(sim|preview)\b/.test(commandOf(s))) return true;
	return commandOf(s)
		.split(/&&|\|\||;|\||\n/)
		.map((seg) =>
			seg
				.trim()
				.replace(/^(\w+=\S+\s+)+/, "")
				.replace(/^(time|env|timeout \d+)\s+/, ""),
		)
		.some((seg) => EXEC_PROGRAMS.test(seg.split(/\s+/)[0]));
}

const TEST_RUN =
	/\b(flow\s+run|maestro\s+test|ao\s+sim\s+\w+|xcodebuild\b[^\n]*\btest\b|go\s+test|node\s+--test|npm\s+(run\s+)?test|npx\s+(vitest|jest|playwright)|vitest|jest|pytest|swift\s+test|cargo\s+test)\b/;
const isReproRun = (s) => /^(run|verify|finnomena-app-e2e)$/.test(skillOf(s)) || TEST_RUN.test(commandOf(s));

const firstIndex = (t, pred) => t.find(pred)?.i ?? null;
const lastIndexBefore = (t, pred, before) => t.filter((s) => s.i < before && pred(s)).at(-1)?.i ?? null;

export function replies(t) {
	const out = [];
	let last = null;
	for (const s of [...t, { kind: "human" }]) {
		if (s.kind === "text") last = s;
		else if (s.kind === "human" && last) {
			out.push(last);
			last = null;
		}
	}
	return out;
}

const stripCode = (text) =>
	text
		.replace(/```[\s\S]*?```/g, "")
		.replace(/`[^`\n]*`/g, "")
		.replace(/https?:\/\/\S+/g, "");

export const emDashes = (text) => (stripCode(text).match(/[—–]/g) ?? []).length;

export function midSentenceColons(text) {
	return stripCode(text)
		.replace(/"[^"\n]*"|“[^”\n]*”/g, "")
		.split("\n")
		.filter((line) => !/^\s*\|/.test(line))
		.flatMap((line) => {
			const item = line.replace(/\*\*/g, "").match(/^\s*(?:[-*]|\d+\.)\s+([^:]*):\s/);
			const body =
				item && item[1].trim().split(/\s+/).length <= 3 ? line.replace(/\*\*/g, "").slice(item[0].length) : line;
			return [...body.matchAll(/[\p{L})*][:：]\**(?=[ \t]+\S)/gu)].filter((m) => {
				const clause = body.slice(m.index + m[0].length).split(/[.!?](\s|$)/)[0];
				return !/,[^,]+(,| and | or )/.test(clause);
			});
		}).length;
}

const verdict = (v, why = "") => ({ verdict: v, why });
const pass = (why) => verdict("pass", why);
const fail = (why) => verdict("fail", why);
const na = (why) => verdict("n/a", why);

const normalize = (text) =>
	text
		.replace(/\[[ xX]\]/g, " ")
		.toLowerCase()
		.replace(/[^a-z0-9 ]+/g, " ")
		.replace(/\s+/g, " ")
		.trim();

export function todoState(t, upTo = Infinity) {
	const items = new Map();
	let next = 1;
	for (const s of t) {
		if (s.i >= upTo || s.kind !== "tool") continue;
		if (s.name === "TaskCreate") {
			const id = s.result.match(/#(\d+)/)?.[1] ?? String(next);
			next = Number(id) + 1;
			items.set(id, { text: `${s.input.subject ?? ""} ${s.input.description ?? ""}`, status: "pending" });
		} else if (s.name === "TaskUpdate" && items.has(String(s.input.taskId))) {
			const item = items.get(String(s.input.taskId));
			if (s.input.status) item.status = s.input.status;
			if (s.input.subject) item.text = `${s.input.subject} ${item.text}`;
			if (s.input.description) item.text += ` ${s.input.description}`;
		} else if (s.name === "TodoWrite") {
			items.clear();
			(s.input.todos ?? []).forEach((todo, k) => items.set(`w${k}`, { text: todo.content ?? "", status: todo.status }));
		} else if (todoText(s) !== null) {
			const text = s.name === "Bash" ? commandOf(s) : String(s.input.content ?? s.input.new_string ?? "");
			if (s.name !== "Edit") for (const key of [...items.keys()]) if (key.startsWith("md")) items.delete(key);
			text
				.split("\n")
				.filter((line) => /^\s*[-*]\s|^\s*\d+\./.test(line))
				.forEach((line) =>
					items.set(`md${items.size}:${line}`, { text: line, status: /\[[xX]\]/.test(line) ? "completed" : "pending" }),
				);
		}
	}
	return [...items.values()].map((item) => ({
		text: normalize(item.text),
		closed: item.status === "completed" || item.status === "deleted" || /\b(skip|n a)\b/.test(normalize(item.text)),
	}));
}

const firstWords = (step) => normalize(step).split(" ").slice(0, 4).join(" ");

export function taskKind(t, branch = "") {
	const read = t.map((s) => (s.kind === "tool" ? playbookRead(s) : null)).filter(Boolean);
	const main = read.find((p) => p !== "opening-a-pr");
	if (main) return { kind: main, source: "playbook read" };
	if (/^(fix|bugfix|hotfix)\//.test(branch)) return { kind: "bug-fix", source: "branch prefix" };
	if (/^perf\//.test(branch)) return { kind: "perf-issue", source: "branch prefix" };
	return { kind: null, source: "unknown" };
}

const PERF_NUMBER =
	/\b\d+(\.\d+)?\s?(ms|µs|us|ns|s|sec|seconds|x|×|%|MB|KB|ops\/s|req\/s)(?![\p{L}\p{N}])[^\n]{0,80}\b(faster|slower|speed-?up|regress\w*|latency|throughput|p50|p95|p99)\b|\b(faster|slower|speed-?up|regress\w*)\b[^\n]{0,40}\b\d+(\.\d+)?\s?(ms|x|×|%)(?![\p{L}\p{N}])/iu;
const VERIFIED_CLAIM =
	/\b(verified|confirmed working|tests? (all )?pass(es|ed)?|all (tests )?green|it works|works end to end)\b|ยืนยันแล้ว|ทดสอบแล้ว|เทสผ่าน|ผ่านหมด|รันผ่าน/i;
const YES = /^\s*(y|yes|ok|okay|go|lgtm|ship it|sure|approved?|1)\b|ได้เลย|เอาเลย|ทำเลย|โอเค|ตกลง|ใช่|อนุมัติ/i;
const isQuestion = (text) => /[?？]\s*$|ไหม\s*$|มั้ย\s*$/.test(text.trim());
const BRIEF_AUTHORIZES =
	/\b(open|create|raise|leave|ship)\b[^\n]{0,40}\b(pr|mr|pull request|merge request)\b|\b(pr|mr)\s+(into|against|to|body|link)\b|\bpush\b/i;
const SURFACE = [
	[/\b(git\s+(-C\s+\S+\s+)?push)\b/, /push/i],
	[/\b(gh\s+pr|glab\s+mr|gh\s+issue|glab\s+api)\b|^open-mr/, /\b(pr|mr)\b|merge request|pull request/i],
	[/atlassian__/, /jira|การ์ด|card|\bac\b|ticket|confluence/i],
];
const askedFor = (action, text) =>
	SURFACE.some(([act, words]) => act.test(commandOf(action) || skillOf(action) || action.name) && words.test(text));

export const RULES = [
	{
		id: "playbook-read",
		group: "set-up",
		rule: "A playbook file under poteto-mode/playbooks/ is read.",
		detect: "Read, or cat/sed/head of poteto-mode/playbooks/<name>.md in the main thread.",
		risks: "FN: a playbook read only inside a subagent. FP: a read that was not acted on.",
		check: (t) => (t.some((s) => s.kind === "tool" && playbookRead(s)) ? pass() : fail("no playbook file read")),
	},
	{
		id: "todolist-has-steps",
		group: "set-up",
		rule: "The todolist holds every numbered step of the playbook read.",
		detect:
			"First four words of each step found in any TaskCreate, TodoWrite or todo.md text written during the run (same match as #422).",
		risks: "FN: a step reworded beyond its first four words. FP: a step pasted but never tracked.",
		check: (t, ctx) => {
			if (!ctx.kind || ctx.kindSource !== "playbook read") return na("no playbook read");
			const steps = ctx.steps(ctx.kind).map(firstWords);
			const todo = t
				.filter((s) => s.kind === "tool")
				.map(todoText)
				.filter((x) => x !== null)
				.map(normalize)
				.join("\n");
			const missing = steps.filter((s) => !todo.includes(s));
			return missing.length ? fail(`${steps.length - missing.length}/${steps.length} steps in the todolist`) : pass();
		},
	},
	{
		id: "steps-closed-at-pr",
		group: "set-up",
		rule: "When the PR opens, every playbook step is done or carries skip/n-a.",
		detect:
			"Replays TaskCreate/TaskUpdate/TodoWrite/todo.md up to the first PR-open call; each step's item is completed or says skip:/n/a:.",
		risks:
			"FN: steps closed in a todo.md edited by a shell command the parser misses. FP: an item ticked without the work.",
		check: (t, ctx) => {
			const at = firstIndex(t, isPrOpen);
			if (at === null) return na("no PR opened in the transcript");
			if (!ctx.kind || ctx.kindSource !== "playbook read") return na("no playbook read");
			const items = todoState(t, at);
			const open = ctx
				.steps(ctx.kind)
				.map(firstWords)
				.filter((step) => !items.some((x) => x.text.includes(step) && x.closed));
			return open.length ? fail(`${open.length} step(s) open or missing at PR open`) : pass();
		},
	},
	{
		id: "deslop-before-commit",
		group: "skill timing",
		rule: "deslop runs before the commit that ships (the last commit before the first push, or the last commit).",
		detect: "Skill deslop in the main thread before that git commit.",
		risks: "FN: deslop run by a subagent that wrote the commit. FP: deslop run early, then more code committed.",
		check: (t) => {
			const push = firstIndex(t, (s) => isPush(s) || isPrOpen(s)) ?? Infinity;
			const commit = lastIndexBefore(t, isCommit, push);
			if (commit === null) return na("no commit in the main thread");
			return firstIndex(t, (s) => skillOf(s) === "deslop" && s.i < commit) !== null
				? pass()
				: fail("no deslop before it");
		},
	},
	{
		id: "no-comments-before-review",
		group: "skill timing",
		rule: "no-comments runs before review (PR open or ao crew review).",
		detect: "Skill no-comments, or an Agent of type pstack:comment-sicko, before the first review call.",
		risks: "FN: run by a subagent. FP: run before later code was added.",
		check: (t) => {
			const at = firstIndex(t, (s) => isPrOpen(s) || isCrewReview(s));
			if (at === null) return na("no PR open or crew review");
			const ran = t.some(
				(s) =>
					s.i < at &&
					(skillOf(s) === "no-comments" ||
						(s.kind === "tool" && s.name === "Agent" && /comment-sicko/.test(s.input.subagent_type ?? ""))),
			);
			return ran ? pass() : fail("no no-comments before review");
		},
	},
	{
		id: "prose-skill-before-pr-body",
		group: "skill timing",
		rule: "unslop or technical-writing runs before the PR body is written.",
		detect: "Skill unslop / technical-writing (or the older no-ai-slop) before the first PR-open call.",
		risks:
			"FN: the open-mr skill writes the body and the worker relied on fluke-writing-style. FP: unslop loaded for an earlier reply only.",
		check: (t) => {
			const at = firstIndex(t, isPrOpen);
			if (at === null) return na("no PR opened in the transcript");
			return t.some((s) => s.i < at && /^(unslop|technical-writing|no-ai-slop)$/.test(skillOf(s)))
				? pass()
				: fail("no unslop/technical-writing before PR open");
		},
	},
	{
		id: "benchmark-before-perf-number",
		group: "skill timing",
		rule: "benchmark-checklist runs before a measured perf number is reported.",
		detect:
			"First worker text with a number+unit next to faster/slower/speedup/latency/throughput; benchmark-checklist must come before it.",
		risks: "FP: a quoted or predicted number, not a measurement. FN: a number phrased without the trigger words.",
		check: (t) => {
			const at = firstIndex(t, (s) => s.kind === "text" && PERF_NUMBER.test(stripCode(s.text)));
			if (at === null) return na("no perf number reported");
			return t.some((s) => s.i < at && skillOf(s) === "benchmark-checklist")
				? pass()
				: fail("perf number before benchmark-checklist");
		},
	},
	{
		id: "bugfix-repro-before-edit",
		group: "bug fix",
		rule: "A bug fix reproduces (driver skill, flow run, or a test run) before the first source edit.",
		detect:
			"Skill run/verify/finnomena-app-e2e or a test-runner/flow/maestro/ao sim command before the first non-test repo edit or worktree delegate.",
		risks:
			"FP: a test run that does not exercise the bug counts as a repro. FN: a repro by hand in a shell script under another name.",
		check: (t, ctx) => {
			if (ctx.kind !== "bug-fix") return na("not a bug fix");
			const edit = firstIndex(t, (s) => isSourceEdit(s) || isCodeDelegate(s));
			if (edit === null) return na("no source edit in the main thread");
			return t.some((s) => s.i < edit && isReproRun(s)) ? pass() : fail("first source edit before any repro run");
		},
	},
	{
		id: "bugfix-failing-test-first",
		group: "bug fix",
		rule: "In a bug fix, a commit with only test files lands before the first commit touching source.",
		detect: "git log --reverse --name-only of the branch; file paths classified by test-path patterns.",
		risks:
			"FP: a test-only commit unrelated to the bug. FN: test and fix squashed on merge (branch then reads as one commit), or a test path the patterns miss.",
		check: (t, ctx) => {
			if (ctx.kind !== "bug-fix") return na("not a bug fix");
			if (!ctx.commits) return na("branch commits unavailable");
			const source = ctx.commits.findIndex((c) => c.files.some((f) => !isTestFile(f)));
			if (source === -1) return na("no source commit");
			const testFirst = ctx.commits.slice(0, source).some((c) => c.files.length && c.files.every(isTestFile));
			return testFirst ? pass() : fail("no test-only commit before the first source commit");
		},
	},
	{
		id: "verified-claims-backed",
		group: "evidence",
		rule: "Every 'verified/tests pass' claim follows a script run made after the last code edit.",
		detect:
			"Worker text matching verified/confirmed/tests pass (English and Thai); needs a script run (test runner, build, node/python, flow, curl, ao sim) between the last repo edit and the claim.",
		risks:
			"FP: a claim about reading, not running (e.g. 'verified in the docs'). FN: any unrelated script run satisfies it.",
		check: (t) => {
			const claims = t.filter((s) => s.kind === "text" && VERIFIED_CLAIM.test(stripCode(s.text)));
			if (!claims.length) return na("no verified claim");
			const bare = claims.filter((c) => {
				const since = lastIndexBefore(t, (s) => isRepoEdit(s) || isCodeDelegate(s), c.i) ?? -1;
				return !t.some((s) => s.i > since && s.i < c.i && isScriptRun(s));
			});
			return bare.length
				? fail(`${bare.length}/${claims.length} claims without a script run after the last edit`)
				: pass();
		},
	},
	{
		id: "principles-cited-read",
		group: "evidence",
		rule: "A principle named in the worker's text was read (its leaf SKILL.md) earlier in the session.",
		detect:
			"Text naming principle-<x> or its display name; needs Skill principle-<x> or a read of principle-<x>/SKILL.md before it.",
		risks: "FP: a principle mentioned in passing, not cited. FN: a display name rephrased.",
		check: (t, ctx) => {
			const cited = [];
			const read = new Set();
			for (const s of t) {
				const m =
					skillOf(s).match(/^principle-[a-z-]+$/) ??
					(commandOf(s) + pathOf(s)).match(/principle-[a-z-]+(?=\/SKILL\.md)/);
				if (m) read.add(m[0]);
				if (s.kind !== "text") continue;
				for (const [slug, name] of ctx.principles)
					if ((s.text.includes(slug) || s.text.toLowerCase().includes(name.toLowerCase())) && !read.has(slug))
						cited.push(slug);
			}
			if (
				!t.some(
					(s) =>
						s.kind === "text" &&
						ctx.principles.some(
							([slug, name]) => s.text.includes(slug) || s.text.toLowerCase().includes(name.toLowerCase()),
						),
				)
			)
				return na("no principle cited");
			return cited.length ? fail(`cited unread: ${[...new Set(cited)].join(", ")}`) : pass();
		},
	},
	{
		id: "subagent-type",
		group: "delegation",
		rule: "Subagents are spawned as pstack:poteto-agent (or its effort variants).",
		detect:
			"Agent calls in the main thread; subagent_type must match pstack:poteto-agent(-level) or a pstack-routed type (comment-sicko, effort-*).",
		risks: "FP: Explore/general-purpose spawned by a routed skill (how, why) that sets its own type. FN: none known.",
		check: (t) => {
			const agents = t.filter((s) => s.kind === "tool" && s.name === "Agent");
			if (!agents.length) return na("no subagents");
			const off = agents.filter(
				(s) => !/^pstack:(poteto-agent(-\w+)?|comment-sicko|effort-\w+)$/.test(s.input.subagent_type ?? ""),
			);
			return off.length
				? fail(
						`${off.length}/${agents.length} not poteto-agent (${[...new Set(off.map((s) => s.input.subagent_type ?? "none"))].join(", ")})`,
					)
				: pass();
		},
	},
	{
		id: "subagent-model",
		group: "delegation",
		rule: "A subagent's model and effort match some role value in ~/.claude/pstack-models.md.",
		detect:
			"poteto-agent and effort-* calls only (no-comments spawns comment-sicko without a model); model from Agent.model, effort from the -<level> suffix (default effort otherwise); the pair must appear in the sheet.",
		risks:
			"FP: the sheet changed after the session. FN: a valid pair used for the wrong role (role is not knowable from the call).",
		check: (t, ctx) => {
			const agents = t.filter(
				(s) =>
					s.kind === "tool" && s.name === "Agent" && /^pstack:(poteto-agent|effort)/.test(s.input.subagent_type ?? ""),
			);
			if (!agents.length) return na("no pstack subagents");
			const off = agents.filter((s) => {
				const level =
					(s.input.subagent_type.match(/-(low|medium|high|xhigh|max)$/) ?? [])[1] ?? ctx.models.defaultEffort;
				return !ctx.models.pairs.has(`${s.input.model ?? "inherit"} @${level}`);
			});
			return off.length
				? fail(
						`${off.length}/${agents.length} off the sheet (${[...new Set(off.map((s) => `${s.input.model ?? "no model"}/${s.input.subagent_type}`))].join(", ")})`,
					)
				: pass();
		},
	},
	{
		id: "no-self-merge",
		group: "autonomy",
		rule: "The worker never merges, marks ready, approves, or arms auto-merge.",
		detect: "gh pr merge/ready, glab mr merge/approve/--ready, merge_when_pipeline_succeeds in a Bash command.",
		risks:
			"FN: a merge through an API call or MCP tool. FP: the human told it to merge (still a rule break per poteto-mode).",
		check: (t) => {
			const merges = t.filter(isMerge);
			return merges.length ? fail(`${merges.length} merge action(s)`) : pass();
		},
	},
	{
		id: "outward-after-yes",
		group: "autonomy",
		rule: "Every push / PR or MR action / Jira write follows an explicit yes.",
		detect:
			"The latest human message before the action is a yes (EN/TH) or asks for a write on that surface (push, PR/MR, Jira); a brief that asks for a PR authorizes every push and PR action.",
		risks:
			"FP: a yes to something else counts; a brief naming a PR authorizes every later push. FN: a yes worded outside the patterns.",
		check: (t) => {
			const actions = t.filter(isOutward);
			if (!actions.length) return na("no outward action");
			const humans = t.filter((s) => s.kind === "human");
			const briefOk = humans[0] && !isQuestion(humans[0].text) && BRIEF_AUTHORIZES.test(humans[0].text);
			const bare = actions.filter((a) => {
				if (briefOk && !/atlassian__/.test(a.name)) return false;
				const last = humans.filter((h) => h.i < a.i).at(-1);
				return !last || isQuestion(last.text) || !(YES.test(last.text) || askedFor(a, last.text));
			});
			return bare.length ? fail(`${bare.length}/${actions.length} without a yes`) : pass();
		},
	},
	{
		id: "reply-no-em-dash",
		group: "prose",
		rule: "Replies to the human carry no em or en dash.",
		detect: "Turn-final worker texts, code spans and URLs stripped, counted for U+2014/U+2013.",
		risks: "FP: a dash quoted from someone else's text. FN: none.",
		check: (t) => {
			const r = replies(t);
			if (!r.length) return na("no reply");
			const bad = r.filter((s) => emDashes(s.text));
			return bad.length ? fail(`${bad.length}/${r.length} replies`) : pass();
		},
	},
	{
		id: "reply-no-colon-connector",
		group: "prose",
		rule: "Replies use no mid-sentence colon connector (unslop rule 14).",
		detect:
			"A colon after a letter/number/bold and followed by more text on the same line; table rows, code and URLs stripped, line-final colons allowed.",
		risks: "FP: 'Key: value' facts, labels in a list, ratios. FN: a colon reveal split over two lines.",
		check: (t) => {
			const r = replies(t);
			if (!r.length) return na("no reply");
			const bad = r.filter((s) => midSentenceColons(s.text));
			return bad.length ? fail(`${bad.length}/${r.length} replies`) : pass();
		},
	},
	{
		id: "pr-body-no-em-dash",
		group: "prose",
		rule: "PR/MR descriptions carry no em or en dash.",
		detect: "Body fetched read-only with gh/glab, same count as replies.",
		risks: "FP: a dash in a quoted template the team owns. FN: a body edited after the audit.",
		check: (t, ctx) => {
			if (!ctx.prBodies?.length) return na("no PR body");
			const bad = ctx.prBodies.filter((b) => emDashes(b));
			return bad.length ? fail(`${bad.length}/${ctx.prBodies.length} bodies`) : pass();
		},
	},
	{
		id: "pr-body-no-colon-connector",
		group: "prose",
		rule: "PR/MR descriptions use no mid-sentence colon connector.",
		detect: "Same colon pattern as replies.",
		risks: "FP: template fields such as 'Jira: KEY'. FN: as for replies.",
		check: (t, ctx) => {
			if (!ctx.prBodies?.length) return na("no PR body");
			const bad = ctx.prBodies.filter((b) => midSentenceColons(b));
			return bad.length ? fail(`${bad.length}/${ctx.prBodies.length} bodies`) : pass();
		},
	},
];

export function parseModelSheet(text) {
	const defaultEffort = text.match(/^default effort:\s*(\w+)/m)?.[1] ?? "medium";
	const pairs = new Set();
	for (const line of text.split("\n")) {
		const m = line.match(/^[a-z][^:]*:\s*(.+)$/);
		if (!m || /^(default effort|session hook)/.test(line)) continue;
		for (const value of m[1].split(",")) {
			const [model, level] = value.trim().split(/\s*@/);
			pairs.add(`${/^(inherit-parent|auto)$/.test(model) ? "inherit" : model} @${level ?? defaultEffort}`);
		}
	}
	return { defaultEffort, pairs };
}

export function parsePrinciples(skillText) {
	return [...skillText.matchAll(/^- \*\*([^*]+)\*\* \(\*\*(principle-[a-z-]+)\*\*\)/gm)].map((m) => [m[2], m[1]]);
}

export function auditSession(t, ctx) {
	const { kind, source } = taskKind(t, ctx.branch);
	const full = { ...ctx, kind, kindSource: source };
	return {
		kind,
		kindSource: source,
		results: Object.fromEntries(RULES.map((r) => [r.id, r.check(t, full)])),
	};
}
