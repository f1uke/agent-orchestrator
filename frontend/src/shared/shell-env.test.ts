import { describe, expect, it } from "vitest";
import {
	buildDaemonEnv,
	FALLBACK_PATH_DIRS,
	parseEnvBlock,
	resolveShellEnv,
	resolveShellPath,
	SHELL_ENV_SENTINEL,
	type ShellRunner,
	stripWorkerScopedEnv,
	withFallbackPath,
	WORKER_SCOPED_AO_KEYS,
} from "./shell-env";

describe("parseEnvBlock", () => {
	it("parses NUL-separated records after the sentinel", () => {
		const stdout = `${SHELL_ENV_SENTINEL}PATH=/opt/homebrew/bin:/usr/bin\0HOME=/Users/me\0`;
		expect(parseEnvBlock(stdout)).toEqual({
			PATH: "/opt/homebrew/bin:/usr/bin",
			HOME: "/Users/me",
		});
	});

	it("drops banner/prompt noise printed before the sentinel", () => {
		const stdout = `motd line\nWelcome\n${SHELL_ENV_SENTINEL}FOO=bar\0`;
		expect(parseEnvBlock(stdout)).toEqual({ FOO: "bar" });
	});

	it("preserves a value containing a newline", () => {
		const stdout = `${SHELL_ENV_SENTINEL}MULTI=line1\nline2\0NEXT=ok\0`;
		expect(parseEnvBlock(stdout)).toEqual({ MULTI: "line1\nline2", NEXT: "ok" });
	});

	it("skips records with no '=' or a leading '='", () => {
		const stdout = `${SHELL_ENV_SENTINEL}NOEQUALS\0=leading\0GOOD=value\0`;
		expect(parseEnvBlock(stdout)).toEqual({ GOOD: "value" });
	});
});

describe("withFallbackPath", () => {
	it("appends only floor dirs not already present, preserving the existing order", () => {
		// /opt/homebrew/bin and /usr/bin are already present and stay put; the rest
		// of the floor is appended in floor order, with no duplicates added.
		const result = withFallbackPath("/opt/homebrew/bin:/custom/bin:/usr/bin");
		expect(result).toBe(
			"/opt/homebrew/bin:/custom/bin:/usr/bin:/opt/homebrew/sbin:/usr/local/bin:/bin:/usr/sbin:/sbin",
		);
	});

	it("yields the full floor for undefined input", () => {
		expect(withFallbackPath(undefined)).toBe(FALLBACK_PATH_DIRS.join(":"));
	});

	it("yields the full floor for empty input", () => {
		expect(withFallbackPath("")).toBe(FALLBACK_PATH_DIRS.join(":"));
	});
});

