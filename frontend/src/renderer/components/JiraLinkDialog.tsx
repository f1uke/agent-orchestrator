import * as Dialog from "@radix-ui/react-dialog";
import { useEffect, useState } from "react";
import { X } from "lucide-react";
import { JiraIssuePicker } from "./JiraIssuePicker";
import { useSetJiraBinding, type JiraIssueSummary } from "../hooks/useSessionJiraContext";

/**
 * Links an EXISTING session to a Jira issue after the fact — the "I created this
 * session before Jira, now tie it to DEMO-2272" flow. Reuses the same live search
 * picker as the New-task modal; on confirm it PUTs the binding and the session's
 * Summary / board badge / Move-status all light up. Unlink lives on the linked
 * section itself.
 */
export function JiraLinkDialog({
	sessionId,
	open,
	onOpenChange,
}: {
	sessionId: string;
	open: boolean;
	onOpenChange: (open: boolean) => void;
}) {
	const [query, setQuery] = useState("");
	const [picked, setPicked] = useState<JiraIssueSummary | null>(null);
	const bind = useSetJiraBinding(sessionId);
	const pending = bind.isPending;

	useEffect(() => {
		if (open) {
			setQuery("");
			setPicked(null);
			bind.reset();
		}
		// bind.reset is stable; depend only on `open`.
		// eslint-disable-next-line react-hooks/exhaustive-deps
	}, [open]);

	const confirm = () => {
		if (!picked?.key || pending) return;
		bind.mutate(picked.key, { onSuccess: () => onOpenChange(false) });
	};

	return (
		<Dialog.Root open={open} onOpenChange={(next) => !pending && onOpenChange(next)}>
			<Dialog.Portal>
				<Dialog.Overlay className="jira-move__overlay" />
				<Dialog.Content className="jira-move__dialog" aria-label="Link a Jira issue">
					<Dialog.Title className="jira-move__title">◈ Link a Jira issue</Dialog.Title>
					<Dialog.Description className="jira-move__sub">
						Search across all your projects and bind this session to an issue — its context, board badge, and
						Move-status light up once linked.
					</Dialog.Description>

					{picked ? (
						<div className="jira-linked-chip jira-linked-chip--dialog">
							<span className="jira-linked-chip__key">{picked.key}</span>
							<span className="jira-linked-chip__title">{picked.title}</span>
							{picked.status ? <span className="jira-linked-chip__status">{picked.status}</span> : null}
							<button
								type="button"
								className="jira-linked-chip__clear"
								aria-label="Clear selected issue"
								disabled={pending}
								onClick={() => setPicked(null)}
							>
								<X className="size-3.5" aria-hidden="true" />
							</button>
						</div>
					) : (
						<JiraIssuePicker
							query={query}
							onQueryChange={setQuery}
							enabled={open}
							autoFocus
							placeholder="Search Jira (e.g. DEMO-2272 or a keyword)"
							onPick={(issue) => {
								setPicked(issue);
								setQuery("");
							}}
						/>
					)}

					{bind.isError ? (
						<p className="jira-move__error" role="alert">
							{bind.error instanceof Error ? bind.error.message : "Couldn't link the issue."}
						</p>
					) : null}

					<div className="jira-move__foot">
						<button type="button" className="jira-move__send" disabled={!picked || pending} onClick={confirm}>
							{pending ? "Linking…" : "◈ Link issue"}
						</button>
						<Dialog.Close asChild>
							<button type="button" className="jira-move__cancel" disabled={pending}>
								Cancel
							</button>
						</Dialog.Close>
					</div>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
