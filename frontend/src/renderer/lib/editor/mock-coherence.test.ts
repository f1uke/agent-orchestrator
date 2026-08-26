import { describe, expect, it } from "vitest";
import { mockWorkspaceFile, mockWorkspaceFileDiff } from "../mock-data";
import { branchLaneLines, hunksOf, originalTextFrom } from "./change-lanes";

const PATH = "frontend/src/renderer/components/FilesPanel.tsx";

/**
 * `ao preview` is where this slice gets looked at, so its fixtures have to be a
 * COHERENT file rather than three separately invented payloads. If the diff's
 * new side is not the buffer the editor has open, the branch lane marks lines
 * that are not there and the discard popover restores text from another file —
 * and both of those look like editor bugs when they are fixture bugs.
 */
describe("preview fixtures describe one file", () => {
	it("the full-context diff's new side is exactly the file the editor opens", () => {
		const file = mockWorkspaceFile(PATH);
		const diff = mockWorkspaceFileDiff(PATH, { base: "target", fullContext: true });

		const modelText = file.lines.map((l) => l.text).join("\n");
		const newSide = diff.lines
			.filter((l) => l.newLine > 0 && l.kind !== "hunk")
			.map((l) => l.text)
			.join("\n");

		expect(newSide).toBe(modelText);
	});

	it("the uncommitted hunks agree with the file read's changedLines", () => {
		const file = mockWorkspaceFile(PATH);
		const head = mockWorkspaceFileDiff(PATH, { base: "head" });

		expect(hunksOf(head).map((h) => ({ start: h.start, end: h.end, kind: h.kind }))).toEqual(
			file.changedLines.map((c) => ({ start: c.start, end: c.end, kind: c.kind })),
		);
	});

	// The two levels are never merged, and the branch level is the superset:
	// everything the branch did, committed or not.
	it("the branch lane is a strict superset of the uncommitted one", () => {
		const branch = new Set(branchLaneLines(mockWorkspaceFileDiff(PATH, { base: "target", fullContext: true })));
		const uncommitted = branchLaneLines(mockWorkspaceFileDiff(PATH, { base: "head" }));

		expect(uncommitted.length).toBeGreaterThan(0);
		expect(branch.size).toBeGreaterThan(uncommitted.length);
		for (const line of uncommitted) expect(branch.has(line)).toBe(true);
	});

	it("only the full-context payload can be replayed as a file", () => {
		expect(originalTextFrom(mockWorkspaceFileDiff(PATH, { base: "target", fullContext: true }))).not.toBeNull();
		expect(originalTextFrom(mockWorkspaceFileDiff(PATH, { base: "target" }))).toBeNull();
	});
});
