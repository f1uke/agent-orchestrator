const QUIET_EXIT_1: Record<string, string> = {
	grep: "no match",
	egrep: "no match",
	fgrep: "no match",
	zgrep: "no match",
	rg: "no match",
	ag: "no match",
	ack: "no match",
	ugrep: "no match",
	"git grep": "no match",
	diff: "differs",
	colordiff: "differs",
	cmp: "differs",
	"git diff": "differs",
	test: "false",
	"[": "false",
	"[[": "false",
	which: "not found",
	type: "not found",
	"command -v": "not found",
	pgrep: "not found",
	pidof: "not found",
	"git merge-base --is-ancestor": "no",
};

const PREFIXES = new Set(["sudo", "env", "time", "nice", "nohup", "exec"]);
const GIT_OPTIONS_WITH_VALUE = new Set(["-C", "-c", "--git-dir", "--work-tree", "--namespace"]);
const SHELL_ERROR = /command not found|no such file or directory|permission denied/i;
const DIFF_LINE = /^[<>+\- @]/;

// Claude Code writes a non-zero exit as `Exit code N` on the result's first line, and a
// shell list exits with its last command's status, so only that command is judged.
export function quietExitLabel(command: string, result: string): string | undefined {
	const exit = /^Exit code (\d+)(?:\n|$)/.exec(result);
	if (!exit || exit[1] !== "1") return undefined;
	const output = result.slice(exit[0].length);
	if (output.split("\n").some((line) => SHELL_ERROR.test(line) && !DIFF_LINE.test(line))) return undefined;
	const key = commandKey(words(lastCommand(command)));
	return key && Object.hasOwn(QUIET_EXIT_1, key) ? QUIET_EXIT_1[key] : undefined;
}

function lastCommand(line: string): string {
	let quote = "";
	let depth = 0;
	let start = 0;
	let last = "";
	const cut = (end: number) => {
		const segment = line.slice(start, end).trim();
		if (segment) last = segment;
	};
	for (let i = 0; i < line.length; i++) {
		const c = line[i];
		if (quote) {
			if (c === "\\" && quote === '"') i++;
			else if (c === quote) quote = "";
		} else if (c === "\\") i++;
		else if (c === "'" || c === '"') quote = c;
		else if (c === "(") depth++;
		else if (c === ")") depth = Math.max(0, depth - 1);
		else if (depth === 0 && isSeparator(line, i)) {
			cut(i);
			start = i + 1;
		}
	}
	cut(line.length);
	return last;
}

function isSeparator(line: string, i: number): boolean {
	const c = line[i];
	if (c === ";" || c === "\n" || c === "|") return true;
	return c === "&" && line[i - 1] !== ">" && line[i + 1] !== ">";
}

function words(command: string): string[] {
	const out: string[] = [];
	for (const match of command.matchAll(/"((?:\\.|[^"\\])*)"|'([^']*)'|(\S+)/g))
		out.push(match[1] ?? match[2] ?? match[3]);
	return out;
}

function commandKey(argv: string[]): string | undefined {
	let i = 0;
	while (i < argv.length && (PREFIXES.has(argv[i]) || /^[A-Za-z_]\w*=/.test(argv[i]))) i++;
	const program = argv[i]?.split("/").pop();
	if (!program) return undefined;
	if (program === "command") return argv[i + 1] === "-v" ? "command -v" : commandKey(argv.slice(i + 1));
	if (program !== "git") return program;
	let j = i + 1;
	while (j < argv.length && argv[j].startsWith("-")) j += GIT_OPTIONS_WITH_VALUE.has(argv[j]) ? 2 : 1;
	const sub = argv[j];
	if (sub === "merge-base" && argv.includes("--is-ancestor")) return "git merge-base --is-ancestor";
	return sub ? `git ${sub}` : undefined;
}
