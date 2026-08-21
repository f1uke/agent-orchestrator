import { useQueries } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { mockSmokeChecks } from "../lib/mock-data";
import { type Task, type TaskGates, reviewGateState } from "../lib/crew";
import { progressFor } from "../lib/smoke-test";
import { fetchSessionScmSummary, sessionScmSummaryQueryKey, type SessionPRSummary } from "./useSessionScmSummary";
import { sessionSmokeQueryKey, type SmokeChecksResponse } from "./useSessionSmokeChecks";

const usePreviewData = import.meta.env.VITE_NO_ELECTRON === "1";

/**
 * The two facts the lane rollup needs beyond the sessions themselves: has a
 * PERSON played the smoke checklist, and has AO's reviewer objected at head.
 *
 * It is asked ONLY for crew tasks. A solo task's lane is `attentionZone` and
 * nothing else, exactly as it is today, so an ordinary board issues not one extra
 * request because of this file - which is the point: turning the crew on must
 * cost a board with no crews on it nothing at all.
 *
 * Both queries reuse the keys the Tests tab and the Summary strip already use, so
 * a card and the tab it opens read one cache rather than racing two.
 */
export function useTaskGates(tasks: Task[]): Map<string, TaskGates> {
	const crews = tasks.filter((task) => task.isCrew);

	const smoke = useQueries({
		queries: crews.map((task) => ({
			queryKey: sessionSmokeQueryKey(task.dev.id),
			queryFn: async (): Promise<SmokeChecksResponse> => {
				if (usePreviewData) return mockSmokeChecks(task.dev.id, task.dev.title);
				const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/smoke-checks", {
					params: { path: { sessionId: task.dev.id } },
				});
				if (error) throw new Error(apiErrorMessage(error, "Unable to load smoke checks"));
				return data ?? ({ worker: "", checks: [] } satisfies SmokeChecksResponse);
			},
			retry: 1,
		})),
	});

	const scm = useQueries({
		queries: crews.map((task) => ({
			queryKey: sessionScmSummaryQueryKey(task.dev.id),
			queryFn: (): Promise<SessionPRSummary[]> => fetchSessionScmSummary(task.dev.id),
			retry: 1,
		})),
	});

	const out = new Map<string, TaskGates>();
	crews.forEach((task, index) => {
		const checks = smoke[index]?.data?.checks;
		out.set(task.dev.id, {
			// undefined until it has actually loaded: "no checklist" and "not yet
			// known" are different answers, and only one of them may open a gate.
			smoke: checks ? progressFor(checks) : undefined,
			review: reviewGateState(scm[index]?.data ?? []),
		});
	});
	return out;
}
