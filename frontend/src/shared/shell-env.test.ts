import { describe, expect, it } from "vitest";
import {
	buildDaemonEnv,
	FALLBACK_PATH_DIRS,
	parseEnvBlock,
	resolveShellEnv,
	resolveShellPath,
	SHELL_ENV_SENTINEL,
	type ShellRunner,
	withFallbackPath,
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

	it("lets AO_OWNER override win over a caller session's AO_OWNER", () => {
		const env = buildDaemonEnv(
			{ ...minimalProcessEnv, AO_SESSION_ID: "agent-orchestrator-48", AO_OWNER: "leaked" },
			null,
			{ AO_OWNER: "app" },
		);
		expect(env.AO_OWNER).toBe("app");
	});

	it("puts a default under the login shell and process env", () => {
		const env = buildDaemonEnv(
			minimalProcessEnv,
			{ PATH: "/opt/homebrew/bin", AO_TELEMETRY_EVENTS: "off" },
			{},
			{ AO_TELEMETRY_EVENTS: "on", AO_TELEMETRY_REMOTE: "posthog" },
		);
		expect(env.AO_TELEMETRY_EVENTS).toBe("off");
		expect(env.AO_TELEMETRY_REMOTE).toBe("posthog");
	});
});

const callerSessionEnv: NodeJS.ProcessEnv = {
	PATH: "/usr/bin:/bin",
	HOME: "/Users/me",
	SSH_AUTH_SOCK: "/private/tmp/launchd/Listeners",
	ANTHROPIC_BASE_URL: "http://127.0.0.1:20130",
	ANTHROPIC_AUTH_TOKEN: "caller-token",
	CLAUDECODE: "1",
	CLAUDE_CODE_SESSION_ID: "6f1c",
	CLAUDE_CODE_CHILD_SESSION: "1",
	CLAUDE_CODE_MESSAGING_SOCKET: "/tmp/claude.sock",
	CLAUDE_CODE_MESSAGING_TOKEN: "caller-token",
	CLAUDE_CODE_ENTRYPOINT: "cli",
	CLAUDE_CODE_EXECPATH: "/opt/homebrew/bin/claude",
	CLAUDE_CODE_SESSION_ATTENDED: "1",
	CLAUDE_CODE_DISABLE_AGENT_VIEW: "1",
	AO_SESSION_ID: "agent-orchestrator-355",
	AO_SESSION_KIND: "orchestrator",
	AO_PROJECT_ID: "agent-orchestrator",
	AO_WORKSPACE: "/Users/me/.ao/data/worktrees/agent-orchestrator/x",
	AO_AGENT_PARENT_PID: "4242",
	AO_CREW_ID: "agent-orchestrator-355",
	AO_ISSUE_ID: "",
	AO_OWNER: "app",
	AO_SESSION_IDLE_CLOSE: "3h",
	AO_JIRA_URL: "https://caller.example",
	AO_TELEMETRY_EVENTS: "on",
	AO_INSTALL_DETACHED: "1",
	TMUX: "/Users/me/.ao/data/tmux/ao-355,1,0",
	TMUX_PANE: "%0",
	AO_DATA_DIR: "/Users/me/.ao/data",
	AO_RUN_FILE: "/Users/me/.ao/running.json",
	AO_PORT: "4021",
};

const callerSessionKeys = Object.keys(callerSessionEnv).filter(
	(key) => !["PATH", "HOME", "SSH_AUTH_SOCK", "AO_DATA_DIR", "AO_RUN_FILE", "AO_PORT"].includes(key),
);

describe("launching from inside an agent session", () => {
	it("gives the daemon none of the caller session's identity or provider", () => {
		const env = buildDaemonEnv(callerSessionEnv, { PATH: "/opt/homebrew/bin" }, {});
		for (const key of callerSessionKeys) expect(env, key).not.toHaveProperty(key);
	});

	it("keeps the data dir, run file and port the caller pointed the app at", () => {
		const env = buildDaemonEnv(callerSessionEnv, { PATH: "/opt/homebrew/bin" }, {});
		expect(env.AO_DATA_DIR).toBe("/Users/me/.ao/data");
		expect(env.AO_RUN_FILE).toBe("/Users/me/.ao/running.json");
		expect(env.AO_PORT).toBe("4021");
		expect(env.SSH_AUTH_SOCK).toBe("/private/tmp/launchd/Listeners");
	});

	it("takes provider, Claude Code and AO config from the login shell instead", () => {
		const shellEnv = {
			PATH: "/opt/homebrew/bin",
			ANTHROPIC_API_KEY: "shell-key",
			CLAUDE_CODE_USE_BEDROCK: "1",
			AO_JIRA_URL: "https://jira.example",
			AO_GITLAB_HOST: "gitlab.example",
			AO_SESSION_IDLE_CLOSE: "48h",
		};
		const env = buildDaemonEnv(callerSessionEnv, shellEnv, {});
		expect(env.ANTHROPIC_API_KEY).toBe("shell-key");
		expect(env.CLAUDE_CODE_USE_BEDROCK).toBe("1");
		expect(env.AO_JIRA_URL).toBe("https://jira.example");
		expect(env.AO_GITLAB_HOST).toBe("gitlab.example");
		expect(env.AO_SESSION_IDLE_CLOSE).toBe("48h");
	});

	it("probes the login shell without the caller session's env, so the probe cannot echo it back", async () => {
		let probeEnv: NodeJS.ProcessEnv | undefined;
		const run: ShellRunner = async (_shell, _args, env) => {
			probeEnv = env;
			const dump = Object.entries(env)
				.map(([key, value]) => `${key}=${value}`)
				.join("\0");
			return `${SHELL_ENV_SENTINEL}${dump}\0`;
		};
		const shellEnv = await resolveShellEnv(callerSessionEnv, run);
		for (const key of callerSessionKeys) {
			expect(probeEnv, key).not.toHaveProperty(key);
			expect(shellEnv, key).not.toHaveProperty(key);
		}
		expect(probeEnv?.HOME).toBe("/Users/me");
		expect(probeEnv?.AO_DATA_DIR).toBe("/Users/me/.ao/data");
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
