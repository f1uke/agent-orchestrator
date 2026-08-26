import { apiErrorMessage } from "../api-client";

/**
 * What went wrong with a save, in words the reader can act on.
 *
 * `conflict` is its own kind because it is the only one the reader RESOLVES
 * rather than reads: an AO worktree has agents writing in it, so a file moving
 * under you is the normal case, not an edge case.
 */
export type SaveFailure = {
	kind: "conflict" | "refused";
	title: string;
	detail: string;
	/** For a conflict: what the server says is on disk now. */
	current?: { hash?: string; size?: number; modifiedAt?: string };
};

type ErrorBody = { code?: unknown; message?: unknown; details?: Record<string, unknown> };

function readBody(error: unknown): ErrorBody {
	return typeof error === "object" && error !== null ? (error as ErrorBody) : {};
}

function str(value: unknown): string | undefined {
	return typeof value === "string" && value !== "" ? value : undefined;
}

function num(value: unknown): number | undefined {
	return typeof value === "number" ? value : undefined;
}

/**
 * Copy for every refusal the write route can answer with.
 *
 * Each one names the thing the reader can do something about, because every one
 * of them is a real state of a real file rather than a transient failure. None
 * of these is a toast: the pane the file is open in is where the explanation
 * belongs.
 */
const NOT_EDITABLE: Record<string, string> = {
	truncated:
		"Only the first 2000 lines of this file were loaded, so saving it would delete everything after them. " +
		"It stays read-only until the whole file can be loaded.",
	binary: "This looks like a binary file, so it can’t be edited here.",
	too_large: "This file is larger than the editor can load, so it can’t be edited here.",
};

const CONTENT_REJECTED: Record<string, string> = {
	too_many_lines:
		"This edit would take the file past 2000 lines, which is more than the viewer can load. " +
		"It would then open truncated, and could never be saved again — so the save is refused rather than " +
		"creating that dead end. Raising that cap is a decision that hasn’t been made yet.",
	too_large: "This edit would take the file past 5 MB, which is more than the viewer can load.",
	binary: "This edit would make the file unreadable as text.",
};

/** Map an API error from `PUT .../workspace/file` to what the reader is shown. */
export function saveFailureFrom(error: unknown): SaveFailure {
	const body = readBody(error);
	const code = str(body.code);
	const details = body.details ?? {};
	const reason = str(details.reason);

	switch (code) {
		case "WORKSPACE_FILE_CONFLICT":
			return {
				kind: "conflict",
				title: "This file changed on disk while you were editing it.",
				detail: "Nothing was written. Compare the two versions and decide what this file should say.",
				current: {
					hash: str(details.currentHash),
					size: num(details.currentSize),
					modifiedAt: str(details.currentModifiedAt),
				},
			};
		case "WORKSPACE_FILE_NOT_EDITABLE":
			return {
				kind: "refused",
				title: "This file can’t be saved.",
				detail: (reason && NOT_EDITABLE[reason]) || "The daemon can’t safely write this file.",
			};
		case "WORKSPACE_FILE_CONTENT_REJECTED":
			return {
				kind: "refused",
				title: "This edit can’t be saved.",
				detail: (reason && CONTENT_REJECTED[reason]) || "The daemon refused this file’s new content.",
			};
		case "WORKSPACE_FILE_PATH_INVALID":
			return {
				kind: "refused",
				title: "This file can’t be saved.",
				detail:
					"The editor can open a file anywhere on disk, but it can only save one inside this session’s " + "workspace.",
			};
		case "WORKSPACE_FILE_NOT_FOUND":
			return {
				kind: "refused",
				title: "This file is no longer there.",
				detail: "It was moved or deleted since you opened it. The route never creates a file it didn’t find.",
			};
		case "WORKSPACE_FILE_BASE_HASH_REQUIRED":
		case "WORKSPACE_FILE_CONTENT_REQUIRED":
			return {
				kind: "refused",
				title: "This file can’t be saved.",
				detail: "The editor sent an incomplete request. Close the file and reopen it, then try again.",
			};
		default:
			// Never swallow an error we did not anticipate: show what the server
			// actually said rather than a generic sentence that hides it.
			return {
				kind: "refused",
				title: "This file couldn’t be saved.",
				detail: apiErrorMessage(error, "The daemon refused the write."),
			};
	}
}
