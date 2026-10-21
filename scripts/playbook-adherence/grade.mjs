#!/usr/bin/env node
// Grades every run under --out from its transcripts and prints the tables.
//
//   node scripts/playbook-adherence/grade.mjs --out <dir> [--json]
//
// pass (strict): within the graded turn the worker read the CORRECT playbook
// file and its todolist holds every step of that playbook, both before its
// first code-exploration call in that turn. The graded turn is turn one, or
// the nudged turn two when turn one missed (arms B, C, C-wrong); there a
// playbook read in turn one counts, and the todolist must come before turn
// two explores code again.
// set-up (loose): the same two facts by the end of the run, in any order.

import { existsSync, readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { TASKS } from "./arms.mjs";
import { analyzeTurn, firstText, readEvents, resultOf, toolCalls } from "./transcript.mjs";

const argv = process.argv.slice(2);
const out = resolve(argv[argv.indexOf("--out") + 1]);
const asJson = argv.includes("--json");

const sorted = (dir) =>
	existsSync(dir) ? readdirSync(dir).sort((a, b) => a.localeCompare(b, "en", { numeric: true })) : [];

function gradeRun(dir) {
	const meta = JSON.parse(readFileSync(join(dir, "meta.json"), "utf8"));
	const correct = TASKS[meta.task].playbook;
	const turns = meta.turns.map((t) => {
		const events = readEvents(join(dir, t.file));
		const calls = toolCalls(events);
		const r = resultOf(events);
		return { ...t, events, calls, result: r, a: analyzeTurn(calls, correct) };
	});
	const all = analyzeTurn(
		turns.flatMap((t) => t.calls),
		correct,
	);
	if (turns[1]?.nudged)
		turns[1].a = analyzeTurn(turns[1].calls, correct, { playbookAlreadyRead: turns[0].a.correctReadAt !== null });
	const graded = turns[1]?.nudged ? turns[1] : turns[0];
	const refusals = existsSync(join(dir, "stop-refusals"))
		? readFileSync(join(dir, "stop-refusals"), "utf8").split("\n").filter(Boolean)
		: [];
	const chosen = [...new Set(all.playbooksRead)];
	return {
		arm: meta.arm,
		task: meta.task,
		rep: meta.rep,
		pass: graded.a.setupBeforeExploration,
		passTurn: graded.a.setupBeforeExploration ? (graded === turns[0] ? 1 : 2) : null,
		setupByEnd: all.setupDone,
		turn1Pass: turns[0].a.setupBeforeExploration,
		nudged: turns.some((t) => t.nudged),
		callsBeforePlaybook: all.playbookReadAt,
		callsBeforeExploration: turns[0].a.firstExplorationAt,
		chosen: chosen.join("+") || "-",
		correct,
		todo: all.todoAt !== null,
		todoTool: all.todoTool ?? "-",
		stepsCovered: `${all.stepsCovered}/${all.stepsTotal}`,
		toolCalls: all.toolCalls,
		agentTurns: turns.reduce((s, t) => s + (t.result?.num_turns ?? 0), 0),
		outputTokens: turns.reduce((s, t) => s + (t.result?.usage?.output_tokens ?? 0), 0),
		costUsd: turns.reduce((s, t) => s + (t.result?.total_cost_usd ?? 0), 0),
		endedWith: turns.map((t) => t.result?.subtype ?? "no-result").join(","),
		refusals: refusals.length,
		stuck: refusals.length >= 3 || turns.some((t) => t.result?.subtype !== "success"),
		repoEdited: turns[0].repoChanges?.length > 0,
		firstWords: firstText(turns[0].events).split("\n")[0].slice(0, 160),
	};
}

const runs = [];
for (const arm of sorted(join(out, "runs")))
	for (const task of sorted(join(out, "runs", arm)))
		for (const rep of sorted(join(out, "runs", arm, task))) {
			const dir = join(out, "runs", arm, task, rep);
			if (existsSync(join(dir, "meta.json"))) runs.push(gradeRun(dir));
		}

if (asJson) {
	process.stdout.write(JSON.stringify(runs, null, 2) + "\n");
	process.exit(0);
}

const n = (xs, f) => xs.filter(f).length;
const median = (xs) => {
	const v = xs.filter((x) => x !== null).sort((a, b) => a - b);
	return v.length ? v[Math.floor((v.length - 1) / 2)] : "-";
};

console.log(
	"| arm | task | runs | pass | turn-1 pass | set-up by end | correct playbook read | todolist | median calls before playbook | repo edited in turn 1 | stuck | mean cost $ |",
);
console.log("|---|---|---|---|---|---|---|---|---|---|---|---|");
const cells = [...new Set(runs.map((r) => `${r.arm}\t${r.task}`))];
for (const cell of cells) {
	const [arm, task] = cell.split("\t");
	const rs = runs.filter((r) => r.arm === arm && r.task === task);
	const cost = rs.reduce((s, r) => s + r.costUsd, 0) / rs.length;
	console.log(
		`| ${arm} | ${task} | ${rs.length} | ${n(rs, (r) => r.pass)} | ${n(rs, (r) => r.turn1Pass)} | ${n(rs, (r) => r.setupByEnd)} | ${n(rs, (r) => r.chosen.split("+").includes(r.correct))} | ${n(rs, (r) => r.todo)} | ${median(rs.map((r) => r.callsBeforePlaybook))} | ${n(rs, (r) => r.repoEdited)} | ${n(rs, (r) => r.stuck)} | ${cost.toFixed(2)} |`,
	);
}

console.log("\n| arm | runs | pass | turn-1 pass | set-up by end |");
console.log("|---|---|---|---|---|");
for (const arm of [...new Set(runs.map((r) => r.arm))]) {
	const rs = runs.filter((r) => r.arm === arm);
	console.log(
		`| ${arm} | ${rs.length} | ${n(rs, (r) => r.pass)} | ${n(rs, (r) => r.turn1Pass)} | ${n(rs, (r) => r.setupByEnd)} |`,
	);
}

console.log("\nPer run:");
console.log(
	"| run | pass | turn | chosen | steps | calls before playbook | calls before exploration | todo tool | agent turns | out tokens | cost $ | refusals | ended | first words |",
);
console.log("|---|---|---|---|---|---|---|---|---|---|---|---|---|---|");
for (const r of runs)
	console.log(
		`| ${r.arm}/${r.task}/${r.rep} | ${r.pass ? "pass" : "FAIL"} | ${r.passTurn ?? "-"} | ${r.chosen} | ${r.stepsCovered} | ${r.callsBeforePlaybook ?? "-"} | ${r.callsBeforeExploration ?? "-"} | ${r.todoTool} | ${r.agentTurns} | ${r.outputTokens} | ${r.costUsd.toFixed(2)} | ${r.refusals} | ${r.endedWith} | ${r.firstWords.replace(/\|/g, "/")} |`,
	);
