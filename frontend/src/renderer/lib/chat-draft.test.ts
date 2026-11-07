import { afterEach, describe, expect, it } from "vitest";
import { loadDraft, MAX_DRAFTS, saveDraft } from "./chat-draft";

afterEach(() => window.localStorage.clear());

describe("chat drafts", () => {
	it("keeps only the most recently edited drafts", () => {
		for (let i = 0; i <= MAX_DRAFTS; i++) saveDraft(`s${i}`, { text: `draft ${i}`, mode: "prompt" }, i);
		expect(loadDraft("s0")).toBeNull();
		expect(loadDraft("s1")).toEqual({ text: "draft 1", mode: "prompt" });
		expect(loadDraft(`s${MAX_DRAFTS}`)).toEqual({ text: `draft ${MAX_DRAFTS}`, mode: "prompt" });
	});

	it("removes the storage key once the last draft is cleared", () => {
		saveDraft("s1", { text: "x", mode: "bash" });
		expect(loadDraft("s1")).toEqual({ text: "x", mode: "bash" });
		saveDraft("s1", { text: "", mode: "prompt" });
		expect(window.localStorage.getItem("ao.chat.drafts")).toBeNull();
	});

	it("reads corrupt storage as no drafts", () => {
		window.localStorage.setItem("ao.chat.drafts", "{not json");
		expect(loadDraft("s1")).toBeNull();
		saveDraft("s1", { text: "fresh", mode: "prompt" });
		expect(loadDraft("s1")).toEqual({ text: "fresh", mode: "prompt" });
	});
});
