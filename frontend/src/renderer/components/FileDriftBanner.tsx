import { useState } from "react";
import { RotateCcw } from "lucide-react";
import { PALETTE as P, accentMix, ACCENT } from "../lib/comment-inbox";

/** What the file on disk looks like now, as far as we have been told. */
export type Drift = {
	/** The hash to precondition the next save on, once the reader has seen it. */
	hash?: string;
	size?: number;
	modifiedAt?: string;
	/** True once the reader is looking at the comparison. */
	reviewing: boolean;
};

function formatSize(bytes: number | undefined): string | null {
	if (bytes == null) return null;
	if (bytes < 1024) return `${bytes} B`;
	if (bytes < 1024 * 1024) return `${(bytes / 1024).toFixed(1)} KB`;
	return `${(bytes / (1024 * 1024)).toFixed(1)} MB`;
}

function formatAgo(iso: string | undefined, now = Date.now()): string | null {
	if (!iso) return null;
	const at = Date.parse(iso);
	if (Number.isNaN(at)) return null;
	const seconds = Math.max(0, Math.round((now - at) / 1000));
	if (seconds < 60) return `${seconds} second${seconds === 1 ? "" : "s"} ago`;
	const minutes = Math.round(seconds / 60);
	if (minutes < 60) return `${minutes} minute${minutes === 1 ? "" : "s"} ago`;
	const hours = Math.round(minutes / 60);
	return `${hours} hour${hours === 1 ? "" : "s"} ago`;
}

/**
 * "This file changed on disk while you were editing it."
 *
 * 🗝 This is the whole answer to the write route's 409, and it is deliberately
 * NOT an alert with an OK button. An AO worktree has agents writing in it, so a
 * file moving under the reader is the NORMAL case; the banner therefore appears
 * for drift detected while typing as well as for a refused save, and in both
 * cases the buffer is left exactly as the reader left it.
 *
 * The three ways out, and why there is no fourth:
 *
 * - **Review changes** puts the two versions in a diff editor — theirs on the
 *   left, read-only; yours on the right, still editable. Saving from there
 *   preconditions on the hash of the version the reader was just shown. That IS
 *   the overwrite, and it is the only one offered: the route refuses a BLIND
 *   clobber, not an informed one, so the UI's job is to make the reading
 *   actually happen rather than to add a "force" the daemon correctly declines
 *   to implement. It is OPTIONAL: an editor that edits one block at a time
 *   cannot offer it, because the block's byte range no longer means anything
 *   once the file has moved. Such a caller passes no `onReview` and the button
 *   is not drawn, rather than being drawn to do something unsafe.
 * - **Discard mine and reload** loses the reader's edits, says so, and asks
 *   twice — it sits next to a primary button, and a one-click destroy there is
 *   how work gets lost.
 * - **Dismiss** keeps typing. The banner returns on the next drift, and the
 *   save will refuse again until the reader has looked. Nothing is lost by
 *   ignoring it.
 */
export function FileDriftBanner({
	drift,
	onReview,
	onDiscardMine,
	onDismiss,
}: {
	drift: Drift;
	onReview?: () => void;
	onDiscardMine: () => void;
	onDismiss: () => void;
}) {
	const [confirmingDiscard, setConfirmingDiscard] = useState(false);
	const facts = [formatSize(drift.size), formatAgo(drift.modifiedAt)].filter(Boolean).join(" · ");

	return (
		<div
			data-testid="file-drift-banner"
			role="status"
			style={{
				flex: "none",
				display: "flex",
				alignItems: "center",
				gap: 12,
				padding: "10px 20px",
				background: accentMix(9),
				borderBottom: `1px solid ${accentMix(28)}`,
			}}
		>
			<RotateCcw aria-hidden="true" style={{ width: 15, height: 15, color: ACCENT, flex: "none" }} />
			<div style={{ minWidth: 0, flex: 1 }}>
				<p style={{ margin: 0, fontSize: 12.5, fontWeight: 600, color: P.textStrong }}>
					{drift.reviewing
						? "You’re comparing your version with the one on disk."
						: "This file changed on disk while you were editing it."}
				</p>
				<p
					style={{
						margin: "2px 0 0",
						fontSize: 11.5,
						color: P.secondary,
						// One line, always. Wrapping pushed the buttons off the row's
						// vertical centre, and the banner grew as the file aged.
						overflow: "hidden",
						textOverflow: "ellipsis",
						whiteSpace: "nowrap",
					}}
				>
					{drift.reviewing ? "Saving writes your version over the one on the left." : "Nothing was written."}
					{facts !== "" && <span style={{ color: P.muted3 }}> {facts}</span>}
				</p>
			</div>
			{!drift.reviewing && onReview && (
				<button
					type="button"
					onClick={onReview}
					style={{
						flex: "none",
						fontSize: 12,
						fontWeight: 600,
						padding: "5px 12px",
						borderRadius: 7,
						border: `1px solid ${accentMix(45)}`,
						background: accentMix(16),
						color: ACCENT,
						cursor: "pointer",
					}}
				>
					Review changes
				</button>
			)}
			<button
				type="button"
				onClick={() => (confirmingDiscard ? onDiscardMine() : setConfirmingDiscard(true))}
				onBlur={() => setConfirmingDiscard(false)}
				style={{
					flex: "none",
					fontSize: 12,
					padding: "5px 12px",
					borderRadius: 7,
					border: `1px solid color-mix(in oklab, ${P.red} ${confirmingDiscard ? 55 : 30}%, transparent)`,
					background: confirmingDiscard ? `color-mix(in oklab, ${P.red} 14%, transparent)` : "transparent",
					color: P.red,
					cursor: "pointer",
				}}
			>
				{confirmingDiscard ? "Really discard my edits?" : "Discard mine and reload"}
			</button>
			<button
				type="button"
				onClick={onDismiss}
				style={{
					flex: "none",
					fontSize: 12,
					padding: "5px 10px",
					borderRadius: 7,
					border: `1px solid ${P.borderPill}`,
					background: "transparent",
					color: P.secondary,
					cursor: "pointer",
				}}
			>
				{drift.reviewing ? "Back to editing" : "Dismiss"}
			</button>
		</div>
	);
}
