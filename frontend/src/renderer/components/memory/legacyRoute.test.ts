import { isRedirect } from "@tanstack/react-router";
import { describe, expect, it } from "vitest";
import { Route } from "../../routes/_shell.skills";

describe("the old /skills route", () => {
	it("redirects to /memory, replacing the history entry", () => {
		let thrown: unknown;
		try {
			(Route.options.beforeLoad as () => void)();
		} catch (e) {
			thrown = e;
		}
		expect(isRedirect(thrown)).toBe(true);
		expect((thrown as { options: { to: string; replace?: boolean } }).options).toMatchObject({
			to: "/memory",
			replace: true,
		});
	});
});
