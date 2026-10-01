import { describe, expect, test } from "vitest";
import type { InfillRequest } from "../../../main/inline-completion/infill";
import { CompletionCache } from "./cache";

const at = (prompt: string, suffix = "\n}"): InfillRequest => ({
	inputPrefix: "func main() {\n",
	prompt,
	inputSuffix: suffix,
	inputExtra: [],
	nIndent: 1,
});

describe("CompletionCache", () => {
	test("an exact hit", () => {
		const cache = new CompletionCache();
		cache.set(at("\tfmt.Pri"), 'ntln("hi")');
		expect(cache.get(at("\tfmt.Pri"))).toBe('ntln("hi")');
	});

	test("typing ALONG the suggestion is answered from the cache, minus what was typed", () => {
		const cache = new CompletionCache();
		cache.set(at("\tfmt.Pri"), 'ntln("hi")');
		expect(cache.get(at("\tfmt.Prin"))).toBe('tln("hi")');
		expect(cache.get(at("\tfmt.Println("))).toBe('"hi")');
	});

	test("typing AWAY from it is a miss, not a wrong answer", () => {
		const cache = new CompletionCache();
		cache.set(at("\tfmt.Pri"), 'ntln("hi")');
		expect(cache.get(at("\tfmt.Prix"))).toBeNull();
	});

	test("typing the whole suggestion leaves nothing to show", () => {
		const cache = new CompletionCache();
		cache.set(at("\tx"), "yz");
		expect(cache.get(at("\txyz"))).toBeNull();
	});

	test("a different suffix is a different place", () => {
		const cache = new CompletionCache();
		cache.set(at("\tfmt.Pri"), "ntln()");
		expect(cache.get(at("\tfmt.Pri", "\n}\n// more"))).toBeNull();
	});

	test("evicts the least recently used past capacity", () => {
		const cache = new CompletionCache(2);
		cache.set(at("a"), "1");
		cache.set(at("b"), "2");
		expect(cache.get(at("a"))).toBe("1");
		cache.set(at("c"), "3");
		expect(cache.get(at("b"))).toBeNull();
		expect(cache.get(at("a"))).toBe("1");
		expect(cache.size).toBe(2);
	});
});
