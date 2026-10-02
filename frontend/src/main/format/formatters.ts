import { execFile, spawn } from "node:child_process";
import path from "node:path";
import { formatterFor } from "../../shared/formatter-languages";
import { findLocalBin, findPrettierConfig, findSwiftFormatConfig, type Fs, nodeFs } from "./project-config";

export { formatterFor };

/**
 * A language's own formatter, run as the standard tool installed on this Mac -
 * the format-document path when no language server is attached to do it.
 *
 * The contract the editor relies on: this either returns the formatted text,
 * or it returns a reason and NO text. A formatter that is missing, crashes,
 * times out, or prints nothing for a non-empty file is a failure, so a broken
 * tool can never empty the buffer.
 */

export type FormatRequest = {
	/** Monaco language id. */
	languageId: string;
	/** The file's absolute path: config lookup, `.prettierignore`, and the name in messages. */
	filePath: string;
	workspaceRoot?: string;
	text: string;
	/** The editor's indentation, for a formatter the project has not configured. */
	insertSpaces: boolean;
	tabSize: number;
};

export type FormatResult =
	| { ok: true; text: string; formatter: string }
	| {
			ok: false;
			/** `unavailable`: no formatter applies (or none is installed). `failed`: it ran and refused. */
			reason: "unavailable" | "failed";
			formatter?: string;
			message: string;
	  };

export type FormatterDeps = {
	env: NodeJS.ProcessEnv;
	fs?: Fs;
	timeoutMs?: number;
	run?: RunTool;
	/** Where swift-format lives, found once per process. Injected by tests. */
	findSwiftFormat?: (env: NodeJS.ProcessEnv) => Promise<string | null>;
};

export type ToolOutcome =
	| { kind: "exited"; code: number; stdout: string; stderr: string }
	| { kind: "missing" }
	| { kind: "timeout" }
	| { kind: "error"; message: string };

export type RunTool = (
	command: string,
	args: string[],
	options: { input: string; cwd: string; env: NodeJS.ProcessEnv; timeoutMs: number },
) => Promise<ToolOutcome>;

const DEFAULT_TIMEOUT_MS = 10_000;
/** A formatter printing more than this is not formatting a source file. */
const MAX_OUTPUT_BYTES = 32 * 1024 * 1024;

export async function runFormatter(request: FormatRequest, deps: FormatterDeps): Promise<FormatResult> {
	const fs = deps.fs ?? nodeFs;
	const run = deps.run ?? runTool;
	const timeoutMs = deps.timeoutMs ?? DEFAULT_TIMEOUT_MS;
	const cwd = request.workspaceRoot ?? path.dirname(request.filePath);
	const name = path.basename(request.filePath);

	switch (formatterFor(request.languageId)) {
		case "gofmt": {
			const outcome = await run("gofmt", [], { input: request.text, cwd, env: deps.env, timeoutMs });
			return settle("gofmt", outcome, request.text, name, "gofmt is not installed - it ships with Go.");
		}
		case "swift-format": {
			const binary = await (deps.findSwiftFormat ?? findSwiftFormat)(deps.env);
			if (!binary) {
				return {
					ok: false,
					reason: "unavailable",
					formatter: "swift-format",
					message: "swift-format was not found - it ships with Xcode 16 and later.",
				};
			}
			const args = ["format", "--assume-filename", request.filePath];
			// With no `.swift-format` in the project, swift-format's own default is
			// two spaces - not what this file is indented with. Hand it the editor's
			// indentation, the same thing sourcekit-lsp does with FormattingOptions.
			if (!(await findSwiftFormatConfig(request.filePath, fs))) {
				const indentation = request.insertSpaces ? { spaces: request.tabSize } : { tabs: 1 };
				args.push("--configuration", JSON.stringify({ version: 1, indentation, tabWidth: request.tabSize }));
			}
			args.push("-");
			const outcome = await run(binary, args, { input: request.text, cwd, env: deps.env, timeoutMs });
			return settle("swift-format", outcome, request.text, name, "swift-format could not be started.");
		}
		case "prettier": {
			const config = await findPrettierConfig(request.filePath, request.workspaceRoot, fs);
			if (!config) {
				return {
					ok: false,
					reason: "unavailable",
					formatter: "prettier",
					message: "This project does not configure Prettier.",
				};
			}
			const binary = await findLocalBin("prettier", request.filePath, request.workspaceRoot, fs);
			if (!binary) {
				return {
					ok: false,
					reason: "failed",
					formatter: "prettier",
					message: "This project configures Prettier but it is not installed - run its package install first.",
				};
			}
			const outcome = await run(binary, ["--stdin-filepath", request.filePath], {
				input: request.text,
				cwd,
				env: deps.env,
				timeoutMs,
			});
			return settle("prettier", outcome, request.text, name, "Prettier could not be started.");
		}
		default:
			return { ok: false, reason: "unavailable", message: "No formatter for this language." };
	}
}

