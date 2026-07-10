import * as Dialog from "@radix-ui/react-dialog";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Loader2, Play, RotateCcw, Trash2, X } from "lucide-react";
import { useEffect, useId, useMemo, useState } from "react";
import { Button } from "./ui/button";
import { Input } from "./ui/input";
import { BranchCombobox } from "./BranchCombobox";
import { RequiredAgentField } from "./CreateProjectAgentSheet";
import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { agentsQueryOptions } from "../hooks/useAgentsQuery";
import { useProjectBranches } from "../hooks/useProjectBranches";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import type { AgentProvider, WorkspaceSession } from "../types/workspace";

type TodoDetailDialogProps = {
	/** The TODO session to inspect/edit, or null when the dialog is closed. */
	session: WorkspaceSession | null;
	onOpenChange: (open: boolean) => void;
	/** Called after a successful Start with the now-live session id. */
	onStarted: (sessionId: string) => void;
};

/** Local editable copy of a TODO's spec. */
type Draft = {
	name: string;
	baseBranch: string;
	branch: string;
	prTarget: string;
	agent: string;
	prompt: string;
};

function draftFromSession(s: WorkspaceSession): Draft {
	return {
		name: s.title ?? "",
		baseBranch: s.baseBranch ?? "",
		branch: s.autoNameBranch ? "" : (s.branch ?? ""),
		prTarget: s.prTarget ?? "",
		agent: s.provider ?? "",
		prompt: s.prompt ?? "",
	};
}

function formatQueuedAt(iso?: string): string {
	if (!iso) return "just now";
	const then = new Date(iso).getTime();
	if (Number.isNaN(then)) return "just now";
	const secs = Math.max(0, Math.round((Date.now() - then) / 1000));
	if (secs < 60) return "just now";
	const mins = Math.round(secs / 60);
	if (mins < 60) return `${mins}m ago`;
	const hrs = Math.round(mins / 60);
	if (hrs < 24) return `${hrs}h ago`;
	return `${Math.round(hrs / 24)}d ago`;
}

