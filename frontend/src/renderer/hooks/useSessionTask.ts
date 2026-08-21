import { useMemo } from "react";
import { type Task, tasksFrom } from "../lib/crew";
import { useWorkspaceQuery } from "./useWorkspaceQuery";

/**
 * The TASK a routed session belongs to — its dev, its qa if it has one, in
 * order.
 *
 * The session view is addressed by SESSION (the route names one), but almost
 * everything it draws around the terminal is a fact about the TASK: one
 * worktree, one branch, one pull request, one checklist. This is the bridge
 * between the two, and it answers a solo session with a task of one — the same
 * identity `CrewDevOf` relies on in the daemon — so every caller can be written
 * once and be correct for both.
 */
export function useSessionTask(sessionId?: string): Task | undefined {
	const workspaces = useWorkspaceQuery().data;
	return useMemo(() => {
		if (!sessionId || !workspaces) return undefined;
		const workspace = workspaces.find((w) => w.sessions.some((session) => session.id === sessionId));
		if (!workspace) return undefined;
		// Grouped over the WHOLE project rather than a filtered list: a qa whose
		// dev is hidden by a board filter is still that dev's member, and a task
		// that lost half of itself to a filter would draw a switcher with one seat.
		return tasksFrom(workspace.sessions).find((task) => task.members.some((member) => member.id === sessionId));
	}, [sessionId, workspaces]);
}