function settle(
	formatter: string,
	outcome: ToolOutcome,
	input: string,
	fileName: string,
	missing: string,
): FormatResult {
	switch (outcome.kind) {
		case "missing":
			return { ok: false, reason: "unavailable", formatter, message: missing };
		case "timeout":
			return { ok: false, reason: "failed", formatter, message: `${formatter} did not finish in time.` };
		case "error":
			return { ok: false, reason: "failed", formatter, message: `${formatter} could not run: ${outcome.message}` };
		case "exited": {
			if (outcome.code !== 0) {
				return { ok: false, reason: "failed", formatter, message: firstProblem(outcome.stderr, fileName, formatter) };
			}
			if (outcome.stdout.trim() === "" && input.trim() !== "") {
				return { ok: false, reason: "failed", formatter, message: `${formatter} returned nothing.` };
			}
			return { ok: true, text: outcome.stdout, formatter };
		}
	}
}

/**
 * The first line of a tool's complaint, worth showing in one line: the path
 * prefix shortened to the file's name, ANSI colour dropped, deprecation
 * chatter skipped.
 */
export function firstProblem(stderr: string, fileName: string, formatter: string): string {
	const lines = stderr
		.replace(/\x1b\[[0-9;]*m/g, "")
		.split(/\r?\n/)
		.map((line) => line.trim())
		.filter((line) => line !== "" && !/warning: Running swift-format without input paths is deprecated/.test(line));
	const line = lines.find((l) => /error|expected|unexpected|\d+:\d+/i.test(l)) ?? lines[0];
	if (!line) return `${formatter} failed.`;
	return line
		.replace(
			/^(?:<standard input>|[^\s:]*\/)?([^/\s:]*):(\d+):(\d+):\s*/,
			(_m, file: string, row: string, col: string) => {
				const shown = file && file !== "<standard input>" ? file : fileName;
				return `${shown}:${row}:${col}: `;
			},
		)
		.replace(/^<standard input>/, fileName)
		.replace(/^\[error\]\s*/, "");
}

let swiftFormatPath: Promise<string | null> | null = null;

/**
 * swift-format ships inside Xcode's toolchain and is not on PATH, so `xcrun`
 * is asked first; a standalone install on PATH is the fallback. Found once.
 */
export function findSwiftFormat(env: NodeJS.ProcessEnv): Promise<string | null> {
	swiftFormatPath ??= (async () => {
		if (process.platform === "darwin") {
			const found = await new Promise<string | null>((resolve) => {
				execFile("xcrun", ["--find", "swift-format"], { env, timeout: 10_000 }, (error, stdout) => {
					resolve(error ? null : stdout.trim() || null);
				});
			});
			if (found) return found;
		}
		const probe = await runTool("swift-format", ["--version"], { input: "", cwd: "/", env, timeoutMs: 10_000 });
		return probe.kind === "exited" && probe.code === 0 ? "swift-format" : null;
	})();
	return swiftFormatPath;
}

/** Spawn a tool, feed it stdin, collect stdout/stderr - with a deadline and a size cap. */
export const runTool: RunTool = (command, args, options) =>
	new Promise((resolve) => {
		let settled = false;
		const finish = (outcome: ToolOutcome) => {
			if (settled) return;
			settled = true;
			clearTimeout(timer);
			resolve(outcome);
		};
		const child = spawn(command, args, { cwd: options.cwd, env: options.env, stdio: ["pipe", "pipe", "pipe"] });
		const stdout: Buffer[] = [];
		const stderr: Buffer[] = [];
		let size = 0;
		const timer = setTimeout(() => {
			child.kill("SIGKILL");
			finish({ kind: "timeout" });
		}, options.timeoutMs);
		child.on("error", (error: NodeJS.ErrnoException) => {
			finish(error.code === "ENOENT" ? { kind: "missing" } : { kind: "error", message: error.message });
		});
		child.stdout.on("data", (chunk: Buffer) => {
			size += chunk.length;
			if (size > MAX_OUTPUT_BYTES) {
				child.kill("SIGKILL");
				finish({ kind: "error", message: "it printed far more than the file holds" });
				return;
			}
			stdout.push(chunk);
		});
		child.stderr.on("data", (chunk: Buffer) => stderr.push(chunk));
		child.on("close", (code) => {
			finish({
				kind: "exited",
				code: code ?? -1,
				stdout: Buffer.concat(stdout).toString("utf8"),
				stderr: Buffer.concat(stderr).toString("utf8"),
			});
		});
		// A tool that exits before reading all of stdin raises EPIPE here; the
		// exit code is the answer that matters, so the write error is swallowed.
		child.stdin.on("error", () => undefined);
		child.stdin.end(options.input);
	});
