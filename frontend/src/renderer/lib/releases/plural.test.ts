import { describe, expect, it } from "vitest";
import { plural } from "./plural";

describe("plural", () => {
	it("keeps the noun singular for one and pluralises it for every other count", () => {
		expect(plural(1, "rule")).toBe("1 rule");
		expect(plural(0, "rule")).toBe("0 rules");
		expect(plural(7, "hide")).toBe("7 hides");
	});
});
