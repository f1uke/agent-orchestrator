// Recovering the login-shell environment so a Finder/Dock launch (started by
// launchd, not a shell) gets the same PATH and exported credentials a terminal
// launch would. See docs/daemon-environment.md for the root cause.
//
// Kept pure and dependency-injected (no node:* or electron imports — the
// vite-plugin-electron-renderer polyfill breaks node:* under vitest, see
// daemon-attach.ts) so the parsing/merging logic is testable directly; the real
// shell spawn lives in main.ts and is injected as a ShellRunner.

export const SHELL_ENV_SENTINEL = "__AO_SHELL_ENV__";

// PATH floor: dirs a working macOS/Linux box keeps tools in, appended when the
// shell probe fails so zellij/git/agents still resolve.
export const FALLBACK_PATH_DIRS = [
	"/opt/homebrew/bin",
	"/opt/homebrew/sbin",
	"/usr/local/bin",
	"/usr/bin",
	"/bin",
	"/usr/sbin",
	"/sbin",
];

// Ask the login shell (-l sources zprofile, -i sources zshrc) to print a
// sentinel then a NUL-separated env dump (-0 keeps values with newlines intact).
export function shellEnvArgs(): string[] {
	return ["-ilc", `printf '%s' '${SHELL_ENV_SENTINEL}'; env -0`];
}

// Slice after the sentinel (drops banner/motd/prompt noise printed before it),
// split on NUL, split each record on the first '='.
export function parseEnvBlock(stdout: string): Record<string, string> {
	const at = stdout.lastIndexOf(SHELL_ENV_SENTINEL);
	const block = at === -1 ? stdout : stdout.slice(at + SHELL_ENV_SENTINEL.length);
	const out: Record<string, string> = {};
	for (const rec of block.split("\0")) {
		if (rec === "") continue;
		const eq = rec.indexOf("=");
		if (eq <= 0) continue; // skip records with no key or a leading '='
		out[rec.slice(0, eq)] = rec.slice(eq + 1);
	}
	return out;
}

// Prefer $SHELL (the user's login shell); under launchd it may be absent, so
// fall back to /bin/zsh.
export function resolveShellPath(env: Record<string, string | undefined>): string {
	const shell = env.SHELL?.trim();
	return shell && shell.length > 0 ? shell : "/bin/zsh";
}

// Append any missing floor dirs to PATH, preserving the existing order/priority
// and de-duping.
export function withFallbackPath(currentPath: string | undefined): string {
	const result = (currentPath ?? "").split(":").filter(Boolean);
	const present = new Set(result);
	for (const dir of FALLBACK_PATH_DIRS) {
		if (!present.has(dir)) {
			present.add(dir);
			result.push(dir);
		}
	}
	return result.join(":");
}

function normalizeTerm(term: string | undefined): string {
	const trimmed = term?.trim();
	if (!trimmed || trimmed === "dumb") return "xterm-256color";
	return trimmed;
}

// AO_* env vars that scope to a single WORKER SESSION and must never define the
// daemon's own behaviour. When the desktop app is (re)launched from inside a
// worker's shell (its tmux pane), Electron's process.env carries these — and left
// unscrubbed they leak into the daemon: a stale AO_SESSION_IDLE_CLOSE silently
// overrides the user's ~/.zshrc so the idle sweep closes finished workers early,
// and AO_SESSION_ID gives the daemon a worker's identity. The daemon then
// re-propagates them to every worker it spawns, so the stale value perpetuates
// across app restarts. AO_SESSION_ID being present is the tell that the launch
// context is a worker shell (a daemon has no session of its own).
export const WORKER_SCOPED_AO_KEYS = ["AO_SESSION_ID", "AO_ISSUE_ID", "AO_OWNER", "AO_SESSION_IDLE_CLOSE"] as const;

// Drop worker-session-scoped AO_* keys from a launch env when it is a worker
// shell's env (marked by AO_SESSION_ID), so the daemon derives config from the
// clean login-shell profile + explicit overrides rather than from whatever worker
// pane happened to spawn the app. A non-worker env is returned unchanged.
export function stripWorkerScopedEnv(env: NodeJS.ProcessEnv): NodeJS.ProcessEnv {
	if (!env.AO_SESSION_ID) return env;
	const clean: NodeJS.ProcessEnv = { ...env };
	for (const key of WORKER_SCOPED_AO_KEYS) delete clean[key];
	return clean;
}

// Base = shell env, overlaid by processEnv so Electron/AO runtime vars win, then
// PATH forced to the shell's PATH (with floor), TERM forced to a tmux-usable
// value, then explicit overrides. The process.env base is first scrubbed of
// worker-session-scoped AO_* keys (see stripWorkerScopedEnv) so a launch from
// inside a worker shell cannot corrupt daemon-global config.
//
// TERM defaults to xterm-256color (what the renderer's xterm.js emulates): a
// Finder/Dock launch starts under launchd with no controlling tty, so TERM is
// unset, and the daemon's tmux attach client inherits that and dies with
// "open terminal failed: terminal does not support clear". Seeded as the base
// so a real TERM from the shell/process env still wins.
export function buildDaemonEnv(
	processEnv: NodeJS.ProcessEnv,
	shellEnv: Record<string, string> | null,
	overrides: Record<string, string>,
): NodeJS.ProcessEnv {
	const base = stripWorkerScopedEnv(processEnv);
	const merged: NodeJS.ProcessEnv = { TERM: "xterm-256color", ...(shellEnv ?? {}), ...base };
	merged.PATH = withFallbackPath(shellEnv?.PATH ?? base.PATH);
	merged.TERM = normalizeTerm(merged.TERM);
	return { ...merged, ...overrides };
}

export type ShellRunner = (shellPath: string, args: string[]) => Promise<string | null>;

// Run the probe via an injected runner (main.ts supplies the real spawn).
// Returns null on any failure or if the result lacks PATH; the caller then falls
// back to the static floor.
export async function resolveShellEnv(
	env: Record<string, string | undefined>,
	run: ShellRunner,
): Promise<Record<string, string> | null> {
	try {
		const stdout = await run(resolveShellPath(env), shellEnvArgs());
		if (stdout == null) return null;
		const parsed = parseEnvBlock(stdout);
		return parsed.PATH ? parsed : null;
	} catch {
		return null;
	}
}
