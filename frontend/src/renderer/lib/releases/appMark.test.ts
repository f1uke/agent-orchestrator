import { describe, expect, it } from "vitest";
import { appMark, appMarkOf } from "./appMark";
import type { BoardApp } from "./types";

const apps: BoardApp[] = [
	{ id: "di", name: "Demo iOS", family: "demo", platform: "ios" },
	{ id: "xa", name: "F4A Android", family: "f4a", platform: "android" },
	{ id: "pi", name: "iOS", family: "pilot", platform: "ios" },
];

describe("appMark", () => {
	it("names an app by its family's short name and its platform, keeping the full name", () => {
		expect(appMark(apps[0])).toEqual({ family: "Dem", platform: "ios", name: "Demo iOS" });
		expect(appMark(apps[1])).toEqual({ family: "F4A", platform: "android", name: "F4A Android" });
	});

	it("falls back to the family id when the name is only a platform", () => {
		expect(appMark(apps[2]).family).toBe("pil");
	});

	it("finds an app by id and gives null for an unknown one", () => {
		expect(appMarkOf(apps, "xa")?.family).toBe("F4A");
		expect(appMarkOf(apps, "zz")).toBeNull();
		expect(appMarkOf(apps, undefined)).toBeNull();
	});
});
