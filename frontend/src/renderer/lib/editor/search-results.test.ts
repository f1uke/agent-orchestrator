import { describe, expect, it } from "vitest";
import {
	type WorkspaceSearchFile,
	type WorkspaceSearchMatch,
	type WorkspaceSearchResponse,
	fileCountLabel,
	searchRows,
	searchSummary,
	splitPreview,
	trimPreviewIndent,
} from "./search-results";

function match(over: Partial<WorkspaceSearchMatch> = {}): WorkspaceSearchMatch {
	return {
		line: 12,
		// "class LoginViewModel {" — `ViewModel` is the 12th UTF-16 unit (1-based
		// column) and sits at offsets 11..20 (0-based) in the preview.
		column: 12,
		endColumn: 21,
		preview: "class LoginViewModel {",
		previewStart: 11,
		previewEnd: 20,
		...over,
	};
}

function file(over: Partial<WorkspaceSearchFile> = {}): WorkspaceSearchFile {
	const matches = over.matches ?? [match()];
	return { path: "App/Login.swift", matches, total: matches.length, truncated: false, ...over };
}

function response(over: Partial<WorkspaceSearchResponse> = {}): WorkspaceSearchResponse {
	return {
		available: true,
		query: "ViewModel",
		files: [],
		totalMatches: 0,
		totalFiles: 0,
		filesSearched: 100,
		truncated: false,
		...over,
	};
}

describe("searchRows", () => {
	it("puts a header before each file's matches", () => {
		const rows = searchRows(
			[file({ matches: [match({ line: 3 }), match({ line: 9 })] }), file({ path: "App/Signup.swift" })],
			new Set(),
		);
		expect(rows.map((r) => r.kind)).toEqual(["file", "match", "match", "file", "match"]);
	});

	it("keeps a collapsed file's header and drops its matches", () => {
		const rows = searchRows([file({ matches: [match(), match({ line: 40 })] })], new Set(["App/Login.swift"]));
		expect(rows).toHaveLength(1);
		expect(rows[0].kind).toBe("file");
	});

	it("keys two matches on the same line apart by column", () => {
		// The same term twice on one line is the case where a line-only key would
		// collapse two rows into one under React.
		const rows = searchRows([file({ matches: [match({ column: 11 }), match({ column: 30 })] })], new Set());
		expect(new Set(rows.map((r) => r.key)).size).toBe(rows.length);
	});
});

describe("fileCountLabel", () => {
	it("is the plain total when nothing was capped", () => {
		expect(fileCountLabel(file({ total: 4, matches: [match(), match(), match(), match()] }))).toBe("4");
	});

	it("says how much of the total is shown when the file was capped", () => {
		expect(fileCountLabel(file({ total: 512, truncated: true, matches: [match()] }))).toBe("1 of 512");
	});
});

describe("searchSummary", () => {
	it("reports nothing found, and how much was looked at", () => {
		// "nothing" and "nothing, in 100 files" are different facts, and the second
		// is the one that tells the reader the search actually ran.
		expect(searchSummary(response())).toBe("No results in 100 files");
	});

	it("reports the honest totals, not the returned prefix", () => {
		expect(searchSummary(response({ totalMatches: 12847, totalFiles: 1203, files: [file()] }))).toContain(
			"12,847 results in 1,203 files",
		);
	});

	it("says out loud when the list is a prefix", () => {
		// Silent truncation reads as "that's all there is" — the trap #254 named.
		const res = response({ totalMatches: 12847, totalFiles: 1203, truncated: true, files: [file()] });
		expect(searchSummary(res)).toBe("12,847 results in 1,203 files — showing 1");
	});

	it("does not claim truncation when everything found was returned", () => {
		const res = response({ totalMatches: 1, totalFiles: 1, truncated: true, files: [file()] });
		expect(searchSummary(res)).toBe("1 result in 1 file");
	});

	it("counts one result and one file in the singular", () => {
		expect(searchSummary(response({ totalMatches: 1, totalFiles: 1, files: [file()] }))).toBe("1 result in 1 file");
	});
});

describe("splitPreview", () => {
	it("splits the line into before, match and after", () => {
		expect(splitPreview(match())).toEqual({ before: "class Login", hit: "ViewModel", after: " {" });
	});

	it("clamps offsets that fall outside the preview", () => {
		// A row must paint even if the two ends disagree; it must never throw.
		expect(splitPreview(match({ previewStart: 500, previewEnd: 900 }))).toEqual({
			before: "class LoginViewModel {",
			hit: "",
			after: "",
		});
	});
});

describe("trimPreviewIndent", () => {
	it("removes the leading indent and moves the highlight with it", () => {
		const trimmed = trimPreviewIndent(match({ preview: "\t\t  let viewModel = 1", previewStart: 8, previewEnd: 17 }));
		expect(trimmed.preview).toBe("let viewModel = 1");
		expect(splitPreview(trimmed).hit).toBe("viewModel");
	});

	it("leaves the file COLUMN alone, so the hit still opens on the match", () => {
		// Display and navigation are different coordinates on purpose: trimming
		// what is drawn must not move where the caret lands.
		const original = match({ preview: "    let viewModel = 1", column: 9, previewStart: 8, previewEnd: 17 });
		expect(trimPreviewIndent(original).column).toBe(9);
	});

	it("returns the same object when there is no indent to trim", () => {
		const original = match();
		expect(trimPreviewIndent(original)).toBe(original);
	});
});