export function TodoDetailDialog({ session, onOpenChange, onStarted }: TodoDetailDialogProps) {
	const open = session !== null;
	const queryClient = useQueryClient();
	const baseId = useId();
	const branchId = useId();
	const prTargetId = useId();
	const agentId = useId();
	const promptId = useId();

	const projectId = session?.workspaceId;
	const sessionId = session?.id;

	const [draft, setDraft] = useState<Draft | null>(null);
	const [confirmDelete, setConfirmDelete] = useState(false);
	const [error, setError] = useState<string | undefined>();

	// Seed the draft when a (different) TODO opens; a background refetch must not
	// clobber in-progress edits, so this keys on the session id, not the object.
	useEffect(() => {
		if (session) {
			setDraft(draftFromSession(session));
			setConfirmDelete(false);
			setError(undefined);
		} else {
			setDraft(null);
		}
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [sessionId]);

	const agentsQuery = useQuery({ ...agentsQueryOptions, enabled: open });
	const { branches } = useProjectBranches(open ? projectId : undefined);
	const agentCatalog = agentsQuery.data;

	const dirty = useMemo(() => {
		if (!session || !draft) return false;
		const base = draftFromSession(session);
		return (Object.keys(base) as (keyof Draft)[]).some((k) => base[k] !== draft[k]);
	}, [session, draft]);

	const patchBody = (d: Draft): components["schemas"]["UpdateTodoSpecRequest"] => ({
		displayName: d.name.slice(0, 20),
		baseBranch: d.baseBranch,
		branch: d.branch,
		prTarget: d.prTarget,
		harness: (d.agent || undefined) as AgentProvider | undefined,
		prompt: d.prompt,
		autoNameBranch: d.branch.trim() === "",
	});

	// Autosave a field commit (blur / combobox select) so edits survive a
	// close/reopen even before Start.
	const saveSpec = useMutation({
		mutationFn: async (d: Draft) => {
			if (!sessionId) return;
			const { error: apiError } = await apiClient.PATCH("/api/v1/sessions/{sessionId}/spec", {
				params: { path: { sessionId } },
				body: patchBody(d),
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Could not save changes"));
		},
		onSuccess: () => void queryClient.invalidateQueries({ queryKey: workspaceQueryKey }),
		onError: (e) => setError(e instanceof Error ? e.message : "Could not save changes"),
	});

	const commit = () => {
		if (draft && dirty) saveSpec.mutate(draft);
	};

	const start = useMutation({
		mutationFn: async () => {
			if (!sessionId || !draft) throw new Error("No task selected");
			// Persist the current draft first so the started session uses the edits.
			if (dirty) {
				const { error: patchErr } = await apiClient.PATCH("/api/v1/sessions/{sessionId}/spec", {
					params: { path: { sessionId } },
					body: patchBody(draft),
				});
				if (patchErr) throw new Error(apiErrorMessage(patchErr, "Could not save changes"));
			}
			const { error: apiError } = await apiClient.POST("/api/v1/sessions/{sessionId}/start", {
				params: { path: { sessionId } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Could not start task"));
			return sessionId;
		},
		onSuccess: async (id) => {
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			onStarted(id);
			onOpenChange(false);
		},
		onError: (e) => setError(e instanceof Error ? e.message : "Could not start task"),
	});

	const remove = useMutation({
		mutationFn: async () => {
			if (!sessionId) throw new Error("No task selected");
			const { error: apiError } = await apiClient.DELETE("/api/v1/sessions/{sessionId}", {
				params: { path: { sessionId }, query: { force: true } },
			});
			if (apiError) throw new Error(apiErrorMessage(apiError, "Could not delete task"));
		},
		onSuccess: async () => {
			await queryClient.invalidateQueries({ queryKey: workspaceQueryKey });
			onOpenChange(false);
		},
		onError: (e) => setError(e instanceof Error ? e.message : "Could not delete task"),
	});

	const busy = start.isPending || remove.isPending;

	if (!session || !draft) return null;

	const setField = (k: keyof Draft, v: string) => setDraft((d) => (d ? { ...d, [k]: v } : d));

	return (
		<Dialog.Root open={open} onOpenChange={(next) => !next && onOpenChange(false)}>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-50 bg-black/55 data-[state=open]:animate-overlay-in" />
				<Dialog.Content
					aria-describedby={undefined}
					// Grey top accent marks the TODO detail modal (vs the create modal's blue).
					style={{ borderTopColor: "var(--lane-todo)", borderTopWidth: 3 }}
					className="fixed left-1/2 top-1/2 z-50 flex max-h-[calc(100vh-32px)] w-[min(600px,calc(100vw-32px))] -translate-x-1/2 -translate-y-1/2 flex-col rounded-lg border border-border bg-popover p-0 text-popover-foreground shadow-xl data-[state=open]:animate-modal-in"
				>
					{/* header */}
					<div className="border-b border-border px-5 pb-4 pt-4">
						<div className="mb-3 flex items-center gap-2.5">
							<span
								className="inline-flex items-center gap-1.5 text-[10.5px] font-bold uppercase tracking-[0.09em]"
								style={{ color: "var(--lane-todo-bright)" }}
							>
								<span
									className="size-2 rounded-full border-[1.5px]"
									style={{ borderColor: "var(--lane-todo-bright)" }}
								/>
								TODO · not started
							</span>
							<span className="font-mono text-[10.5px] text-passive">{session.id}</span>
							<Dialog.Close asChild>
								<button
									type="button"
									className="ml-auto grid size-7 place-items-center rounded-md text-muted-foreground transition hover:bg-surface hover:text-foreground"
									aria-label="Close task detail"
								>
									<X className="size-4" aria-hidden="true" />
								</button>
							</Dialog.Close>
						</div>
						<Dialog.Title asChild>
							<input
								aria-label="Task name"
								maxLength={20}
								spellCheck={false}
								value={draft.name}
								onChange={(e) => setField("name", e.target.value)}
								onBlur={commit}
								className="w-full border-b border-transparent bg-transparent pb-0.5 text-[20px] font-bold tracking-[-0.01em] text-foreground outline-none focus:border-border"
							/>
						</Dialog.Title>
						<div className="mt-2 flex items-center gap-2 text-[12px] text-muted-foreground">
							<span>
								Queued {formatQueuedAt(session.createdAt)}
								{session.createdBy ? " by " : ""}
							</span>
							{session.createdBy ? <span className="font-mono text-accent">{session.createdBy}</span> : null}
							<span className="ml-auto text-[11px] text-passive">name · max 20 chars</span>
						</div>
					</div>

					{/* body */}
					<div className="min-h-0 flex-1 space-y-4 overflow-y-auto px-5 py-4">
						<div className="flex items-center gap-2">
							<span className="font-mono text-[10.5px] font-semibold uppercase tracking-[0.08em] text-passive">
								Worker spec
							</span>
							<span className="text-[10px] text-passive">— editable before Start</span>
							{dirty ? (
								<button
									type="button"
									onClick={() => setDraft(draftFromSession(session))}
									className="ml-auto inline-flex items-center gap-1 text-[11px] font-medium text-muted-foreground hover:text-foreground"
								>
									<RotateCcw className="size-3" aria-hidden="true" />
									Reset
								</button>
							) : null}
						</div>

						<div className="grid grid-cols-[92px_1fr] items-center gap-x-4 gap-y-2.5">
							<label className="text-[12.5px] text-muted-foreground">project</label>
							<Input value={session.workspaceName} readOnly disabled className="font-mono" />

							<label className="text-[12.5px] text-muted-foreground" htmlFor={baseId}>
								base branch
							</label>
							<BranchCombobox
								id={baseId}
								branches={branches}
								value={draft.baseBranch}
								onChange={(v) => {
									setDraft((d) => {
										const next = d ? { ...d, baseBranch: v } : d;
										if (next) saveSpec.mutate(next);
										return next;
									});
								}}
							/>

							<label className="text-[12.5px] text-muted-foreground" htmlFor={branchId}>
								new branch
							</label>
							<Input
								id={branchId}
								placeholder="auto-named from task on Start"
								value={draft.branch}
								onChange={(e) => setField("branch", e.target.value)}
								onBlur={commit}
							/>

							<label className="text-[12.5px] text-muted-foreground" htmlFor={prTargetId}>
								PR target
							</label>
							<BranchCombobox
								id={prTargetId}
								branches={branches}
								value={draft.prTarget}
								onChange={(v) => {
									setDraft((d) => {
										const next = d ? { ...d, prTarget: v } : d;
										if (next) saveSpec.mutate(next);
										return next;
									});
								}}
							/>

							<label className="text-[12.5px] text-muted-foreground" htmlFor={agentId}>
								agent
							</label>
							<div>
								<RequiredAgentField
									id={agentId}
									label=""
									placeholder="Project default"
									value={draft.agent}
									authorized={agentCatalog?.authorized}
									installed={agentCatalog?.installed}
									supported={agentCatalog?.supported}
									onChange={(v) => {
										setDraft((d) => {
											const next = d ? { ...d, agent: v } : d;
											if (next) saveSpec.mutate(next);
											return next;
										});
									}}
								/>
							</div>
						</div>

						<div className="flex items-center gap-2">
							<span className="font-mono text-[10.5px] font-semibold uppercase tracking-[0.08em] text-passive">
								Prompt
							</span>
							<span className="text-[10px] text-passive">— freeform; sent verbatim to the worker</span>
						</div>
						<textarea
							id={promptId}
							spellCheck={false}
							value={draft.prompt}
							onChange={(e) => setField("prompt", e.target.value)}
							onBlur={commit}
							className="min-h-[160px] w-full resize-y rounded-md border border-border bg-transparent px-3 py-2 text-[13px] leading-relaxed text-foreground outline-none transition placeholder:text-passive focus-visible:border-accent"
							placeholder="Objective…  Deliverables…  Verify…  Context…  Report back…"
						/>

						{error ? (
							<div className="rounded-md border border-destructive/40 bg-destructive/10 px-3 py-2 text-[12px] text-destructive">
								{error}
							</div>
						) : null}
					</div>

					{/* footer */}
					<div className="border-t border-border bg-surface/40 px-5 py-3.5">
						{confirmDelete ? (
							<div className="flex flex-wrap items-center gap-3">
								<span className="inline-flex items-center gap-2 text-[13px] text-foreground">
									<Trash2 className="size-4" aria-hidden="true" />
									Delete this task? It hasn’t started, so nothing is lost.
								</span>
								<div className="ml-auto flex items-center gap-2">
									<Button type="button" variant="ghost" onClick={() => setConfirmDelete(false)} disabled={busy}>
										Cancel
									</Button>
									<Button
										type="button"
										onClick={() => remove.mutate()}
										disabled={busy}
										className="bg-destructive text-white hover:bg-destructive/90"
									>
										{remove.isPending ? <Loader2 className="size-3.5 animate-spin" aria-hidden="true" /> : null}
										Delete task
									</Button>
								</div>
							</div>
						) : (
							<div className="flex flex-wrap items-center gap-3">
								<Button
									type="button"
									variant="ghost"
									onClick={() => setConfirmDelete(true)}
									disabled={busy}
									className="text-destructive hover:bg-destructive/10 hover:text-destructive"
								>
									<Trash2 className="size-3.5" aria-hidden="true" />
									Delete
								</Button>
								<div className="ml-auto flex items-center gap-2">
									<Dialog.Close asChild>
										<Button type="button" variant="ghost" disabled={busy}>
											Cancel
										</Button>
									</Dialog.Close>
									<Button
										type="button"
										onClick={() => start.mutate()}
										disabled={busy}
										style={{ background: "var(--lane-todo-bright)", color: "#12121a" }}
									>
										{start.isPending ? (
											<Loader2 className="size-3.5 animate-spin" aria-hidden="true" />
										) : (
											<Play className="size-3.5" aria-hidden="true" />
										)}
										Start work
									</Button>
								</div>
							</div>
						)}
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
