import type { SessionPRSummary } from "../hooks/useSessionScmSummary";
import { cn } from "../lib/utils";
import { Badge } from "./ui/badge";

const PROVIDER_LABEL: Partial<Record<NonNullable<SessionPRSummary["provider"]>, string>> = {
	github: "GitHub",
	gitlab: "GitLab",
};

// Small brand-dot pill identifying which SCM provider a PR/MR belongs to. Color
// is rare and meaningful (DESIGN.md → Color): GitLab gets its brand orange dot,
// GitHub stays neutral. Renders nothing for an empty/unknown provider.
export function ProviderBadge({
	provider,
	className,
}: {
	provider: SessionPRSummary["provider"] | undefined;
	className?: string;
}) {
	const label = provider ? PROVIDER_LABEL[provider] : undefined;
	if (!label) return null;
	const isGitlab = provider === "gitlab";
	return (
		<Badge variant="outline" className={cn("h-5 gap-1 px-1.5 text-[10px] font-medium", className)} title={label}>
			<span
				aria-hidden="true"
				className={cn("h-1.5 w-1.5 rounded-full", !isGitlab && "bg-muted-foreground")}
				style={isGitlab ? { backgroundColor: "#FC6D26" } : undefined}
			/>
			{label}
		</Badge>
	);
}
