import { Route } from "lucide-react";
import { isRoutedProfile } from "../lib/claude-profiles";
import type { WorkspaceSession } from "../types/workspace";

export function ClaudeProfileChip({ session, compact = false }: { session: WorkspaceSession; compact?: boolean }) {
	const profile = session.claudeProfile;
	if (!profile || !isRoutedProfile(profile)) return null;
	const label = `Claude profile ${profile}`;
	const title = `Claude Code runs on the ${profile} profile's settings, not the subscription`;

	if (compact) {
		return (
			<span
				aria-label={label}
				className="inline-flex min-w-0 max-w-[88px] shrink items-center gap-0.5 text-[10px] text-passive"
				title={title}
			>
				<Route className="h-3 w-3 shrink-0" strokeWidth={2} aria-hidden="true" />
				<span className="truncate">{profile}</span>
			</span>
		);
	}
	return (
		<span
			aria-label={label}
			className="inline-flex max-w-full shrink-0 items-center gap-1 rounded-full border border-[color-mix(in_srgb,var(--fg-passive)_30%,transparent)] px-1.5 py-0.5 text-[10px] font-medium text-passive"
			title={title}
		>
			<Route className="h-3 w-3 shrink-0" strokeWidth={2} aria-hidden="true" />
			<span className="truncate">{profile}</span>
		</span>
	);
}
