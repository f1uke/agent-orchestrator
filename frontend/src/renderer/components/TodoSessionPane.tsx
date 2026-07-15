import { useNavigate } from "@tanstack/react-router";
import { TodoSpecEditor } from "./TodoSpecEditor";
import type { WorkspaceSession } from "../types/workspace";

type TodoSessionPaneProps = {
	/** A not-started TODO session (`isTodo`) opened from the sidebar. */
	session: WorkspaceSession;
};

/**
 * The center pane of the session-detail route when the opened session is a
 * not-started TODO. It renders the same editable WORKER SPEC as the Board's
 * {@link TodoDetailDialog} — via the shared {@link TodoSpecEditor} — but framed
 * as a first-class page instead of a modal, so the sidebar route is no longer a
 * dead terminal "Preparing…" spinner. On Start the session materializes in place
 * (id unchanged); the workspace refetch flips `isTodo` off and the parent
 * {@link SessionView} swaps in the live terminal automatically, so this pane just
 * lets that happen. On Delete the session is gone, so navigate back to the board.
 */
export function TodoSessionPane({ session }: TodoSessionPaneProps) {
	const navigate = useNavigate();
	const projectId = session.workspaceId;

	return (
		<div className="flex h-full min-h-0 flex-col bg-background">
			<div className="mx-auto flex min-h-0 w-full max-w-[680px] flex-1 flex-col p-4 sm:p-6">
				<div
					className="flex min-h-0 flex-1 flex-col overflow-hidden rounded-lg border border-border bg-popover text-popover-foreground shadow-sm"
					// Grey top accent, mirroring the Board TODO modal's identity.
					style={{ borderTopColor: "var(--lane-todo)", borderTopWidth: 3 }}
				>
					<TodoSpecEditor
						session={session}
						onDeleted={() => void navigate({ to: "/projects/$projectId", params: { projectId } })}
					/>
				</div>
			</div>
		</div>
	);
}
