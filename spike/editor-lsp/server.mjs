// THROWAWAY SPIKE — stands in for what the Electron MAIN process would do.
// The renderer is sandboxed (contextIsolation, no node), so it can never spawn
// a language server itself. Here that hop is a loopback WebSocket, which is
// already allowed by the app's CSP (`connect-src ws://127.0.0.1:*`).
import { spawn, execFile } from "node:child_process";
import { readFileSync, existsSync, readdirSync, writeFileSync } from "node:fs";
import { createServer } from "node:http";
import path from "node:path";
import { WebSocketServer } from "ws";
import { pathToFileURL } from "node:url";

const PORT = Number(process.env.PORT ?? 8917);
const ROOT = process.env.LSP_ROOT ?? process.cwd();
const LANG = process.env.LSP_LANG ?? "go";

// AO hard rule: every cache a language server writes must land under ~/.ao.
const AO = path.join(process.env.HOME, ".ao", "spike-lsp", "cache");
const SERVERS = {
	go: {
		cmd: "gopls",
		args: ["-mode=stdio"],
		env: { GOPLSCACHE: path.join(AO, "gopls-app") },
		exts: [".go"],
		languageId: () => "go",
	},
	swift: {
		cmd: "/usr/bin/sourcekit-lsp",
		args: ["--scratch-path", path.join(AO, "sk-scratch"), "--generated-files-path", path.join(AO, "sk-generated")],
		env: {},
		exts: [".swift"],
		languageId: () => "swift",
	},
};

const cfg = SERVERS[LANG];
if (!cfg) {
	console.error("unknown LSP_LANG", LANG);
	process.exit(1);
}

// ---- file index: the "file" half of Cmd+Shift+O ----
// A checkout without git metadata still needs an index; the daemon's Files tab
// already carries this same fallback (workspace_file.go walkWorkspaceFiles).
const SKIP = new Set(["node_modules", ".build", "DerivedData", ".xbs-filelists", "Pods.build"]);
function walk(dir, rel, out) {
	let entries;
	try {
		entries = readdirSync(dir, { withFileTypes: true });
	} catch {
		return out;
	}
	for (const e of entries) {
		if (e.name.startsWith(".") || SKIP.has(e.name)) continue;
		const r = rel ? rel + "/" + e.name : e.name;
		if (e.isDirectory()) walk(path.join(dir, e.name), r, out);
		else out.push(r);
	}
	return out;
}
let fileIndex = [];
let fileIndexMs = 0;
function buildFileIndex() {
	const t0 = Date.now();
	return new Promise((resolve) => {
		execFile(
			"git",
			["ls-files", "-co", "--exclude-standard", "-z"],
			{ cwd: ROOT, maxBuffer: 256 * 1024 * 1024 },
			(err, stdout) => {
				fileIndex = err || !String(stdout || "").trim() ? walk(ROOT, "", []) : stdout.split("\0").filter(Boolean);
				fileIndexMs = Date.now() - t0;
				console.log(`[index] ${fileIndex.length} files in ${fileIndexMs}ms`);
				resolve();
			},
		);
	});
}

// ---- language server child process ----
let lsp = null;
let lspStartedAt = 0;
function startServer() {
	lspStartedAt = Date.now();
	lsp = spawn(cfg.cmd, cfg.args, { cwd: ROOT, env: { ...process.env, ...cfg.env }, stdio: ["pipe", "pipe", "pipe"] });
	lsp.stderr.on("data", (d) => process.stderr.write(`[lsp] ${d}`));
	lsp.on("exit", (c) => {
		// Found by qa: without clearing the handle, `if (!lsp) startServer()` can
		// never fire again. The renderer reconnects, sends `initialize` into a dead
		// pipe, and hangs there forever with no error and no timeout.
		console.log(`[lsp] exited ${c}`);
		lsp = null;
	});
	console.log(`[lsp] spawned ${cfg.cmd} pid=${lsp.pid} root=${ROOT}`);
}

// ---- uncommitted changes -------------------------------------------------
// The daemon already computes this for the Files tab
// (workspace_file.go ReadWorkspaceFile -> changedLines), so a real feature
// reuses that. Here it is recomputed so the spike stands alone.
function git(args) {
	return new Promise((resolve) => {
		execFile("git", args, { cwd: ROOT, maxBuffer: 64 * 1024 * 1024 }, (err, stdout) => resolve(err ? null : stdout));
	});
}

// `git diff -U0` gives one hunk per contiguous change. Keeping the OLD side of
// each hunk is what makes revert possible without a second git call per click.
function parseHunks(diff) {
	const hunks = [];
	if (!diff) return hunks;
	let cur = null;
	for (const line of diff.split("\n")) {
		const m = /^@@ -(\d+)(?:,(\d+))? \+(\d+)(?:,(\d+))? @@/.exec(line);
		if (m) {
			cur = {
				oldStart: Number(m[1]),
				oldLines: m[2] === undefined ? 1 : Number(m[2]),
				newStart: Number(m[3]),
				newLines: m[4] === undefined ? 1 : Number(m[4]),
				oldText: [],
			};
			hunks.push(cur);
			continue;
		}
		if (!cur) continue;
		if (line.startsWith("-")) cur.oldText.push(line.slice(1));
		// "+" lines are already in the working tree; "\ No newline" is ignored.
	}
	return hunks.map((h) => ({
		...h,
		// added: nothing on the old side. removed: nothing on the new side.
		// otherwise the lines were rewritten.
		kind: h.oldLines === 0 ? "added" : h.newLines === 0 ? "removed" : "modified",
	}));
}

