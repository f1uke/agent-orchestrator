#!/usr/bin/env node
// Starts throwaway `claude -p` workers on the tally fixture, one per
// (arm, task, rep), and saves each turn's stream-json under --out.
//
//   node scripts/playbook-adherence/run.mjs --out <dir> [--arms base,A] [--tasks bug] [--reps 5] [--jobs 5]
//
// Then grade with grade.mjs --out <dir>.

import { spawn, spawnSync } from "node:child_process";
import { cpSync, existsSync, mkdirSync, mkdtempSync, readFileSync, readdirSync, writeFileSync } from "node:fs";
import { homedir, tmpdir } from "node:os";
import { dirname, join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { randomUUID } from "node:crypto";
import { ARMS, NUDGE, TASKS, initialPromptFor, systemPromptFor } from "./arms.mjs";
import { anySetup, readEvents, toolCalls } from "./transcript.mjs";

const here = dirname(fileURLToPath(import.meta.url));
const repoRoot = resolve(here, "..", "..");

function args() {
	const out = { arms: Object.keys(ARMS), tasks: Object.keys(TASKS), reps: 5, jobs: 5, maxTurns: 80, budget: 5 };
	const argv = process.argv.slice(2);
	for (let i = 0; i < argv.length; i += 2) {
		const [k, v] = [argv[i].replace(/^--/, ""), argv[i + 1]];
		if (k === "arms" || k === "tasks") out[k] = v.split(",");
		else if (k === "out") out.out = resolve(v);
		else out[k] = Number(v);
	}
	if (!out.out) throw new Error("--out <dir> is required");
	return out;
}

// The prompt AO composes for a standard dev worker on a gated project, built
// by the real buildSystemPrompt through a Go test that writes it to a file.
function composedPrompt(out) {
	const file = join(out, "composed-dev-prompt.md");
	const go = spawnSync("go", ["test", "./internal/session_manager", "-run", "TestDumpGatedDevPrompt", "-count=1"], {
		cwd: join(repoRoot, "backend"),
		env: { ...process.env, AO_DUMP_DEV_PROMPT: file },
		encoding: "utf8",
	});
	if (go.status !== 0) throw new Error(`composing the dev prompt failed:\n${go.stdout}${go.stderr}`);
	return readFileSync(file, "utf8");
}

// A clean environment: nothing from this session (AO ids, Claude Code's own
// session vars, an effort override) leaks into the worker, and the task tools
// an interactive worker has are on in -p mode too.
function workerEnv(extra) {
	const env = {};
	for (const [k, v] of Object.entries(process.env)) {
		if (/^(AO_|CLAUDE_CODE_|CLAUDECODE$|CLAUDE_PID$|CLAUDE_EFFORT$)/.test(k)) continue;
		env[k] = v;
	}
	// No `ao` on PATH: a throwaway worker must never reach the real daemon.
	env.PATH = env.PATH.split(":")
		.filter((p) => !existsSync(join(p, "ao")))
		.join(":");
	return { ...env, CLAUDE_CODE_ENABLE_TODO_TOOLS: "1", ...extra };
}

// The --settings file AO launches workers with on this machine, when it uses
// one ($PA_SETTINGS overrides). Every arm gets the same file; D adds its hook.
function baseSettings() {
	const file = process.env.PA_SETTINGS ?? join(homedir(), ".claude", "settings-9router.json");
	return existsSync(file) ? JSON.parse(readFileSync(file, "utf8")) : {};
}

// `ao` built from this checkout, once per --out.
function aoBinary(out) {
	const bin = join(out, "ao");
	if (!existsSync(bin)) {
		const go = spawnSync("go", ["build", "-o", bin, "./cmd/ao"], { cwd: join(repoRoot, "backend"), encoding: "utf8" });
		if (go.status !== 0) throw new Error(`building ao failed:\n${go.stdout}${go.stderr}`);
	}
	return bin;
}

// AO's refusals as Claude Code recorded them in the session .jsonl, one line
// each, in the format stop-hook.mjs writes for grade.mjs.
function aoRefusals(sessionId) {
	const projects = join(process.env.CLAUDE_CONFIG_DIR ?? join(homedir(), ".claude"), "projects");
	for (const dir of readdirSync(projects)) {
		const file = join(projects, dir, `${sessionId}.jsonl`);
		if (!existsSync(file)) continue;
		const n = readEvents(file).filter((e) => {
			const c = e.message?.content;
			return e.isMeta && typeof c === "string" && c.startsWith("Stop hook feedback:\nAO refused this check-in");
		}).length;
		return "refused\n".repeat(n);
	}
	return "";
}

function makeRepo(task) {
	const base = join(tmpdir(), "tally-work");
	mkdirSync(base, { recursive: true });
	const dir = mkdtempSync(join(base, "tally-"));
	cpSync(join(here, "fixture"), dir, { recursive: true });
	const git = (...a) => spawnSync("git", a, { cwd: dir, encoding: "utf8" });
	git("init", "-q", "-b", "main");
	git("add", "-A");
	git("-c", "user.name=dev", "-c", "user.email=dev@example.com", "commit", "-qm", "tally 0.4.2");
	git("checkout", "-qb", task.branch);
	return dir;
}

// Files the gate forbids touching in turn one: anything tracked or new in the repo.
function repoChanges(cwd) {
	return spawnSync("git", ["status", "--porcelain"], { cwd, encoding: "utf8" }).stdout.split("\n").filter(Boolean);
}

function claude(cwd, argv, env, logFile) {
	return new Promise((done) => {
		const child = spawn("claude", argv, { cwd, env, stdio: ["ignore", "pipe", "pipe"] });
		const chunks = [];
		child.stdout.on("data", (c) => chunks.push(c));
		child.stderr.on("data", (c) => chunks.push(c));
		child.on("close", (code) => {
			writeFileSync(logFile, Buffer.concat(chunks));
			done({ code, pid: child.pid });
		});
	});
}

async function runOne(opts, sysFile, arm, taskName, rep) {
	const runDir = join(opts.out, "runs", arm, taskName, String(rep));
	if (existsSync(join(runDir, "meta.json"))) return;
	mkdirSync(runDir, { recursive: true });
	const task = TASKS[taskName];
	const cwd = makeRepo(task);
	const sessionId = randomUUID();
	const extra = {};
	const common = [
		"-p",
		"--output-format",
		"stream-json",
		"--verbose",
		"--append-system-prompt-file",
		sysFile,
		"--max-turns",
		String(opts.maxTurns),
		"--max-budget-usd",
		String(opts.budget),
	];
	const settings = baseSettings();
	if (ARMS[arm].stopHook === "ao") {
		// The build's own hook, as AO runs it for a gated dev. The daemon address
		// points nowhere, so its activity reports fail quietly and never reach a
		// real daemon.
		settings.hooks = { Stop: [{ hooks: [{ type: "command", command: `${aoBinary(opts.out)} hooks claude-code stop` }] }] };
		Object.assign(extra, {
			AO_SESSION_ID: "pa-1",
			AO_SESSION_KIND: "worker",
			AO_CHECKIN_SETUP: "1",
			AO_DATA_DIR: join(runDir, "ao-data"),
			AO_RUN_FILE: join(runDir, "ao-data", "running.json"),
			AO_PORT: "1",
		});
	} else if (ARMS[arm].stopHook) {
		settings.hooks = { Stop: [{ hooks: [{ type: "command", command: `node ${join(here, "stop-hook.mjs")}` }] }] };
		extra.PA_STOP_STATE = join(runDir, "stop-refusals");
	}
	const settingsFile = join(runDir, "settings.json");
	writeFileSync(settingsFile, JSON.stringify(settings));
	common.push("--settings", settingsFile);
	const env = workerEnv(extra);
	const meta = { arm, task: taskName, rep, cwd, sessionId, turns: [] };
	const prompt = initialPromptFor(arm, task);
	writeFileSync(join(runDir, "prompt.txt"), prompt);
	const t1 = await claude(cwd, [...common, "--session-id", sessionId, prompt], env, join(runDir, "turn1.jsonl"));
	meta.turns.push({ file: "turn1.jsonl", exit: t1.code, pid: t1.pid, repoChanges: repoChanges(cwd) });
	if (ARMS[arm].nudge && !anySetup(toolCalls(readEvents(join(runDir, "turn1.jsonl"))))) {
		const t2 = await claude(cwd, [...common, "--resume", sessionId, NUDGE], env, join(runDir, "turn2.jsonl"));
		meta.turns.push({ file: "turn2.jsonl", exit: t2.code, pid: t2.pid, nudged: true, repoChanges: repoChanges(cwd) });
	}
	if (ARMS[arm].stopHook === "ao") writeFileSync(join(runDir, "stop-refusals"), aoRefusals(sessionId));
	writeFileSync(join(runDir, "meta.json"), JSON.stringify(meta, null, 2));
	process.stdout.write(`done ${arm}/${taskName}/${rep}\n`);
}

async function main() {
	const opts = args();
	mkdirSync(opts.out, { recursive: true });
	const sysByArm = {};
	const composed = composedPrompt(opts.out);
	for (const arm of opts.arms) {
		sysByArm[arm] = join(opts.out, `system-prompt-${arm}.md`);
		writeFileSync(sysByArm[arm], systemPromptFor(arm, composed));
	}
	const queue = [];
	for (let rep = 1; rep <= opts.reps; rep++)
		for (const task of opts.tasks) for (const arm of opts.arms) queue.push([arm, task, rep]);
	const workers = Array.from({ length: opts.jobs }, async () => {
		while (queue.length) {
			const [arm, task, rep] = queue.shift();
			await runOne(opts, sysByArm[arm], arm, task, rep);
		}
	});
	await Promise.all(workers);
}

await main();
