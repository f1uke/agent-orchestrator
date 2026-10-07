import { FileCode2 } from "lucide-react";
import { CHIP_WITH_ACTION } from "../lib/chip-with-action";
import { showsUnpublishedScripts, unpublishedScriptsTitle } from "../lib/scripts-store";
import { cn } from "../lib/utils";
import type { WorkspaceSession } from "../types/workspace";

/**
 * The board-card / sidebar badge for scripts this task wrote that have not
 * reached the mobile scripts store yet: files nobody committed in its store
 * worktree, commits nobody published, or a worktree a teardown kept because
 * publishing it failed.
 *
 * It sits beside the undelivered chip and looks like it, because it is the same
 * kind of fact about a different checkout: work that exists only here. It has
 * no button. Publishing is the agent's job (`ao scripts publish`), and the
 * tooltip names the files and that command.
 */
export function UnpublishedScriptsChip({ session, compact = false }: { session: WorkspaceSession; compact?: boolean }) {
	const store = session.scriptsStore;
	if (!store || !showsUnpublishedScripts(store, session.status)) return null;
	const label = store.heldReason ? "Scripts held" : "Unpublished scripts";
	const title = unpublishedScriptsTitle(store);

	if (compact) {
		return (
			<span className="inline-flex shrink-0" title={title}>
				<FileCode2 aria-label={label} className="h-3 w-3 text-warning" strokeWidth={2} />
			</span>
		);
	}
	return (
		<span
			aria-label={`${label}: work the scripts store does not have yet`}
			className={cn(CHIP_WITH_ACTION, "pr-1.5 border-[color-mix(in_srgb,var(--amber)_45%,transparent)]")}
			title={title}
			data-unpublished-scripts=""
		>
			<span className="inline-flex items-center gap-1">
				<FileCode2 className="h-3 w-3 text-warning" strokeWidth={2} />
				<span className="text-passive">{label}</span>
			</span>
		</span>
	);
}
