import { describe, expect, it } from "vitest";
import type { ChatCommand, ChatModel } from "./chat-fold";
import { modelMenu, replaceToken, slashGroups, tokenAt } from "./chat-menus";

describe("tokenAt", () => {
	it.each([
		["/", 1, { kind: "slash", start: 0, end: 1, query: "" }],
		["/com", 4, { kind: "slash", start: 0, end: 4, query: "com" }],
		["/compact now", 3, { kind: "slash", start: 0, end: 8, query: "co" }],
		["@", 1, { kind: "mention", start: 0, end: 1, query: "" }],
		["Also cover @intern", 18, { kind: "mention", start: 11, end: 18, query: "intern" }],
		["see @src/a.ts and", 8, { kind: "mention", start: 4, end: 13, query: "src" }],
	])("%j with the caret at %i is %j", (text, caret, want) => {
		expect(tokenAt(text, caret)).toEqual(want);
	});

	it.each([
		["/compact ", 9, "past the command's first space"],
		["say /x", 6, "a slash that does not start the message"],
		["mail a@b", 8, "an @ inside a word"],
		["@path done", 10, "after the mention's space"],
		["", 0, "an empty message"],
	])("%j at %i opens nothing: %s", (text, caret) => {
		expect(tokenAt(text, caret)).toBeNull();
	});
});

describe("replaceToken", () => {
	it("completes a command and leaves the caret after its space", () => {
		expect(replaceToken("/co", tokenAt("/co", 3)!, "/compact")).toEqual({ text: "/compact ", caret: 9 });
	});

	it("replaces the whole token around the caret and keeps what follows with one space", () => {
		const text = "Also @int please";
		expect(replaceToken(text, tokenAt(text, 8)!, "@internal/a.go")).toEqual({
			text: "Also @internal/a.go please",
			caret: 20,
		});
	});
});

const cmd = (name: string, description: string, builtin?: boolean): ChatCommand => ({
	name,
	description,
	argumentHint: "",
	...(builtin === undefined ? {} : { builtin }),
});

describe("slashGroups", () => {
	const commands = [
		cmd("compact", "Summarise the conversation", true),
		cmd("clear", "Start fresh", true),
		cmd("review", "Review the branch diff", true),
		cmd("tdd", "Write the failing test first", false),
		cmd("open-pr", "Open the PR and request review", false),
		cmd("mystery", "No source given"),
	];
	const names = (q: string) => slashGroups(commands, q).map((g) => [g.label, g.commands.map((c) => c.name)]);

	it("groups built-in commands before skills, in claude's order, and counts an unmarked command as built in", () => {
		expect(names("")).toEqual([
			["Commands", ["compact", "clear", "review", "mystery"]],
			["Skills", ["tdd", "open-pr"]],
		]);
	});

	it("ranks a name prefix over a name substring over a description match, ignoring case", () => {
		expect(names("RE")).toEqual([
			["Commands", ["review", "clear"]],
			["Skills", ["open-pr"]],
		]);
	});

	it("drops a group with nothing left and returns nothing when nothing matches", () => {
		expect(names("tdd")).toEqual([["Skills", ["tdd"]]]);
		expect(names("zzz")).toEqual([]);
	});
});

const model = (value: string, resolvedModel: string, displayName = value): ChatModel => ({
	value,
	resolvedModel,
	displayName,
	description: "",
	supportsEffort: false,
	supportedEffortLevels: [],
});

describe("modelMenu", () => {
	const models = [
		model("default", "claude-opus-5-5", "Default (recommended)"),
		model("opus", "claude-opus-5-5", "Opus 5.5"),
		model("sonnet", "claude-sonnet-5-5", "Sonnet 5.5"),
		model("claude-opus-4-8", "claude-opus-4-8", "Opus 4.8"),
	];
	const values = (ms: ChatModel[]) => ms.map((m) => m.value);

	it("folds the default alias into its twin and splits aliases from pinned ids", () => {
		const menu = modelMenu(models, "");
		expect(values(menu.primary)).toEqual(["opus", "sonnet"]);
		expect(values(menu.pinned)).toEqual(["claude-opus-4-8"]);
		expect(menu.defaultValue).toBe("opus");
		expect(menu.current).toBeUndefined();
	});

	it.each([
		["sonnet", "sonnet"],
		["claude-sonnet-5-5", "sonnet"],
		["claude-opus-5-5", "opus"],
		["default", "opus"],
		["claude-opus-4-8", "claude-opus-4-8"],
	])("marks %s as %s", (inUse, want) => {
		expect(modelMenu(models, inUse).current?.value).toBe(want);
	});

	it("leaves a model it does not list unmarked", () => {
		expect(modelMenu(models, "claude-future-9").current).toBeUndefined();
	});

	it("keeps the default alias as a row when nothing else resolves to its model", () => {
		const menu = modelMenu([model("default", "claude-x", "Default"), model("sonnet", "claude-sonnet-5-5")], "default");
		expect(values(menu.primary)).toEqual(["default", "sonnet"]);
		expect(menu.defaultValue).toBeUndefined();
		expect(menu.current?.value).toBe("default");
	});
});
