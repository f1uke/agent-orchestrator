#!/usr/bin/env node
// Runs every case in cases.json against one installed copy of the using-ao
// skill with `claude -p`, and keeps each run's stream-json transcript for
// grade.mjs.
//
//   node run.mjs --skill <dir> --ao <real ao binary> --out <dir> [--label old]
//                [--repeat 2] [--model claude-sonnet-5-5] [--jobs 6] [--only <id,...>]
//
// The agent sees a neutral working dir holding only the skill, and an `ao` on
// PATH that answers `--help` and refuses everything else, so a run can never
// claim a simulator or write to Testiny. AO_* variables are dropped for the
// same reason.
import { spawn } from "node:child_process";
import { mkdirSync, writeFileSync, cpSync, readFileSync, mkdtempSync, chmodSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve, dirname } from "node:path";
import { fileURLToPath } from "node:url";

const here = dirname(fileURLToPath(import.meta.url));
const args = Object.fromEntries(
  process.argv.slice(2).reduce((acc, a, i, all) => (a.startsWith("--") ? [...acc, [a.slice(2), all[i + 1]]] : acc), []),
);
for (const req of ["skill", "ao", "out"]) {
  if (!args[req]) throw new Error(`--${req} is required`);
}
const label = args.label ?? "run";
const repeat = Number(args.repeat ?? 1);
const model = args.model ?? "claude-sonnet-5-5";
const jobs = Number(args.jobs ?? 6);
const { preamble, cases } = JSON.parse(readFileSync(join(here, "cases.json"), "utf8"));
const only = args.only ? new Set(args.only.split(",")) : null;

const shimDir = mkdtempSync(join(tmpdir(), "aoshim-"));
const shim = join(shimDir, "ao");
writeFileSync(
  shim,
  `#!/bin/sh
for a in "$@"; do case "$a" in -h|--help) exec "${resolve(args.ao)}" "$@";; esac; done
[ "$1" = help ] && exec "${resolve(args.ao)}" "$@"
echo "ao: only --help works in this environment" >&2; exit 1
`,
);
chmodSync(shim, 0o755);

const env = Object.fromEntries(Object.entries(process.env).filter(([k]) => !k.startsWith("AO_")));
env.PATH = `${shimDir}:${env.PATH}`;

const tasks = [];
for (const c of cases.filter((c) => !only || only.has(c.id))) {
  for (let r = 1; r <= repeat; r++) tasks.push({ c, r });
}

function runOne({ c, r }) {
  const work = mkdtempSync(join(tmpdir(), "proj-"));
  const skill = join(work, "skills", "using-ao");
  cpSync(resolve(args.skill), skill, { recursive: true });
  const prompt = preamble.replaceAll("{skill}", skill) + c.prompt;
  const outDir = join(resolve(args.out), label, c.id, `r${r}`);
  mkdirSync(outDir, { recursive: true });
  writeFileSync(join(outDir, "meta.json"), JSON.stringify({ case: c.id, label, repeat: r, model, skillDir: skill }, null, 2));
  const started = Date.now();
  return new Promise((done) => {
    const child = spawn(
      "claude",
      [
        "-p", prompt,
        "--model", model,
        "--output-format", "stream-json", "--verbose",
        "--no-session-persistence",
        "--max-turns", "30",
        "--allowedTools", "Read", "Grep", "Glob", "Bash(ao:*)",
      ],
      { cwd: work, env, stdio: ["ignore", "pipe", "pipe"] },
    );
    const chunks = [];
    child.stdout.on("data", (d) => chunks.push(d));
    child.stderr.on("data", (d) => chunks.push(d));
    child.on("close", (code) => {
      writeFileSync(join(outDir, "transcript.jsonl"), Buffer.concat(chunks));
      writeFileSync(join(outDir, "timing.json"), JSON.stringify({ exitCode: code, durationMs: Date.now() - started }));
      console.log(`${label} ${c.id} r${r} exit=${code} ${Math.round((Date.now() - started) / 1000)}s`);
      done();
    });
  });
}

const queue = [...tasks];
await Promise.all(
  Array.from({ length: Math.min(jobs, queue.length) }, async () => {
    while (queue.length) await runOne(queue.shift());
  }),
);
