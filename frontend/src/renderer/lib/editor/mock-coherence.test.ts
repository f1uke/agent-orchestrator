import { describe, expect, it } from "vitest";
import { mockWorkspaceFile, mockWorkspaceFileBase, mockWorkspaceFileDiff } from "../mock-data";
import { hunksBetween, linesOf } from "./live-changes";

const PATH = "frontend/src/renderer/components/FilesPanel.tsx";

/**
 * `ao preview` is where this surface gets looked at, so its fixtures have to be
 * a COHERENT file rather than separately invented payloads. If a base is not
 * the base of the buffer the editor has open, the lanes mark lines that are not
 * there and the discard popover restores text from another file - and both of
 * those look like editor bugs when they are fixture bugs.
 */
describe("preview fixtures describe one file", () => {
	const current = linesOf(mockWorkspaceFile(PATH).lines.map((l) => l.text).join("\n"));

	it("the full-context diff's new side is exactly the file the editor opens", () => {
		const diff = mockWorkspaceFileDiff(PATH, { base: "target", fullContext: true });
		const newSide = diff.lines.filter((l) => l.newLine > 0 && l.kind !== "hunk").map((l) => l.text);
		expect(newSide).toEqual(current);
	});

	it("the HEAD base, measured against the file, gives the file read's changedLines", () => {
		const head = linesOf(mockWorkspaceFileBase(PATH, "head").text);
		expect(hunksBetween(head, current).map((h) => ({ start: h.start, end: h.end, kind: h.kind }))).toEqual(
			mockWorkspaceFile(PATH).changedLines.map((c) => ({ start: c.start, end: c.end, kind: c.kind })),
		);
	});

	// The branch level is the superset: everything the branch did, committed or not.
	it("the branch lane is a strict superset of the uncommitted one", () => {
		const lines = (base: "head" | "target") =>
			hunksBetween(linesOf(mockWorkspaceFileBase(PATH, base).text), current).flatMap((h) =>
				Array.from({ length: h.end - h.start + 1 }, (_, i) => h.start + i),
			);
		const branch = new Set(lines("target"));
		const uncommitted = lines("head");
		expect(uncommitted.length).toBeGreaterThan(0);
		expect(branch.size).toBeGreaterThan(uncommitted.length);
		for (const line of uncommitted) expect(branch.has(line)).toBe(true);
	});
});
