import type { ReactNode } from "react";
import { Check } from "lucide-react";
import { Button } from "../ui/button";

// The single sticky save bar per scope (sanctioned deviation, proposal §9). It
// is the ONLY commit point for a scope: every editable field routes through it
// (locked decision 3). Dirty → amber "Unsaved changes" + Discard/Save. Idle →
// a quiet "All changes saved" reassurance plus an optional note reminding that
// true actions (Refresh agents, Send test, Check for updates, Run migration) run
// immediately and are NOT part of Save. `status` carries transient messages
// (validation / save error / "Saved." / orchestrator-restart warning) so they
// stay visible regardless of scroll position.
export function SettingsSaveBar({
	dirty,
	saving = false,
	hint,
	idleNote,
	status,
	onDiscard,
	onSave,
}: {
	dirty: boolean;
	saving?: boolean;
	hint?: string;
	idleNote?: string;
	status?: ReactNode;
	onDiscard: () => void;
	onSave: () => void;
}) {
	return (
		<div className="flex h-14 items-center gap-3.5 border-t border-border-strong bg-background px-6 shadow-[0_-8px_24px_rgba(0,0,0,0.35)]">
			{dirty ? (
				<>
					<span className="flex shrink-0 items-center gap-2.5 text-[13px] text-foreground">
						<span
							aria-hidden="true"
							className="h-2 w-2 rounded-full bg-warning shadow-[0_0_8px_rgba(232,193,74,0.5)]"
						/>
						Unsaved changes
						{hint && <span className="text-[11.5px] text-passive">· {hint}</span>}
					</span>
					<div className="flex min-w-0 items-center gap-3">{status}</div>
					<div className="ml-auto flex shrink-0 items-center gap-2.5">
						<Button type="button" variant="ghost" onClick={onDiscard} disabled={saving}>
							Discard
						</Button>
						<Button type="button" variant="primary" onClick={onSave} disabled={saving}>
							{saving ? "Saving…" : "Save changes"}
						</Button>
					</div>
				</>
			) : (
				<>
					<span className="flex shrink-0 items-center gap-2 text-[12px] text-passive">
						<Check className="h-3.5 w-3.5 text-success" aria-hidden="true" />
						All changes saved
						{idleNote && <span>· {idleNote}</span>}
					</span>
					<div className="ml-auto flex min-w-0 items-center gap-3">{status}</div>
				</>
			)}
		</div>
	);
}