// The target branch a session is FOR. The daemon derives this from the PR
// target (workspace_changes.go); here it is an env knob with the same fallbacks.
const BASE_CANDIDATES = [process.env.LSP_BASE, "origin/main-fluke", "main-fluke", "origin/main", "main"].filter(
	Boolean,
);
let baseRef = null;
let mergeBase = null;

async function resolveBase() {
	for (const cand of BASE_CANDIDATES) {
		if ((await git(["rev-parse", "--verify", "--quiet", cand])) === null) continue;
		const mb = await git(["merge-base", cand, "HEAD"]);
		if (mb === null) continue;
		baseRef = cand;
		mergeBase = mb.trim();
		return;
	}
}

// Everything this branch has done, committed or not. Diffing the merge base
// against the WORKING TREE (no second ref) is what puts both in one list --
// the same call the Changes view already makes (workspace_changes.go:208).
async function branchChanges() {
	if (!mergeBase) await resolveBase();
	if (!mergeBase) return { available: false, files: [] };
	const numstat = (await git(["diff", "--numstat", "-M", mergeBase])) ?? "";
	const files = numstat
		.split("\n")
		.filter(Boolean)
		.map((l) => {
			const [add, del, ...rest] = l.split("\t");
			return { path: rest.join("\t"), additions: Number(add) || 0, deletions: Number(del) || 0, status: "modified" };
		});
	const nameStatus = (await git(["diff", "--name-status", "-M", mergeBase])) ?? "";
	for (const l of nameStatus.split("\n").filter(Boolean)) {
		const [code, ...rest] = l.split("\t");
		const p = rest[rest.length - 1];
		const f = files.find((x) => x.path === p);
		if (f)
			f.status = code.startsWith("A")
				? "added"
				: code.startsWith("D")
					? "removed"
					: code.startsWith("R")
						? "renamed"
						: "modified";
	}
	// git diff never reports untracked files, and a file the agent just created
	// is exactly what a reviewer is looking for.
	const st = (await git(["status", "--porcelain=v1"])) ?? "";
	for (const l of st.split("\n").filter(Boolean)) {
		if (!l.startsWith("??")) continue;
		const p = l.slice(3).trim();
		if (p.endsWith("/") || files.some((x) => x.path === p)) continue;
		let n = 0;
		try {
			n = readFileSync(path.resolve(ROOT, p), "utf8").split("\n").length;
		} catch {
			continue;
		}
		files.push({ path: p, additions: n, deletions: 0, status: "added", untracked: true });
	}
	files.sort((a, b) => a.path.localeCompare(b.path));
	return { available: true, baseRef, mergeBase, files };
}

async function changesFor(rel) {
	const tracked = await git(["ls-files", "--error-unmatch", "--", rel]);
	if (tracked === null) {
		// untracked: the whole file is new
		const n = readFileSync(path.resolve(ROOT, rel), "utf8").split("\n").length;
		return {
			tracked: false,
			hunks: [{ oldStart: 0, oldLines: 0, newStart: 1, newLines: n, oldText: [], kind: "added" }],
		};
	}
	const diff = await git(["diff", "--no-color", "--no-ext-diff", "-U0", "--", rel]);
	// Two levels, deliberately kept apart:
	//   uncommitted  -- working tree vs HEAD. This is what Discard Change reverts.
	//   branch       -- merge base vs working tree. Everything this branch did.
	// The second is a superset of the first, so they are never merged into one
	// list; the gutter gives each its own lane.
	if (!mergeBase) await resolveBase();
	const branchDiff = mergeBase ? await git(["diff", "--no-color", "--no-ext-diff", "-U0", mergeBase, "--", rel]) : null;
	return { tracked: true, hunks: parseHunks(diff), branchHunks: parseHunks(branchDiff), baseRef };
}

