import { describe, expect, it, vi } from "vitest";
import { askToRun, runnableCommand } from "./chat-run";

describe("runnableCommand", () => {
	it("takes a one-line block in any shell language, whatever its case", () => {
		for (const lang of ["bash", "sh", "shell", "zsh", " Bash ", "ZSH"]) {
			expect(runnableCommand(lang, "pnpm test chat-widget\n"), lang).toBe("pnpm test chat-widget");
		}
	});

	it("strips the prompt from a console block, and needs it there", () => {
		expect(runnableCommand("console", "$ npm run typecheck")).toBe("npm run typecheck");
		expect(runnableCommand("console", "npm run typecheck")).toBeUndefined();
		expect(runnableCommand("console", "$ ")).toBeUndefined();
	});

	it("refuses a multi-line block, another language and an empty block", () => {
		expect(runnableCommand("bash", "cd frontend\nnpm test")).toBeUndefined();
		expect(runnableCommand("bash", "cd frontend && \\\nnpm test")).toBeUndefined();
		expect(runnableCommand("ts", "const a = 1")).toBeUndefined();
		expect(runnableCommand("", "ls")).toBeUndefined();
		expect(runnableCommand("bash", "  \n")).toBeUndefined();
	});

	it("refuses a command holding a code fence, which would end the quoted block early", () => {
		expect(runnableCommand("bash", "echo ```")).toBeUndefined();
	});
});

describe("askToRun", () => {
	it("hands the request to onPrompt only when the button is clicked", () => {
		const onPrompt = vi.fn();
		const action = askToRun(onPrompt)({ lang: "bash", text: "ls -la" });
		expect(action?.label).toBe("Ask Claude to run this");
		expect(onPrompt).not.toHaveBeenCalled();
		action?.onRun();
		expect(onPrompt).toHaveBeenCalledExactlyOnceWith("Run this command:\n\n```bash\nls -la\n```");
	});

	it("offers nothing for a block it will not run", () => {
		expect(askToRun(vi.fn())({ lang: "ts", text: "ls" })).toBeUndefined();
	});
});
