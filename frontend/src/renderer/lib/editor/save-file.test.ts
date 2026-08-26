import { describe, expect, it } from "vitest";
import { editabilityOf } from "./editability";
import { buildSaveRequest, fileBytes } from "./save-file";
import { saveFailureFrom } from "./save-errors";
import type { components } from "../../../api/schema";

type WorkspaceFile = components["schemas"]["WorkspaceFileResponse"];

const file = (over: Partial<WorkspaceFile> = {}): WorkspaceFile => ({
	available: true,
	path: "a.ts",
	lines: [{ kind: "context", text: "x", oldLine: 1, newLine: 1 }],
	changedLines: [],
	contentHash: "sha256:abc",
	trailingNewline: true,
	truncated: false,
	...over,
});

describe("buildSaveRequest", () => {
	it("carries all three keys for ordinary content", () => {
		const body = buildSaveRequest({ path: "a.ts", text: "hello", baseHash: "sha256:abc" });

		expect(body).toEqual({ path: "a.ts", content: "hello", baseHash: "sha256:abc" });
	});

	// 🗝 This is the bug the guard exists for. `JSON.stringify` DROPS a key whose
	// value is undefined, and before the route was hardened a body with no
	// `content` emptied the file and answered 200. `null` is the other spelling -
	// an ordinary way to say "the model has not loaded yet" - and stringify KEEPS
	// that one, so a test covering only the missing key covers half the bug.
	it("refuses undefined and null content, which are the two ways to say 'no content'", () => {
		expect(() => buildSaveRequest({ path: "a.ts", text: undefined, baseHash: "sha256:abc" })).toThrow(/no content/i);
		expect(() => buildSaveRequest({ path: "a.ts", text: null, baseHash: "sha256:abc" })).toThrow(/no content/i);
	});

	it("still lets a file be emptied, spelled as an explicit empty string", () => {
		const body = buildSaveRequest({ path: "a.ts", text: "", baseHash: "sha256:abc" });

		expect(body.content).toBe("");
		expect("content" in body).toBe(true);
	});

	it("refuses a save with no base hash rather than sending an unservable request", () => {
		expect(() => buildSaveRequest({ path: "a.ts", text: "x", baseHash: undefined })).toThrow(/content hash/i);
		expect(() => buildSaveRequest({ path: "a.ts", text: "x", baseHash: "" })).toThrow(/content hash/i);
	});

	it("refuses a path outside the workspace, which the route confines", () => {
		expect(() => buildSaveRequest({ path: "/etc/hosts", text: "x", baseHash: "sha256:abc" })).toThrow(/read-only/i);
		expect(() => buildSaveRequest({ path: "~/notes.md", text: "x", baseHash: "sha256:abc" })).toThrow(/read-only/i);
	});
});

describe("fileBytes", () => {
	// The read splits on \n after trimming the final one, so the trailing newline
	// is not recoverable from `lines` — the spike dropped it and turned a one-line
	// change into a two-line diff.
	it("puts back a trailing newline the read reported", () => {
		expect(fileBytes("a\nb", true)).toBe("a\nb\n");
		expect(fileBytes("a\nb\n", true)).toBe("a\nb\n");
	});

	it("adds nothing to a file that had none", () => {
		expect(fileBytes("a\nb", false)).toBe("a\nb");
	});
});

describe("editabilityOf", () => {
	it("is editable only for an in-workspace, available, untruncated, hashed file", () => {
		expect(editabilityOf(file(), "a.ts")).toEqual({ editable: true });
	});

	it("is read-only for a file outside the workspace, whatever else is true", () => {
		const verdict = editabilityOf(file(), "/Users/me/notes.md");

		expect(verdict.editable).toBe(false);
		expect(verdict.editable === false && verdict.chip).toMatch(/outside this workspace/);
	});

	it("is read-only when the read was truncated, because saving would delete the tail", () => {
		const verdict = editabilityOf(file({ truncated: true }), "a.ts");

		expect(verdict.editable).toBe(false);
		expect(verdict.editable === false && verdict.detail).toMatch(/delete everything after/i);
	});

	it("is read-only for a file the read could not display", () => {
		expect(editabilityOf(file({ available: false, reason: "binary" }), "a.ts").editable).toBe(false);
		expect(editabilityOf(file({ available: false, reason: "too_large" }), "a.ts").editable).toBe(false);
	});

	it("is read-only when the read handed out no content hash to write against", () => {
		expect(editabilityOf(file({ contentHash: undefined }), "a.ts").editable).toBe(false);
	});

	it("is read-only before the file has loaded", () => {
		expect(editabilityOf(undefined, "a.ts").editable).toBe(false);
	});
});

describe("saveFailureFrom", () => {
	it("makes a conflict its own kind, carrying what is on disk now", () => {
		const failure = saveFailureFrom({
			code: "WORKSPACE_FILE_CONFLICT",
			message: "changed",
			details: { currentHash: "sha256:new", currentSize: 1440, currentModifiedAt: "2026-08-26T10:00:00Z" },
		});

		expect(failure.kind).toBe("conflict");
		expect(failure.current).toEqual({ hash: "sha256:new", size: 1440, modifiedAt: "2026-08-26T10:00:00Z" });
		expect(failure.detail).toMatch(/nothing was written/i);
	});

	it("names the truncation refusal in terms of what would be lost", () => {
		const failure = saveFailureFrom({ code: "WORKSPACE_FILE_NOT_EDITABLE", details: { reason: "truncated" } });

		expect(failure.kind).toBe("refused");
		expect(failure.detail).toMatch(/delete everything after/i);
	});

	it("explains the line cap rather than hiding it", () => {
		const failure = saveFailureFrom({ code: "WORKSPACE_FILE_CONTENT_REJECTED", details: { reason: "too_many_lines" } });

		expect(failure.detail).toMatch(/2000 lines/);
		expect(failure.detail).toMatch(/never be saved again/i);
	});

	it("says a file outside the workspace is a read-only file, not a broken request", () => {
		expect(saveFailureFrom({ code: "WORKSPACE_FILE_PATH_INVALID" }).detail).toMatch(/only save one inside/i);
	});

	// A code we did not anticipate must not be flattened into a generic
	// sentence - the server's own words are more useful than ours.
	it("falls back to what the server actually said", () => {
		const failure = saveFailureFrom({ code: "SOMETHING_NEW", message: "the daemon is on fire" });

		expect(failure.detail).toMatch(/the daemon is on fire/);
	});
});
