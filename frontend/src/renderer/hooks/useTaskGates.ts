import { useQueries } from "@tanstack/react-query";
import { type Task, type TaskGates, reviewGateState } from "../lib/crew";
import { fetchSessionScmSummary, sessionScmSummaryQueryKey, type SessionPRSummary } from "./useSessionScmSummary";

/**
 * The one fact the lane rollup needs beyond the sessions themselves: has AO's
 * reviewer objected at head.
 *
 * It is asked ONLY for crew tasks. A solo task's lane is `attentionZone` and
 * nothing else, exactly as it is today, so an ordinary board issues not one extra
 * request because of this file - which is the point: turning the crew on must
 * cost a board with no crews on it nothing at all.
 *
 * The query reuses the key the Summary strip already uses, so a card and the tab
 * it opens read one cache rather than racing two.
 */
export function useTaskGates(tasks: Task[]): Map<string, TaskGates> {
	const crews = tasks.filter((task) => task.isCrew);

	const scm = useQueries({
		queries: crews.map((task) => ({
			queryKey: sessionScmSummaryQueryKey(task.dev.id),
			queryFn: (): Promise<SessionPRSummary[]> => fetchSessionScmSummary(task.dev.id),
			retry: 1,
		})),
	});

	const out = new Map<string, TaskGates>();
	crews.forEach((task, index) => {
		out.set(task.dev.id, { review: reviewGateState(scm[index]?.data ?? []) });
	});
	return out;
}
