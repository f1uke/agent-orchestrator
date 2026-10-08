#!/usr/bin/env node
// Grades the runs run.mjs left under <out>: each assertion in cases.json is a
// case-insensitive regex over the agent's final answer. Also counts what the
// agent READ to get there: characters of skill files returned by Read, and of
// `ao ... --help` output returned by Bash (tokens ~= chars / 4).
//
//   node grade.mjs <out> [label ...]
//
// Writes <out>/<label>/<case>/r<n>/grading.json and prints a markdown summary.
import { readFileSync, readdirSync, writeFileSync, existsSync } from "node:fs";
import { join, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const [out, ...wanted] = process.argv.slice(2);
if (!out) throw new Error("usage: grade.mjs <out> [label ...]");
const { cases } = JSON.parse(readFileSync(join(here, "cases.json"), "utf8"));
const labels = wanted.length ? wanted : readdirSync(out).filter((l) => existsSync(join(out, l)) && !l.includes("."));

function resultText(content) {
  if (typeof content === "string") return content;
  return (content ?? []).map((p) => (p.type === "text" ? p.text : "")).join("");
}

function readTranscript(file) {
  const tools = new Map();
  let skillChars = 0, helpChars = 0, answer = "", usage = null, cost = 0;
  const filesRead = [];
  for (const line of readFileSync(file, "utf8").split("\n")) {
    if (!line.startsWith("{")) continue;
    let ev;
    try { ev = JSON.parse(line); } catch { continue; }
    if (ev.type === "assistant") {
      for (const p of ev.message?.content ?? []) if (p.type === "tool_use") tools.set(p.id, p);
    } else if (ev.type === "user") {
      for (const p of ev.message?.content ?? []) {
        if (p.type !== "tool_result") continue;
        const use = tools.get(p.tool_use_id);
        const n = resultText(p.content).length;
        if (use?.name === "Read" && /\/skills\/using-ao\//.test(use.input.file_path ?? "")) {
          skillChars += n;
          filesRead.push(use.input.file_path.replace(/^.*\/skills\/using-ao\//, ""));
        } else if (use?.name === "Bash" && /(^|[\s;&|(])ao\b|\/skills\//.test(use.input.command ?? "")) {
          helpChars += n;
          filesRead.push(`$ ${use.input.command}`);
        } else if (use && ["Grep", "Glob"].includes(use.name)) {
          skillChars += n;
        }
      }
    } else if (ev.type === "result") {
      answer = ev.result ?? "";
      usage = ev.usage;
      cost = ev.total_cost_usd ?? 0;
    }
  }
  return { skillChars, helpChars, answer, usage, cost, filesRead };
}

const rows = [];
for (const label of labels) {
  for (const c of cases) {
    const caseDir = join(out, label, c.id);
    if (!existsSync(caseDir)) continue;
    for (const r of readdirSync(caseDir)) {
      const t = readTranscript(join(caseDir, r, "transcript.jsonl"));
      const expectations = c.assertions.map((a) => ({
        text: a.text,
        passed: new RegExp(a.pattern, "i").test(t.answer),
        evidence: a.pattern,
      }));
      const passed = expectations.filter((e) => e.passed).length;
      writeFileSync(
        join(caseDir, r, "grading.json"),
        JSON.stringify({ expectations, summary: { passed, total: expectations.length }, read: t.filesRead, skillChars: t.skillChars, helpChars: t.helpChars, answer: t.answer }, null, 2),
      );
      rows.push({ label, id: c.id, area: c.area, r, passed, total: expectations.length, readChars: t.skillChars + t.helpChars, skillChars: t.skillChars, helpChars: t.helpChars, cost: t.cost, failed: expectations.filter((e) => !e.passed).map((e) => e.text) });
    }
  }
}

const tok = (chars) => Math.round(chars / 4);
const mean = (xs) => (xs.length ? xs.reduce((a, b) => a + b, 0) / xs.length : 0);
console.log("| case | " + labels.map((l) => `${l} pass | ${l} tokens read`).join(" | ") + " |");
console.log("|---|" + labels.map(() => "---|---").join("|") + "|");
for (const c of cases) {
  const cells = labels.map((l) => {
    const rs = rows.filter((x) => x.label === l && x.id === c.id);
    if (!rs.length) return "- | -";
    const p = rs.reduce((a, x) => a + x.passed, 0), t = rs.reduce((a, x) => a + x.total, 0);
    return `${p}/${t} | ${tok(mean(rs.map((x) => x.readChars)))}`;
  });
  console.log(`| ${c.id} | ${cells.join(" | ")} |`);
}
const totals = labels.map((l) => {
  const rs = rows.filter((x) => x.label === l);
  const p = rs.reduce((a, x) => a + x.passed, 0), t = rs.reduce((a, x) => a + x.total, 0);
  return `**${p}/${t} (${t ? Math.round((100 * p) / t) : 0}%)** | **${tok(mean(rs.map((x) => x.readChars)))}**`;
});
console.log(`| all (mean tokens per run) | ${totals.join(" | ")} |`);
for (const l of labels) {
  const rs = rows.filter((x) => x.label === l);
  console.log(`\n${l}: ${rs.length} runs, $${rs.reduce((a, x) => a + x.cost, 0).toFixed(2)}`);
  for (const x of rs.filter((x) => x.failed.length)) console.log(`  ${x.id} ${x.r}: missed ${x.failed.join("; ")}`);
}
