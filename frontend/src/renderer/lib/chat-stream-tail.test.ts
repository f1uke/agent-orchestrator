import { describe, expect, it } from "vitest";
import { settledPrefix } from "./chat-stream-tail";

describe("settledPrefix", () => {
	it("keeps text that has no open construct", () => {
		expect(settledPrefix("The budget caps attempts. ")).toBe("The budget caps attempts. ");
	});

	it("holds back a table until its delimiter row arrives", () => {
		expect(settledPrefix("Results:\n\n| Attempt | Ceiling |\n")).toBe("Results:\n\n");
		expect(settledPrefix("Results:\n\n| Attempt | Ceiling |\n| ---: | ---: |\n| 1 |")).toBe(
			"Results:\n\n| Attempt | Ceiling |\n| ---: | ---: |\n| 1 |",
		);
	});

	it("holds back an unclosed bold, code span or link on the last line", () => {
		expect(settledPrefix("The **retry ")).toBe("The ");
		expect(settledPrefix("Call `Delay(n) ")).toBe("Call ");
		expect(settledPrefix("See [the PR")).toBe("See ");
		expect(settledPrefix("See [the PR](https://example.invalid/pr")).toBe("See ");
		expect(settledPrefix("The **retry** budget and `Delay(n)` and [PR](https://example.invalid).")).toBe(
			"The **retry** budget and `Delay(n)` and [PR](https://example.invalid).",
		);
	});

	it("leaves an open code fence to the renderer, which draws it as a code block", () => {
		expect(settledPrefix("Run:\n\n```go\nfor ev := range ")).toBe("Run:\n\n```go\nfor ev := range ");
	});
});
