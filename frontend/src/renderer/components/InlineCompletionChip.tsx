import { Sparkles } from "lucide-react";
import { ACCENT, PALETTE as P } from "../lib/comment-inbox";
import { useInlineCompletionStatus } from "../lib/inline-completion/status";
import {
	InlineCompletionControls,
	InlineCompletionModelPicker,
	inlineCompletionSummary,
} from "./settings/InlineCompletionControls";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";

/**
 * The editor header's handle on predictive completion: one word saying what the
 * model is doing, and a click away from the same switch, model picker and
 * download controls Settings › Code editor has - so turning it on never means
 * leaving the file.
 *
 * Absent where the feature cannot exist (the browser preview has no main
 * process): a chip that can only ever say "unavailable" is noise on every file.
 */
export function InlineCompletionChip() {
	const status = useInlineCompletionStatus();
	if (!status || status.unsupported) return null;

	// An ICON, deliberately, whatever the state: the header's Save button must
	// never be pushed out (see COMPACT_WIDTH), and the colour already says the one
	// thing a glance needs - accent when predicting, red when it stopped, muted
	// otherwise. The words live in the tooltip and one click away.
	const summary = inlineCompletionSummary(status);
	const nextEdit = status.models.find((m) => m.id === status.modelId)?.kind === "next-edit";
	const tone = status.server === "error" ? P.red : status.enabled && status.server === "ready" ? ACCENT : P.muted2;
	const title =
		status.server === "ready"
			? nextEdit
				? "Predictive code completion is on: your next edit, shown where it would land - Tab to jump there and accept. Click to change."
				: "Predictive code completion is on: grey text after the cursor, Tab to accept. Click to change."
			: status.server === "error"
				? `Predictive code completion stopped: ${status.serverDetail ?? "llama-server failed"}`
				: `Predictive code completion: ${summary}. Click to change.`;

	return (
		<Popover>
			<PopoverTrigger asChild>
				<button
					type="button"
					data-testid="inline-completion-chip"
					data-state-server={status.server}
					title={title}
					aria-label={`Predictive code completion: ${summary}`}
					style={{
						flex: "none",
						display: "inline-flex",
						alignItems: "center",
						color: tone,
						background: "transparent",
						border: "none",
						padding: 0,
						cursor: "pointer",
					}}
				>
					<Sparkles aria-hidden="true" style={{ width: 14, height: 14 }} />
				</button>
			</PopoverTrigger>
			<PopoverContent align="end" className="w-[340px] p-3.5">
				<div className="flex flex-col gap-3.5">
					<div className="flex flex-col gap-1">
						<span className="text-[13px] font-medium text-foreground">Predictive code completion</span>
						<span className="text-[11.5px] leading-[1.5] text-muted-foreground">
							{nextEdit
								? "A code model on this Mac suggests the edit you are likely to make next, from what you just changed. Tab jumps to it and accepts it."
								: "A code model on this Mac suggests the rest of what you are typing. Tab accepts."}
						</span>
					</div>
					<InlineCompletionControls id="inlineCompletionEnabledEditor" />
					<InlineCompletionModelPicker id="inlineCompletionModelEditor" />
				</div>
			</PopoverContent>
		</Popover>
	);
}
