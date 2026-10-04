/**
 * The Memory inbox's data layer: what learning proposes, the drafts and rules a
 * proposal rests on, learning's health, and the person's decisions.
 *
 * Nothing here writes on its own. A proposal reaches a file only through
 * `useDecideProposal`, which the person triggers.
 */

import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { useApiReady } from "./useApiReady";

export type Proposal = components["schemas"]["ControllersLearningProposalDTO"];
export type ProposalList = components["schemas"]["ControllersListLearningProposalsResponse"];
export type ProposalDetail = components["schemas"]["ControllersLearningProposalResponse"];
export type LearningStatus = components["schemas"]["ControllersLearningStatusResponse"];
export type Written = components["schemas"]["ControllersLearningWrittenDTO"];
export type ProposalEvent = components["schemas"]["ControllersLearningProposalEventDTO"];

export const proposalsQueryKey = ["learning", "proposals"] as const;
export const proposalQueryKey = (id: number) => ["learning", "proposal", id] as const;
export const learningStatusQueryKey = ["learning", "status"] as const;

/** How often the open inbox re-reads the queue: decide runs every 30 minutes. */
const PROPOSALS_POLL_MS = 30_000;

/**
 * Every proposal, pending and settled. `poll` is per observer: the page polls,
 * the sidebar's count rides on the same cache entry without polling.
 */
export function useProposals({ poll = true }: { poll?: boolean } = {}) {
	const ready = useApiReady();
	return useQuery({
		queryKey: proposalsQueryKey,
		enabled: ready,
		queryFn: async (): Promise<ProposalList> => {
			const { data, error } = await apiClient.GET("/api/v1/learning/proposals", { params: { query: { all: true } } });
			if (error) throw new Error(apiErrorMessage(error));
			return data as ProposalList;
		},
		refetchInterval: poll ? PROPOSALS_POLL_MS : false,
		retry: 1,
	});
}

/** One proposal with the person's words it rests on and the rules it names. */
export function useProposal(id: number | null) {
	const ready = useApiReady();
	return useQuery({
		queryKey: proposalQueryKey(id ?? 0),
		enabled: ready && id !== null,
		queryFn: async (): Promise<ProposalDetail> => {
			const { data, error } = await apiClient.GET("/api/v1/learning/proposals/{id}", {
				params: { path: { id: id ?? 0 } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			return data as ProposalDetail;
		},
		retry: 1,
	});
}

/** Learning's per-project health: whether any project learns at all, and today's spend. */
export function useLearningStatus() {
	const ready = useApiReady();
	return useQuery({
		queryKey: learningStatusQueryKey,
		enabled: ready,
		queryFn: async (): Promise<LearningStatus> => {
			const { data, error } = await apiClient.GET("/api/v1/learning/status", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as LearningStatus;
		},
		staleTime: 60_000,
		retry: 1,
	});
}

/** A pending proposal is waiting when it is not snoozed into the future. */
export function isWaiting(p: Proposal, now = Date.now()): boolean {
	return p.status === "pending" && !(p.snoozedUntil && Date.parse(p.snoozedUntil) > now);
}

export type Decision =
	| { kind: "approve"; content?: string; resolution?: "keep_rule" | "words_win" | "both" }
	| { kind: "reject"; reason: string }
	| { kind: "snooze"; until: string }
	| { kind: "unsnooze" }
	| { kind: "reopen" }
	| { kind: "undo"; confirmToken?: string }
	| { kind: "edit"; content: string; confirmToken?: string };

/** A refused decision, with the daemon's code (e.g. PROPOSAL_CHANGED). */
export class DecisionError extends Error {
	constructor(
		message: string,
		readonly code?: string,
	) {
		super(message);
	}
}

/** Every decision says it came from the app; the proposal's history keeps that. */
const via = "app" as const;

async function postDecision(id: number, d: Decision) {
	const path = { params: { path: { id } } };
	switch (d.kind) {
		case "approve":
			return apiClient.POST("/api/v1/learning/proposals/{id}/approve", {
				...path,
				body: { content: d.content, resolution: d.resolution, via },
			});
		case "reject":
			return apiClient.POST("/api/v1/learning/proposals/{id}/reject", { ...path, body: { reason: d.reason, via } });
		case "snooze":
			return apiClient.POST("/api/v1/learning/proposals/{id}/snooze", { ...path, body: { until: d.until, via } });
		case "unsnooze":
			return apiClient.POST("/api/v1/learning/proposals/{id}/unsnooze", { ...path, body: { via } });
		case "reopen":
			return apiClient.POST("/api/v1/learning/proposals/{id}/reopen", { ...path, body: { via } });
		case "undo":
			return apiClient.POST("/api/v1/learning/proposals/{id}/undo", {
				...path,
				body: { confirmToken: d.confirmToken, via },
			});
		case "edit":
			return apiClient.POST("/api/v1/learning/proposals/{id}/edit", {
				...path,
				body: { content: d.content, confirmToken: d.confirmToken, via },
			});
	}
}

/**
 * The person's decision on one proposal. The queue's copy of the proposal is
 * replaced with the daemon's answer at once, so it moves to its new tab without
 * waiting for the next read.
 */
export function useDecideProposal() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async ({ id, decision }: { id: number; decision: Decision }): Promise<Proposal> => {
			const res = await postDecision(id, decision);
			if (res.error) {
				const code = (res.error as { code?: string }).code;
				throw new DecisionError(
					code === "PROPOSAL_CHANGED"
						? "It changed again while you were looking. The change shown is up to date now; review it and confirm again."
						: apiErrorMessage(res.error),
					code,
				);
			}
			return res.data as Proposal;
		},
		onSuccess: (after) => {
			queryClient.setQueryData<ProposalList>(proposalsQueryKey, (old) =>
				old ? { ...old, proposals: old.proposals.map((p) => (p.id === after.id ? after : p)) } : old,
			);
		},
		onSettled: (_data, _error, { id }) => {
			void queryClient.invalidateQueries({ queryKey: proposalsQueryKey });
			void queryClient.invalidateQueries({ queryKey: proposalQueryKey(id) });
		},
	});
}

/** Starts a decide run now under a small budget. */
export function useDecideNow() {
	const queryClient = useQueryClient();
	return useMutation({
		mutationFn: async () => {
			const { error } = await apiClient.POST("/api/v1/learning/decide", { body: { budgetUsd: 2 } });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSettled: () => void queryClient.invalidateQueries({ queryKey: proposalsQueryKey }),
	});
}
