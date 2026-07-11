import * as Dialog from "@radix-ui/react-dialog";
import { useEffect, useState } from "react";
import { ArrowRight } from "lucide-react";
import { useJiraTransitions, useMoveJiraStatus, type JiraIssue } from "../hooks/useSessionJiraContext";
import { cn } from "../lib/utils";

/**
 * The Move-status dialog — the ONLY Jira write AO makes (a status transition,
 * user-initiated + confirmed; no comment, no field edit). Transitions are read
 * LIVE from the issue (never hardcoded — they differ per issue type and current
 * status). On confirm it POSTs the chosen transition and, on success, the mutation
 * invalidates the Jira context so the pill reflects the new status. Mockup 05.
 */
export function JiraMoveStatusDialog({
	sessionId,
	issue,
	open,
	onOpenChange,
}: {
	sessionId: string;
	issue: JiraIssue;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}) {
	const transitions = useJiraTransitions(sessionId, open);
	const move = useMoveJiraStatus(sessionId);
	const [selected, setSelected] = useState<string | null>(null);

	// Reset the choice and any prior error each time the dialog opens.
	useEffect(() => {
		if (open) {
			setSelected(null);
			move.reset();
		}
		// move.reset is stable; depend only on `open` so re-renders don't clear state.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open]);

	const options = transitions.data ?? [];
	const chosen = options.find((t) => t.id === selected) ?? null;
	const pending = move.isPending;

	const confirm = () => {
		if (!chosen || pending) return;
		move.mutate(chosen.id, { onSuccess: () => onOpenChange(false) });
	};

	return (
		<Dialog.Root open={open} onOpenChange={(next) => !pending && onOpenChange(next)}>
			<Dialog.Portal>
				<Dialog.Overlay className="jira-move__overlay" />
				<Dialog.Content className="jira-move__dialog" aria-label="Move Jira status">
					<Dialog.Title className="jira-move__title">◈ Move Jira status</Dialog.Title>
					<Dialog.Description className="jira-move__sub">
						The <b>only</b> write AO makes — no comment, no field edit. Pick a transition and confirm.
					</Dialog.Description>

					<div className="jira-move__target">
						Issue <span className="jira-move__key">{issue.key}</span>
						{[issue.type, issue.title].filter(Boolean).length > 0 ? (
							<span className="jira-move__target-meta">{[issue.type, issue.title].filter(Boolean).join(" · ")}</span>
						) : null}
					</div>

					<div className="jira-move__flow">
						<span className="jira-move__chip jira-move__chip--cur">{issue.status || "current"}</span>
						<ArrowRight className="jira-move__arrow" aria-hidden="true" />
						<span className="jira-move__chip jira-move__chip--next">{chosen ? chosen.to || chosen.name : "…"}</span>
					</div>

					<p className="jira-move__label">
						Available transitions <span className="jira-move__live">◈ fetched live</span>
					</p>

					{transitions.isLoading ? (
						<p className="jira-move__note">Loading transitions…</p>
					) : transitions.isError ? (
						<p className="jira-move__error" role="alert">
							{errorText(transitions.error)}
						</p>
					) : options.length === 0 ? (
						<p className="jira-move__note">No transitions are available for this issue right now.</p>
					) : (
						<div role="radiogroup" aria-label="Available transitions">
							{options.map((t) => (
								<button
									key={t.id}
									type="button"
									role="radio"
									aria-checked={selected === t.id}
									disabled={pending}
									className={cn("jira-move__opt", selected === t.id && "jira-move__opt--on")}
									onClick={() => setSelected(t.id)}
								>
									<span className="jira-move__radio" aria-hidden="true" />
									<span className="jira-move__opt-body">
										<span className="jira-move__opt-name">{t.name}</span>
										{t.to ? <span className="jira-move__opt-desc">→ {t.to}</span> : null}
									</span>
								</button>
							))}
						</div>
					)}

					<p className="jira-move__warn">
						◈ Transitions are read from the issue live — never hardcoded (they differ per issue type &amp; current
						status). If Jira rejects the move (permissions / validators), AO surfaces the error.
					</p>

					{move.isError ? (
						<p className="jira-move__error" role="alert">
							{errorText(move.error)}
						</p>
					) : null}

					<div className="jira-move__foot">
						<button type="button" className="jira-move__send" disabled={!chosen || pending} onClick={confirm}>
							{pending ? "Moving…" : "◈ Move status"}
						</button>
						<Dialog.Close asChild>
							<button type="button" className="jira-move__cancel" disabled={pending}>
								Cancel
							</button>
						</Dialog.Close>
						{chosen ? (
							<span className="jira-move__audit">
								Moves {issue.key}: {issue.status || "current"} → {chosen.to || chosen.name}
							</span>
						) : null}
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}

function errorText(e: unknown): string {
	return e instanceof Error ? e.message : "Something went wrong.";
}
