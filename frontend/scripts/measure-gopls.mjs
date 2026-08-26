// Drives a real gopls against a real Go module and reports what it costs.
//
// 🗝 The measurement METHOD is the point. The editor spike first published
// 493 MB where the real number was ~2 450 MB, because its harness SIGKILLed the
// server the instant the last request returned: on a 1.3 s run that is two or
// three samples, and the "peak" was RSS at the moment of the kill, while it was
// still climbing. It reported the shape of its own teardown.
//
// So this waits for RSS to STOP MOVING - five consecutive samples within ±2%,
// capped at 90 s - before calling anything a peak, and says in the output
// whether it actually settled.
//
// 🗝 A second trap, found while building this: gopls RELEASES nearly everything
// when no document is open. Measured on this repo, with no didOpen, RSS went
// 1 407 MB → 23 MB within a minute. A harness that only calls workspace/symbol
// therefore reports a number far BELOW what the app actually costs, which is the
// same class of error as measuring your own teardown. So this opens a real file
// first and keeps it open, which is what an editor pane does.
//
//   node scripts/measure-gopls.mjs <module-root> <open-file> [<symbol-query>]
//   GOMEMLIMIT=off node scripts/measure-gopls.mjs ../backend internal/x.go   # unbounded control
import { execFile, spawn } from "node:child_process";
import { readFileSync } from "node:fs";
import path from "node:path";
import { pathToFileURL } from "node:url";

const ROOT = path.resolve(process.argv[2] ?? ".");
const OPEN_FILE = process.argv[3];
const QUERY = process.argv[4] ?? "Confined";
if (!OPEN_FILE) {
	console.error("usage: measure-gopls.mjs <module-root> <workspace-relative .go file> [symbol-query]");
	process.exit(2);
}
// Every step says where it got to, so a hang is visible instead of being a
// silent five-minute wait.
const step = (msg) => process.stderr.write(`[measure] ${((Date.now() - startedAt) / 1000).toFixed(1)}s ${msg}\n`);
const CACHE = path.join(process.env.HOME, ".ao", "measure-gopls-cache");
const LIMIT = process.env.GOMEMLIMIT ?? "1GiB";

const SETTLE_SAMPLES = 5;
const SETTLE_TOLERANCE = 0.02;
const SETTLE_CAP_MS = 90_000;
const SAMPLE_EVERY_MS = 500;

const startedAt = Date.now();

const rss = (pid) =>
	new Promise((resolve) =>
		execFile("ps", ["-o", "rss=", "-p", String(pid)], (err, out) => {
			const kb = Number(String(out ?? "").trim());
			resolve(err || !Number.isFinite(kb) || kb <= 0 ? null : Math.round(kb / 1024));
		}),
	);

const env = { ...process.env, GOPLSCACHE: CACHE };
// "off" means: do not set it at all, which is gopls's own default.
if (LIMIT === "off") delete env.GOMEMLIMIT;
else env.GOMEMLIMIT = LIMIT;

const child = spawn("gopls", ["-mode=stdio"], { cwd: ROOT, env, stdio: ["pipe", "pipe", "pipe"] });

let buffer = Buffer.alloc(0);
const pending = new Map();
let nextId = 1;
const send = (msg) => {
	const body = Buffer.from(JSON.stringify(msg), "utf8");
	child.stdin.write(`Content-Length: ${body.length}\r\n\r\n`);
	child.stdin.write(body);
};
const request = (method, params) =>
	new Promise((resolve) => {
		const id = nextId++;
		const sent = Date.now();
		pending.set(id, (result) => resolve({ result, ms: Date.now() - sent }));
		send({ jsonrpc: "2.0", id, method, params });
	});

// Work-done progress: the same signal the app's supervisor gates readiness on.
const progress = new Set();
let indexSettledAt = null;

child.stdout.on("data", (chunk) => {
	buffer = Buffer.concat([buffer, chunk]);
	for (;;) {
		const sep = buffer.indexOf("\r\n\r\n");
		if (sep < 0) return;
		const len = Number(/content-length:\s*(\d+)/i.exec(buffer.subarray(0, sep).toString("ascii"))?.[1] ?? -1);
		if (len < 0 || buffer.length < sep + 4 + len) return;
		const msg = JSON.parse(buffer.subarray(sep + 4, sep + 4 + len).toString("utf8"));
		buffer = buffer.subarray(sep + 4 + len);
		// A RESPONSE has an id and NO method. Server→client requests draw ids from
		// the SERVER's id space, which overlaps ours: matching on id alone resolved
		// our workspace/symbol with gopls's own progress-create request and left
		// gopls waiting forever, so the workspace never loaded.
		if (msg.id !== undefined && msg.method === undefined && pending.has(msg.id)) {
			pending.get(msg.id)(msg.result);
			pending.delete(msg.id);
		} else if (msg.method === "$/progress") {
			const kind = msg.params?.value?.kind;
			if (kind === "begin") progress.add(String(msg.params.token));
			if (kind === "end") {
				progress.delete(String(msg.params.token));
				if (progress.size === 0) indexSettledAt ??= Date.now();
			}
		} else if (msg.id !== undefined && msg.method) {
			send({ jsonrpc: "2.0", id: msg.id, result: msg.method === "workspace/configuration" ? [{}] : null });
		}
	}
});
child.stderr.on("data", (d) => process.stderr.write(`[gopls] ${d}`));

