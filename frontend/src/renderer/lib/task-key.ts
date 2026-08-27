import type { WorkspaceSession } from "../types/workspace";

/**
 * The identity everything TASK-scoped is remembered under: a crew's id, or a
 * solo session's own id.
 *
 * 🗝 ONE definition, on purpose. dev and qa share a single worktree, so they see
 * a byte-for-byte identical file tree; keying anything about that tree by
 * session id would let the same folders be open for one member and shut for the
 * other, over the same files. It is also the key `SessionView` already resets
 * the rail's selected tab by (#242: the daemon answers a task's surfaces for the
 * TASK), so the rail's memories agree with each other rather than disagreeing by
 * one member.
 *
 * The bug this exists to not repeat: `useSessionSmokeChecks` keys by session id
 * while `useTaskGates` keys by `task.dev.id`, so a value written from one member
 * is invisible to the other. Anything task-scoped calls THIS — never its own
 * spelling of the same idea.
 */
export function taskKeyOf(session: Pick<WorkspaceSession, "id" | "crew">): string {
	return session.crew?.id ?? session.id;
}
