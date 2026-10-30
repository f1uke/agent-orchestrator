import type { ChatCommand, ChatModel } from "./chat-fold";

export type ComposerToken =
	| { kind: "slash"; start: 0; end: number; query: string }
	| { kind: "mention"; start: number; end: number; query: string };

export function tokenAt(text: string, caret: number): ComposerToken | null {
	const head = text.slice(0, caret);
	const end = caret + (/^\S*/.exec(text.slice(caret))?.[0].length ?? 0);
	const slash = /^\/(\S*)$/.exec(head);
	if (slash) return { kind: "slash", start: 0, end, query: slash[1] };
	const mention = /(^|\s)@(\S*)$/.exec(head);
	if (mention) return { kind: "mention", start: mention.index + mention[1].length, end, query: mention[2] };
	return null;
}

export function replaceToken(text: string, token: ComposerToken, replacement: string): { text: string; caret: number } {
	const rest = text.slice(token.end).replace(/^ /, "");
	const inserted = `${replacement} `;
	return { text: text.slice(0, token.start) + inserted + rest, caret: token.start + inserted.length };
}

export type SlashGroup = { label: "Commands" | "Skills"; commands: ChatCommand[] };

export function slashGroups(commands: readonly ChatCommand[], query: string): SlashGroup[] {
	const q = query.toLowerCase();
	const tier = (c: ChatCommand) => {
		const name = c.name.toLowerCase();
		if (name.startsWith(q)) return 0;
		if (name.includes(q)) return 1;
		if (c.description.toLowerCase().includes(q)) return 2;
		return -1;
	};
	const ranked = commands
		.map((command, order) => ({ command, order, tier: tier(command) }))
		.filter((r) => r.tier >= 0)
		.sort((a, b) => a.tier - b.tier || a.order - b.order)
		.map((r) => r.command);
	const groups: SlashGroup[] = [
		{ label: "Commands", commands: ranked.filter((c) => c.builtin !== false) },
		{ label: "Skills", commands: ranked.filter((c) => c.builtin === false) },
	];
	return groups.filter((g) => g.commands.length > 0);
}

export type ModelMenu = {
	/** Aliases such as "sonnet", which follow claude's latest release, in claude's order. */
	primary: ChatModel[];
	pinned: ChatModel[];
	current?: ChatModel;
	defaultValue?: string;
};

const isPinned = (m: ChatModel) => m.resolvedModel !== "" && m.value === m.resolvedModel;

/** `model` may be an alias (from --model) or a resolved id (from system/init). */
export function modelMenu(models: readonly ChatModel[], model: string): ModelMenu {
	const alias = models.find((m) => m.value === "default");
	const twin = alias && models.find((m) => m !== alias && m.resolvedModel === alias.resolvedModel);
	const shown = twin ? models.filter((m) => m !== alias) : [...models];
	const wanted = model === "default" && twin ? twin.value : model;
	const current =
		shown.find((m) => m.value === wanted) ?? (wanted ? shown.find((m) => m.resolvedModel === wanted) : undefined);
	return {
		primary: shown.filter((m) => !isPinned(m)),
		pinned: shown.filter(isPinned),
		current,
		defaultValue: twin?.value,
	};
}
