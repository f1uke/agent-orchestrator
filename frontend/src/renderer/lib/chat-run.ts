const SHELLS = new Set(["bash", "sh", "shell", "zsh", "console"]);

export type CodeAction = (code: { lang: string; text: string }) => { label: string; onRun: () => void } | undefined;

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

// Never runs anything: the human sends the request, and Claude's own permission prompt still applies to the command.
export const askToRun =
	(onPrompt: (text: string) => void): CodeAction =>
	({ lang, text }) => {
		const command = runnableCommand(lang, text);
		return command === undefined
			? undefined
			: { label: "Ask Claude to run this", onRun: () => onPrompt(runRequest(command)) };
	};
