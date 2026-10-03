import { describe, expect, it } from "vitest";
import { caFilesCount, caFilesSummary, normalizeSimTrustSettings, parseCaFileLines, sameCaFiles } from "./sim-trust";

describe("sim-trust", () => {
	it("reads one path per line, trimmed, without blank lines", () => {
		expect(parseCaFileLines("  /a.pem\n\n~/b.pem  \n")).toEqual(["/a.pem", "~/b.pem"]);
		expect(parseCaFileLines("")).toEqual([]);
	});

	it("compares lists in order", () => {
		expect(sameCaFiles(["/a", "/b"], ["/a", "/b"])).toBe(true);
		expect(sameCaFiles(["/a", "/b"], ["/b", "/a"])).toBe(false);
		expect(sameCaFiles([], ["/a"])).toBe(false);
	});

	it("summarises a list by file name, and an empty one as None", () => {
		expect(caFilesSummary(["~/x/proxyman-ca.pem", "/opt/charles.pem"])).toBe("proxyman-ca.pem, charles.pem");
		expect(caFilesSummary([])).toBe("None");
	});

	it("counts a list for the at-rest value", () => {
		expect(caFilesCount([])).toBe("None");
		expect(caFilesCount(["/a"])).toBe("1 file");
		expect(caFilesCount(["/a", "/b"])).toBe("2 files");
	});

	it("reads a response missing its lists as empty", () => {
		expect(normalizeSimTrustSettings(undefined)).toEqual({ caFiles: [], defaultCaFiles: [], found: [] });
	});
});