const rootUri = pathToFileURL(ROOT).href;
const init = await request("initialize", {
	processId: process.pid,
	rootUri,
	workspaceFolders: [{ uri: rootUri, name: "measure" }],
	capabilities: {
		workspace: { workspaceFolders: true, configuration: true, symbol: {} },
		textDocument: { definition: { linkSupport: true }, synchronization: {} },
		window: { workDoneProgress: true },
	},
});
const initializeMs = Date.now() - startedAt;
step(`initialize returned (${initializeMs}ms)`);
send({ jsonrpc: "2.0", method: "initialized", params: {} });

// The editor pane's own behaviour, and the thing that makes this measurement
// representative: gopls drops almost all of its state when nothing is open.
const openAbs = path.resolve(ROOT, OPEN_FILE);
send({
	jsonrpc: "2.0",
	method: "textDocument/didOpen",
	params: {
		textDocument: {
			uri: pathToFileURL(openAbs).href,
			languageId: "go",
			version: 1,
			text: readFileSync(openAbs, "utf8"),
		},
	},
});
step(`didOpen ${OPEN_FILE}`);

// Deliberately asked BEFORE the index has settled, to record whether the
// readiness gate this slice ships is actually needed on Go.
const symbolBeforeReady = await request("workspace/symbol", { query: QUERY });
step(`symbol before ready → ${Array.isArray(symbolBeforeReady.result) ? symbolBeforeReady.result.length : 0} hits`);

// Now wait for readiness the way the app does.
const readyDeadline = Date.now() + 60_000;
while (Date.now() < readyDeadline && indexSettledAt === null) await new Promise((r) => setTimeout(r, 100));
const readyMs = indexSettledAt === null ? null : indexSettledAt - startedAt;
step(`ready at ${readyMs ?? "never"}`);

const symbolCold = await request("workspace/symbol", { query: QUERY });
const symbolWarm = await request("workspace/symbol", { query: QUERY });
step(`symbol cold ${symbolCold.ms}ms / warm ${symbolWarm.ms}ms`);

const samples = [];
const settleStart = Date.now();
let settled = false;
while (Date.now() - settleStart < SETTLE_CAP_MS) {
	const mb = await rss(child.pid);
	if (mb !== null) samples.push(mb);
	const tail = samples.slice(-SETTLE_SAMPLES);
	if (tail.length === SETTLE_SAMPLES && Math.max(...tail) - Math.min(...tail) <= Math.max(...tail) * SETTLE_TOLERANCE) {
		settled = true;
		break;
	}
	await new Promise((r) => setTimeout(r, SAMPLE_EVERY_MS));
}
step(`settled=${settled} peak=${samples.length ? Math.max(...samples) : "?"}MB after ${samples.length} samples`);

// 🗝 The number the LIFECYCLE turns on: what gopls gives back when the last
// document closes. If it releases on its own, the idle stop is about process
// count and cold-start latency rather than about memory, and that changes what
// the cap is actually buying.
send({
	jsonrpc: "2.0",
	method: "textDocument/didClose",
	params: { textDocument: { uri: pathToFileURL(openAbs).href } },
});
step("didClose - sampling what is given back");
const afterClose = [];
const closeStart = Date.now();
while (Date.now() - closeStart < 60_000) {
	const mb = await rss(child.pid);
	if (mb !== null) afterClose.push(mb);
	const tail = afterClose.slice(-SETTLE_SAMPLES);
	if (tail.length === SETTLE_SAMPLES && Math.max(...tail) - Math.min(...tail) <= Math.max(...tail) * SETTLE_TOLERANCE)
		break;
	await new Promise((r) => setTimeout(r, 1000));
}
step(`after close: ${afterClose.at(0)}MB → ${afterClose.at(-1)}MB`);

const count = (r) => (Array.isArray(r) ? r.length : 0);
console.log(
	JSON.stringify(
		{
			root: ROOT,
			query: QUERY,
			gomemlimit: LIMIT,
			cache: CACHE,
			serverInfo: init.result?.serverInfo,
			initializeMs,
			readyMs,
			symbolBeforeReadyHits: count(symbolBeforeReady.result),
			symbolBeforeReadyMs: symbolBeforeReady.ms,
			symbolColdHits: count(symbolCold.result),
			symbolColdMs: symbolCold.ms,
			symbolWarmHits: count(symbolWarm.result),
			symbolWarmMs: symbolWarm.ms,
			openFile: OPEN_FILE,
			settled,
			peakRssMb: samples.length ? Math.max(...samples) : null,
			restingRssMb: samples.at(-1) ?? null,
			rssCurve: samples,
			afterCloseRssMb: afterClose.at(-1) ?? null,
			afterCloseCurve: afterClose,
		},
		null,
		2,
	),
);

send({ jsonrpc: "2.0", id: nextId++, method: "shutdown", params: null });
send({ jsonrpc: "2.0", method: "exit" });
setTimeout(() => {
	child.kill("SIGKILL");
	process.exit(0);
}, 3000);
