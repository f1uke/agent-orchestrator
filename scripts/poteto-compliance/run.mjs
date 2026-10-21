#!/usr/bin/env node
import { execFileSync } from "node:child_process";
import { existsSync, readdirSync, readFileSync, statSync, writeFileSync } from "node:fs";
import { homedir } from "node:os";
import { dirname, join } from "node:path";
import { playbookSteps, pstackPlaybooksDir, readEvents } from "../playbook-adherence/transcript.mjs";
import { auditSession, parseModelSheet, parsePrinciples, RULES, timeline } from "./audit.mjs";

const argv = process.argv.slice(2);
const flag = (name, fallback) => (argv.includes(name) ? argv[argv.indexOf(name) + 1] : fallback);
const limit = Number(flag("--limit", 25));

const sh = (cmd, args, opts = {}) => {
	try {
		return execFileSync(cmd, args, { encoding: "utf8", stdio: ["ignore", "pipe", "ignore"], ...opts });
	} catch {
		return null;
	}
};
const aoJson = (...args) => JSON.parse(sh("ao", [...args, "--json"]) ?? "null");

const config = process.env.CLAUDE_CONFIG_DIR ?? join(homedir(), ".claude");
const transcriptsFor = (workspace) => {
	const dir = join(config, "projects", workspace.replace(/[/.]/g, "-"));
	if (!existsSync(dir)) return [];
	return readdirSync(dir)
		.filter((f) => f.endsWith(".jsonl"))
		.map((f) => join(dir, f))
		.sort((a, b) => statSync(a).mtimeMs - statSync(b).mtimeMs);
};

const projectPaths = Object.fromEntries((aoJson("project", "ls")?.projects ?? []).map((p) => [p.id, p.path]));

function branchCommits(session) {
	const repo = existsSync(session.workspacePath ?? "") ? session.workspacePath : projectPaths[session.projectId];
	if (!repo || !session.branch) return null;
	const base = session.prTarget ?? session.baseBranch;
	const range = [
		`origin/${base}..${session.branch}`,
		`${base}..${session.branch}`,
		`origin/${base}..origin/${session.branch}`,
	];
	for (const r of range) {
		const log = sh("git", ["-C", repo, "log", "--reverse", "--format=@@%H", "--name-only", r]);
		if (log === null) continue;
		const commits = log
			.split("@@")
			.filter(Boolean)
			.map((chunk) => ({ files: chunk.split("\n").slice(1).filter(Boolean) }));
		if (commits.length) return commits;
	}
	return prCommits(session.prs?.[0]?.url ?? "");
}

// A squash-merged branch is gone locally; the forge still lists its commits.
function prCommits(url) {
	const api = (path) => JSON.parse(sh("gh", ["api", path]) ?? "null");
	const gh = url.match(/github\.com\/([^/]+\/[^/]+)\/pull\/(\d+)/);
	if (gh) {
		const shas = (api(`repos/${gh[1]}/pulls/${gh[2]}/commits`) ?? []).map((c) => c.sha);
		return shas.length
			? shas.map((sha) => ({ files: (api(`repos/${gh[1]}/commits/${sha}`)?.files ?? []).map((f) => f.filename) }))
			: null;
	}
	const gl = url.match(/^https:\/\/([^/]+)\/(.+)\/-\/merge_requests\/(\d+)/);
	if (gl) {
		const glApi = (path) =>
			JSON.parse(sh("glab", ["api", "--hostname", gl[1], `projects/${encodeURIComponent(gl[2])}/${path}`]) ?? "null");
		const shas = (glApi(`merge_requests/${gl[3]}/commits`) ?? []).map((c) => c.id).reverse();
		return shas.length
			? shas.map((sha) => ({ files: (glApi(`repository/commits/${sha}/diff`) ?? []).map((d) => d.new_path) }))
			: null;
	}
	return null;
}

