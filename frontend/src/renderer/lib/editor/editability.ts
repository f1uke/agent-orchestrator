import type { components } from "../../../api/schema";

type WorkspaceFile = components["schemas"]["WorkspaceFileResponse"];

/**
 * Whether this file can be edited, and if not, what to say instead.
 *
 * 🗝 One function, decided in ONE place, and the order deliberately mirrors the
 * daemon's own check order. Every state here corresponds to a refusal the write
 * route would answer with, and the point of deciding it client-side is that the
 * reader never meets that refusal: a control that always fails is worse than no
 * control, so the Save button is not rendered at all when this says read-only.
 */
export type Editability =
	| { editable: true }
	| {
			editable: false;
			/** A short header chip, in the same register as the "truncated" label. */
			chip: string;
			/** One line under the header saying what the reader can do about it. */
			detail: string;
	  };

export function editabilityOf(file: WorkspaceFile | undefined, path: string): Editability {
	if (!file) return { editable: false, chip: "read-only", detail: "This file hasn’t loaded yet." };

	// A path the daemon reports as absolute is one it resolved OUTSIDE the
	// session's workspace — the read route is intentionally unconfined for those
	// (#132) and the write route is intentionally not. Never re-derive this from
	// anything but the path the server handed back.
	if (path.startsWith("/") || path.startsWith("~")) {
		return {
			editable: false,
			chip: "read-only · outside this workspace",
			detail:
				"The editor can open a file anywhere on disk, but it only saves inside this session’s workspace. " +
				"Writing anywhere on disk through the daemon would be an escalation, not a viewer convenience.",
		};
	}

	if (!file.available) {
		const reason = file.reason ?? "";
		return {
			editable: false,
			chip: "read-only",
			detail:
				reason === "binary"
					? "This looks like a binary file, so it can’t be edited here."
					: reason === "too_large"
						? "This file is larger than the editor can load, so it can’t be edited here."
						: "This file can’t be displayed, so it can’t be edited.",
		};
	}

	if (file.truncated) {
		return {
			editable: false,
			chip: "read-only · truncated",
			detail:
				"Only the first 2000 lines of this file were loaded. Saving what’s on screen would delete everything " +
				"after them, so this file stays read-only until the whole of it can be loaded.",
		};
	}

	if (!file.contentHash) {
		// The route has no way to spell "write regardless", so a file the read
		// handed out no hash for is a file this client cannot write. Saying so is
		// better than a Save button that 400s.
		return {
			editable: false,
			chip: "read-only",
			detail: "This file was read without a content hash, so there is nothing to safely write against.",
		};
	}

	return { editable: true };
}
