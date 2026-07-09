import { useMutation } from "@tanstack/react-query";
import { ChevronDown, Loader2 } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { Button } from "./ui/button";
import { Textarea } from "./ui/textarea";

const SENT_STATE_MS = 2000;

/**
 * Split button on a review thread: the primary half sends the thread to the
 * session's worker as-is; the caret half opens a small panel to attach free
 * text first. The panel is a plain controlled `useState` toggle rather than
 * the shared `DropdownMenu` — Radix's menu type-ahead intercepts keystrokes
 * meant for the textarea inside it, so a real menu fights a form control
 * (Radix's own guidance is to reach for a non-menu overlay when the content
 * includes inputs).
 */
export function SendToWorkerButton({
	sessionId,
	prUrl,
	threadId,
}: {
	sessionId: string;
	prUrl: string;
	threadId: string;
}) {
	const [panelOpen, setPanelOpen] = useState(false);
	const [extraPrompt, setExtraPrompt] = useState("");
	const [sent, setSent] = useState(false);
	const sentTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

	useEffect(
		() => () => {
			if (sentTimer.current) clearTimeout(sentTimer.current);
		},
		[],
	);

	const dispatch = useMutation({
		mutationFn: async (nextExtraPrompt: string) => {
			const { data, error } = await apiClient.POST("/api/v1/sessions/{sessionId}/comment-dispatch", {
				params: { path: { sessionId } },
				body: { prUrl, threadId, extraPrompt: nextExtraPrompt },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to send"));
			return data;
		},
		onSuccess: () => {
			setPanelOpen(false);
			setExtraPrompt("");
			setSent(true);
			if (sentTimer.current) clearTimeout(sentTimer.current);
			sentTimer.current = setTimeout(() => setSent(false), SENT_STATE_MS);
		},
	});

	const busy = dispatch.isPending;

	return (
		<div className="relative inline-flex">
			<div className="inline-flex overflow-hidden rounded-md border border-border">
				<Button
					type="button"
					variant="outline"
					size="sm"
					className="rounded-none border-0"
					disabled={busy || sent}
					onClick={() => dispatch.mutate("")}
				>
					{busy && <Loader2 className="h-3.5 w-3.5 animate-spin" aria-hidden="true" />}
					{sent ? "Sent ✓" : "Send to worker"}
				</Button>
				<Button
					type="button"
					variant="outline"
					size="icon-sm"
					aria-label="Add extra instructions"
					className="rounded-none border-0 border-l border-border"
					disabled={busy}
					onClick={() => setPanelOpen((open) => !open)}
				>
					<ChevronDown className="h-3.5 w-3.5" aria-hidden="true" />
				</Button>
			</div>
			{panelOpen && (
				<div className="absolute right-0 top-full z-10 mt-1 w-64 rounded-lg border border-border bg-popover p-2.5 shadow-[var(--shadow)]">
					<label htmlFor={`extra-prompt-${threadId}`} className="mb-1 block text-[11px] text-muted-foreground">
						Extra instructions for the worker (optional)
					</label>
					<Textarea
						id={`extra-prompt-${threadId}`}
						className="min-h-16 text-[12px]"
						placeholder="e.g. also check the other call sites"
						value={extraPrompt}
						onChange={(event) => setExtraPrompt(event.target.value)}
					/>
					{dispatch.isError && (
						<div className="mt-1.5 text-[11px] text-destructive" role="alert">
							{apiErrorMessage(dispatch.error, "Unable to send")}
						</div>
					)}
					<div className="mt-2 flex justify-end">
						<Button type="button" size="sm" disabled={busy} onClick={() => dispatch.mutate(extraPrompt)}>
							Send with instructions
						</Button>
					</div>
				</div>
			)}
		</div>
	);
}