function prBody(url) {
	const gl = url.match(/^https:\/\/([^/]+)\/(.+)\/-\/merge_requests\/(\d+)/);
	if (gl) {
		const out = sh("glab", [
			"api",
			"--hostname",
			gl[1],
			`projects/${encodeURIComponent(gl[2])}/merge_requests/${gl[3]}`,
		]);
		return out ? (JSON.parse(out).description ?? "") : null;
	}
	if (/github\.com/.test(url)) return sh("gh", ["pr", "view", url, "--json", "body", "-q", ".body"]);
	return null;
}

const sheetPath = join(config, "pstack-models.md");
const models = parseModelSheet(existsSync(sheetPath) ? readFileSync(sheetPath, "utf8") : "");
const principles = parsePrinciples(readFileSync(join(dirname(pstackPlaybooksDir()), "SKILL.md"), "utf8"));

const listed = aoJson("session", "ls", "--include-terminated")?.data ?? [];
const wanted = flag("--sessions", "").split(",").filter(Boolean);
const candidates = (
	wanted.length
		? listed.filter((s) => wanted.includes(s.id))
		: listed.filter((s) => s.role === "worker" && s.harness === "claude-code")
).sort((a, b) => b.createdAt.localeCompare(a.createdAt));

const rows = [];
for (const brief of candidates) {
	if (rows.length >= limit) break;
	const session = aoJson("session", "get", brief.id)?.session;
	if (!session || session.crew?.role === "qa" || session.id === process.env.AO_SESSION_ID) continue;
	const files = transcriptsFor(session.workspacePath ?? "");
	if (!files.length) continue;
	const t = timeline(files.flatMap(readEvents));
	// The SessionStart hook names poteto-mode in every session; only a run that
	// loaded the skill is held to it.
	const loaded = (f) =>
		/<command-name>\/pstack:poteto-mode<|"skill":"pstack:poteto-mode"/.test(readFileSync(f, "utf8"));
	if (!files.some(loaded)) continue;
	const prs = (session.prs ?? []).map((p) => p.url);
	const prBodies = prs.map(prBody).filter((b) => b !== null);
	const audit = auditSession(t, {
		branch: session.branch ?? "",
		commits: branchCommits(session),
		prBodies,
		steps: playbookSteps,
		models,
		principles,
	});
	rows.push({
		id: session.id,
		project: session.projectId,
		role: session.crew?.role ?? "solo",
		taskSize: session.taskSize,
		createdAt: session.createdAt,
		prs: prs.length,
		steps: t.length,
		...audit,
	});
	process.stderr.write(`${session.id} ${audit.kind ?? "-"}\n`);
}

const mark = { pass: "pass", fail: "FAIL", "n/a": "-" };
const lines = [];
lines.push("| rule | group | pass | fail | n/a | compliance | failing sessions |", "|---|---|---|---|---|---|---|");
for (const r of RULES) {
	const vs = rows.map((row) => row.results[r.id].verdict);
	const p = vs.filter((v) => v === "pass").length;
	const f = vs.filter((v) => v === "fail").length;
	const failing = rows.filter((row) => row.results[r.id].verdict === "fail").map((row) => row.id);
	lines.push(
		`| ${r.id} | ${r.group} | ${p} | ${f} | ${vs.length - p - f} | ${p + f ? `${Math.round((100 * p) / (p + f))}%` : "-"} | ${failing.join(", ")} |`,
	);
}
lines.push(
	"",
	`| session | kind | ${RULES.map((r) => r.id).join(" | ")} |`,
	`|---|---|${RULES.map(() => "---").join("|")}|`,
);
for (const row of rows)
	lines.push(`| ${row.id} | ${row.kind ?? "-"} | ${RULES.map((r) => mark[row.results[r.id].verdict]).join(" | ")} |`);

const md = lines.join("\n") + "\n";
if (flag("--json")) writeFileSync(flag("--json"), JSON.stringify(rows, null, 2) + "\n");
if (flag("--md")) writeFileSync(flag("--md"), md);
process.stdout.write(md);
