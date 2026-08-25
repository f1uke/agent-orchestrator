// THROWAWAY SPIKE — stands in for what the Electron MAIN process would do.
// The renderer is sandboxed (contextIsolation, no node), so it can never spawn
// a language server itself. Here that hop is a loopback WebSocket, which is
// already allowed by the app's CSP (`connect-src ws://127.0.0.1:*`).
import { spawn, execFile } from "node:child_process";
import { readFileSync, existsSync, readdirSync } from "node:fs";
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
	lsp.on("exit", (c) => console.log(`[lsp] exited ${c}`));
	console.log(`[lsp] spawned ${cfg.cmd} pid=${lsp.pid} root=${ROOT}`);
}

const http = createServer((req, res) => {
	const url = new URL(req.url, "http://localhost");
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