const http = createServer((req, res) => {
	const url = new URL(req.url, "http://localhost");
	// The renderer is a different origin from this loopback bridge (in the real
	// app, `app://` vs `http://127.0.0.1`), so anything past a simple GET is
	// preflighted. A POST with a JSON content-type is not simple — without this
	// the write silently fails as "TypeError: Failed to fetch".
	res.setHeader("access-control-allow-origin", "*");
	res.setHeader("access-control-allow-headers", "content-type");
	res.setHeader("access-control-allow-methods", "GET, POST, OPTIONS");
	if (req.method === "OPTIONS") {
		res.writeHead(204);
		return res.end();
	}
	const json = (o) => {
		res.writeHead(200, { "content-type": "application/json", "access-control-allow-origin": "*" });
		res.end(JSON.stringify(o));
	};
	if (url.pathname === "/files") return json({ root: ROOT, lang: LANG, files: fileIndex, indexMs: fileIndexMs });
	if (url.pathname === "/file") {
		const rel = url.searchParams.get("path") ?? "";
		const abs = path.resolve(ROOT, rel);
		if (!abs.startsWith(path.resolve(ROOT) + path.sep) && abs !== path.resolve(ROOT)) {
			res.writeHead(403);
			return res.end("outside root");
		}
		if (!existsSync(abs)) {
			res.writeHead(404);
			return res.end("no such file");
		}
		return json({ path: rel, abs, uri: pathToFileURL(abs).href, text: readFileSync(abs, "utf8") });
	}
	if (url.pathname === "/open-external") {
		// a definition landing outside the workspace (SDK headers, Pods in another tree)
		const abs = url.searchParams.get("abs") ?? "";
		if (!existsSync(abs)) {
			res.writeHead(404);
			return res.end("no such file");
		}
		return json({ abs, uri: pathToFileURL(abs).href, text: readFileSync(abs, "utf8") });
	}
	if (url.pathname === "/changes") {
		const rel = url.searchParams.get("path") ?? "";
		const abs = path.resolve(ROOT, rel);
		if (!abs.startsWith(path.resolve(ROOT) + path.sep)) {
			res.writeHead(403);
			return res.end("outside root");
		}
		if (!existsSync(abs)) {
			res.writeHead(404);
			return res.end("no such file");
		}
		return changesFor(rel)
			.then((c) => json({ path: rel, ...c }))
			.catch((e) => {
				res.writeHead(500);
				res.end(String(e));
			});
	}
	if (url.pathname === "/write" && req.method === "POST") {
		let body = "";
		req.on("data", (d) => (body += d));
		req.on("end", () => {
			try {
				const { path: rel, text } = JSON.parse(body);
				const abs = path.resolve(ROOT, rel ?? "");
				if (!abs.startsWith(path.resolve(ROOT) + path.sep)) {
					res.writeHead(403);
					return res.end("outside root");
				}
				writeFileSync(abs, text, "utf8");
				return changesFor(rel).then((c) => json({ ok: true, path: rel, ...c }));
			} catch (e) {
				res.writeHead(400);
				res.end(String(e));
			}
		});
		return;
	}
	// The ORIGINAL side of a diff: the file as it is at the merge base. `git show`
	// rather than a worktree read, because that content does not exist on disk.
	if (url.pathname === "/file-at") {
		const rel = url.searchParams.get("path") ?? "";
		return (async () => {
			if (!mergeBase) await resolveBase();
			if (!mergeBase) return json({ path: rel, text: "", missing: true });
			const out = await git(["show", `${mergeBase}:${rel}`]);
			// A file added on this branch has no old side at all — an empty original
			// is exactly right, and is what makes the diff read as "all added".
			return json({ path: rel, ref: mergeBase, text: out ?? "", missing: out === null });
		})().catch((e) => {
			res.writeHead(500);
			res.end(String(e));
		});
	}
	if (url.pathname === "/branch-changes") {
		return branchChanges()
			.then(json)
			.catch((e) => {
				res.writeHead(500);
				res.end(String(e));
			});
	}
	if (url.pathname === "/stats") {
		return json({
			root: ROOT,
			lang: LANG,
			pid: lsp?.pid ?? null,
			upMs: lspStartedAt ? Date.now() - lspStartedAt : 0,
			files: fileIndex.length,
		});
	}
	res.writeHead(404);
	res.end("nope");
});

const wss = new WebSocketServer({ server: http, path: "/lsp" });
wss.on("connection", (ws) => {
	console.log("[ws] renderer connected");
	if (!lsp) startServer();

	// renderer -> server: one JSON-RPC message per frame, framed here.
	ws.on("message", (data) => {
		const s = data.toString();
		lsp.stdin.write(`Content-Length: ${Buffer.byteLength(s)}\r\n\r\n${s}`);
	});

	// server -> renderer: unframe, forward payloads.
	let buf = Buffer.alloc(0);
	const onData = (d) => {
		buf = Buffer.concat([buf, d]);
		for (;;) {
			const sep = buf.indexOf("\r\n\r\n");
			if (sep < 0) return;
			const len = Number(/content-length:\s*(\d+)/i.exec(buf.subarray(0, sep).toString("ascii"))?.[1] ?? -1);
			if (len < 0 || buf.length < sep + 4 + len) return;
			ws.send(buf.subarray(sep + 4, sep + 4 + len).toString("utf8"));
			buf = buf.subarray(sep + 4 + len);
		}
	};
	lsp.stdout.on("data", onData);
	ws.on("close", () => lsp?.stdout.off("data", onData));
});

await buildFileIndex();
http.listen(PORT, "127.0.0.1", () => console.log(`[bridge] http://127.0.0.1:${PORT}  root=${ROOT}  lang=${LANG}`));
