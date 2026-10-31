const SHELLS = new Set(["bash", "sh", "shell", "zsh", "console"]);

export type CodeAction = (code: { lang: string; text: string }) => { label: string; onRun: () => void } | undefined;

/** The command a one-line shell block holds, or undefined when the block is not one to offer a Run button on. */
export function runnableCommand(lang: string, text: string): string | undefined {
	const shell = lang.trim().toLowerCase();
	if (!SHELLS.has(shell)) return undefined;
	let command = text.trim();
	if (shell === "console") {
		if (!command.startsWith("$ ")) return undefined;
		command = command.slice(2).trim();
	}
	return command === "" || /[\r\n]/.test(command) || command.includes("```") ? undefined : command;
}

export const runRequest = (command: string) => `Run this command:\n\n\`\`\`bash\n${command}\n\`\`\``;

/** A Run button that fills the message box with a request. The human sends it and Claude's permission flow decides. */
export const askToRun =
	(onPrompt: (text: string) => void): CodeAction =>
	({ lang, text }) => {
		const command = runnableCommand(lang, text);
		return command === undefined
			? undefined
			: { label: "Ask Claude to run this", onRun: () => onPrompt(runRequest(command)) };
	};