describe("buildDaemonEnv", () => {
	const minimalProcessEnv: NodeJS.ProcessEnv = { PATH: "/usr/bin:/bin" };

	it("lets overrides win over both shell and process env", () => {
		const env = buildDaemonEnv(
			{ ...minimalProcessEnv, AO_TELEMETRY_EVENTS: "off" },
			{ PATH: "/opt/homebrew/bin", AO_TELEMETRY_EVENTS: "shell" },
			{ AO_TELEMETRY_EVENTS: "on" },
		);
		expect(env.AO_TELEMETRY_EVENTS).toBe("on");
	});

	it("keeps a credential present only in the shell env", () => {
		const env = buildDaemonEnv(minimalProcessEnv, { PATH: "/opt/homebrew/bin", ANTHROPIC_API_KEY: "sk-ant" }, {});
		expect(env.ANTHROPIC_API_KEY).toBe("sk-ant");
	});

	it("takes PATH from the shell env (with floor) over a minimal process PATH", () => {
		const env = buildDaemonEnv(minimalProcessEnv, { PATH: "/opt/homebrew/bin:/usr/bin" }, {});
		expect(env.PATH).toBe("/opt/homebrew/bin:/usr/bin:/opt/homebrew/sbin:/usr/local/bin:/bin:/usr/sbin:/sbin");
	});

	it("still produces a PATH containing the floor when shellEnv is null", () => {
		const env = buildDaemonEnv(minimalProcessEnv, null, {});
		for (const dir of FALLBACK_PATH_DIRS) {
			expect(env.PATH?.split(":")).toContain(dir);
		}
	});

	it("defaults TERM when neither shell nor process env sets it (Finder launch)", () => {
		const env = buildDaemonEnv(minimalProcessEnv, null, {});
		expect(env.TERM).toBe("xterm-256color");
	});

	it("lets a real TERM from the process env win over the default", () => {
		const env = buildDaemonEnv({ ...minimalProcessEnv, TERM: "screen-256color" }, null, {});
		expect(env.TERM).toBe("screen-256color");
	});

	it("replaces TERM=dumb with a tmux-usable default", () => {
		const env = buildDaemonEnv({ ...minimalProcessEnv, TERM: "dumb" }, null, {});
		expect(env.TERM).toBe("xterm-256color");
	});

	it("lets the profile's idle-close win when launched from a worker shell (leak scrubbed)", () => {
		// Regression: the app relaunched from inside a worker pane carried a stale
		// AO_SESSION_IDLE_CLOSE=3h in process.env, which used to override the user's
		// ~/.zshrc 48h and idle-close finished workers early.
		const env = buildDaemonEnv(
			{ ...minimalProcessEnv, AO_SESSION_ID: "agent-orchestrator-48", AO_SESSION_IDLE_CLOSE: "3h" },
			{ PATH: "/opt/homebrew/bin", AO_SESSION_IDLE_CLOSE: "48h" },
			{},
		);
		expect(env.AO_SESSION_IDLE_CLOSE).toBe("48h");
		expect(env.AO_SESSION_ID).toBeUndefined();
	});

	it("still honors a process-env idle-close when NOT launched from a worker shell", () => {
		const env = buildDaemonEnv(
			{ ...minimalProcessEnv, AO_SESSION_IDLE_CLOSE: "24h" },
			{ PATH: "/opt/homebrew/bin" },
			{},
		);
		expect(env.AO_SESSION_IDLE_CLOSE).toBe("24h");
	});

	it("keeps non-worker-scoped AO vars (data dir) when scrubbing a worker-shell launch", () => {
		const env = buildDaemonEnv(
			{ ...minimalProcessEnv, AO_SESSION_ID: "agent-orchestrator-48", AO_DATA_DIR: "/Users/me/.ao/data" },
			null,
			{},
		);
		expect(env.AO_DATA_DIR).toBe("/Users/me/.ao/data");
		expect(env.AO_SESSION_ID).toBeUndefined();
	});

	it("lets AO_OWNER override win even when a worker-shell AO_OWNER is scrubbed", () => {
		const env = buildDaemonEnv(
			{ ...minimalProcessEnv, AO_SESSION_ID: "agent-orchestrator-48", AO_OWNER: "leaked" },
			null,
			{ AO_OWNER: "app" },
		);
		expect(env.AO_OWNER).toBe("app");
	});
});

describe("stripWorkerScopedEnv", () => {
	it("returns the env unchanged when AO_SESSION_ID is absent", () => {
		const env = { PATH: "/usr/bin", AO_SESSION_IDLE_CLOSE: "3h" };
		expect(stripWorkerScopedEnv(env)).toBe(env);
	});

	it("drops every worker-scoped AO_* key when AO_SESSION_ID is present", () => {
		const env = {
			PATH: "/usr/bin",
			AO_SESSION_ID: "agent-orchestrator-48",
			AO_ISSUE_ID: "",
			AO_OWNER: "app",
			AO_SESSION_IDLE_CLOSE: "3h",
			AO_DATA_DIR: "/Users/me/.ao/data",
		};
		const out = stripWorkerScopedEnv(env);
		for (const key of WORKER_SCOPED_AO_KEYS) expect(out[key]).toBeUndefined();
		expect(out.AO_DATA_DIR).toBe("/Users/me/.ao/data");
		expect(out.PATH).toBe("/usr/bin");
	});
});

describe("resolveShellPath", () => {
	it("returns $SHELL when set", () => {
		expect(resolveShellPath({ SHELL: "/bin/bash" })).toBe("/bin/bash");
	});

	it("falls back to /bin/zsh when unset", () => {
		expect(resolveShellPath({})).toBe("/bin/zsh");
	});

	it("falls back to /bin/zsh when blank", () => {
		expect(resolveShellPath({ SHELL: "   " })).toBe("/bin/zsh");
	});
});

describe("resolveShellEnv", () => {
	it("yields the parsed map on a successful probe", async () => {
		const run: ShellRunner = async () => `${SHELL_ENV_SENTINEL}PATH=/opt/homebrew/bin\0FOO=bar\0`;
		expect(await resolveShellEnv({ SHELL: "/bin/zsh" }, run)).toEqual({
			PATH: "/opt/homebrew/bin",
			FOO: "bar",
		});
	});

	it("returns null when the runner returns null", async () => {
		const run: ShellRunner = async () => null;
		expect(await resolveShellEnv({}, run)).toBeNull();
	});

	it("returns null when the runner throws", async () => {
		const run: ShellRunner = async () => {
			throw new Error("spawn failed");
		};
		expect(await resolveShellEnv({}, run)).toBeNull();
	});

	it("returns null when the parsed env lacks PATH", async () => {
		const run: ShellRunner = async () => `${SHELL_ENV_SENTINEL}FOO=bar\0`;
		expect(await resolveShellEnv({}, run)).toBeNull();
	});
});
