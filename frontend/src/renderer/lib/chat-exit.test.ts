import { describe, expect, it } from "vitest";
import { quietExitLabel } from "./chat-exit";

const exit = (code: number, output = "") => (output ? `Exit code ${code}\n${output}` : `Exit code ${code}`);

describe("quietExitLabel", () => {
	it.each([
		["grep with no match", "grep -rn needle src", exit(1), "no match"],
		["grep -q", "grep -q needle file.txt", exit(1), "no match"],
		["grep -c prints 0", "grep -c needle file.txt", exit(1, "0"), "no match"],
		["rg with no match", "rg needle", exit(1), "no match"],
		["git grep -c with no match", "git grep -c needle", exit(1), "no match"],
		["git grep after global options", "git -C /work/repo --no-pager grep needle", exit(1), "no match"],
		["git grep against a ref", 'git grep -n "needle" origin/main -- src', exit(1), "no match"],
		["diff with differences", "diff a.txt b.txt", exit(1, "1c1\n< a\n---\n> b"), "differs"],
		["cmp with differences", "cmp a.bin b.bin", exit(1, "a.bin b.bin differ: byte 1, line 1"), "differs"],
		["git diff --quiet", "git diff --quiet", exit(1), "differs"],
		["git diff --no-index", "git diff --no-index a b", exit(1, "diff --git a/a b/b"), "differs"],
		["test false", "test -f missing.txt", exit(1), "false"],
		["[ ] false", '[ -d "$HOME/nope" ]', exit(1), "false"],
		["[[ ]] false", '[[ "$x" == y ]]', exit(1), "false"],
		["which not found", "which nope", exit(1, "nope not found"), "not found"],
		["command -v not found", "command -v nope", exit(1), "not found"],
		["pgrep no process", "pgrep -f nope", exit(1), "not found"],
		["merge-base --is-ancestor no", "git merge-base --is-ancestor a b", exit(1), "no"],
		["pipeline ending in grep", "ps aux | grep -v grep | grep nope", exit(1), "no match"],
		["; chain ending in diff", "cd /tmp; diff a b", exit(1, "1c1"), "differs"],
		["&& chain ending in grep", "cd frontend && grep -rn needle src", exit(1), "no match"],
		["|| chain ending in grep", "grep a f || grep b f", exit(1), "no match"],
		["newline chain ending in test", "echo start\ntest -e out.log", exit(1), "false"],
		["env prefix and redirects", "LC_ALL=C grep needle file 2>/dev/null", exit(1), "no match"],
		["sudo prefix", "sudo grep needle /etc/hosts", exit(1), "no match"],
		["separator inside quotes", 'echo "a; b | c" && grep "x && y" f', exit(1), "no match"],
		["separator inside $()", 'grep "$(echo a | tr a b)" f', exit(1), "no match"],
		["trailing separator", "grep needle f;", exit(1), "no match"],
	])("%s is quiet", (_name, command, output, label) => {
		expect(quietExitLabel(command, output)).toBe(label);
	});

	it.each([
		["grep exit 2 (bad file)", "grep needle missing.txt", exit(2, "grep: missing.txt: No such file or directory")],
		["command not found", "nosuchtool --flag", exit(127, "zsh: command not found: nosuchtool")],
		["failing test run", "npx vitest run", exit(1, "FAIL src/a.test.ts")],
		["failing build", "go build ./...", exit(1, "./main.go:3:2: undefined: x")],
		["make", "make test", exit(2, "make: *** [test] Error 1")],
		["grep earlier in the pipeline", "grep needle f | wc -l", exit(1)],
		["grep before a failing step", "grep -q needle f && npm test", exit(1, "FAIL")],
		["cd failed before grep", "cd nope && grep needle f", exit(1, "(eval):cd:1: no such file or directory: nope")],
		["missing command before grep", "nosuch && grep x f", exit(1, "zsh: command not found: nosuch")],
		["plain git diff over a broken ref", "git diff nope", exit(128, "fatal: bad revision 'nope'")],
		["git status", "git status", exit(1)],
		["a command named like an object key", "constructor", exit(1)],
		["heredoc last", "cat <<'EOF' > f\ngrep x\nEOF", exit(1)],
		["no exit code (timeout or tool error)", "grep needle f", "Command timed out after 2m"],
	])("%s is a real failure", (_name, command, output) => {
		expect(quietExitLabel(command, output)).toBeUndefined();
	});

	it("exit 0 with stderr output is not an exit at all", () => {
		expect(quietExitLabel("grep needle f", "warning: something on stderr\nneedle")).toBeUndefined();
	});
});
