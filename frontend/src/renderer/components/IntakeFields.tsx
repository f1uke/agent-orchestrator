import { Info } from "lucide-react";
import type { components } from "../../api/schema";
import { Label } from "./ui/label";
import { Tooltip, TooltipContent, TooltipProvider, TooltipTrigger } from "./ui/tooltip";

type TrackerIntakeConfig = components["schemas"]["TrackerIntakeConfig"];

// IntakeForm is the flat, string-backed shape both the create sheet and the
// project settings form edit. repo has no input today (it's derived from the
// git origin server-side) but is plumbed so a value set via the CLI
// (--tracker-repo) survives a UI save instead of being wiped. provider mirrors
// the project's SCM (github vs a self-hosted GitLab); the settings form derives
// it from the git origin host rather than asking, matching how the daemon
// routes intake (observer.go trackerRepo).
export type IntakeForm = {
	enabled: boolean;
	provider: TrackerProvider;
	repo: string;
	assignee: string;
};

// The tracker providers intake supports today (backend openapi enum
// TrackerIntakeConfig.provider). Adding Linear/Jira later grows this union and
// buildIntake switches the scope field it emits.
type TrackerProvider = NonNullable<TrackerIntakeConfig["provider"]>;

// intakeNeedsRule mirrors the backend guard (TrackerIntakeConfig.Validate):
// enabling intake requires an assignee so it cannot drain an entire issue
// backlog. v1 intake is assignee-only.
export function intakeNeedsRule(form: IntakeForm): boolean {
	return form.enabled && form.assignee.trim() === "";
}

// buildIntake produces the payload field, scrubbing empties so a disabled or
// blank intake serializes to `undefined` (omit) rather than an empty object the
// daemon would persist.
export function buildIntake(form: IntakeForm): TrackerIntakeConfig | undefined {
	const next: TrackerIntakeConfig = {
		enabled: form.enabled || undefined,
		provider: form.enabled ? form.provider : undefined,
		repo: form.repo.trim() || undefined,
		assignee: form.assignee.trim() || undefined,
	};
	return Object.values(next).some((v) => v !== undefined) ? next : undefined;
}

// originPath extracts the repository path (no host, no trailing ".git") from a
// git origin URL in any of the common forms: git@host:group/proj.git,
// https://host/group/proj.git, or ssh://git@host/group/proj.git.
function originPath(remote?: string): string | undefined {
	const trimmed = remote?.trim();
	if (!trimmed) return undefined;
	let path: string | undefined;
	if (trimmed.startsWith("git@")) {
		path = trimmed.split(":")[1];
	} else {
		try {
			path = new URL(trimmed).pathname;
		} catch {
			path = trimmed;
		}
	}
	if (!path) return undefined;
	const cleaned = path.replace(/\.git$/, "").replace(/^\/+|\/+$/g, "");
	return cleaned || undefined;
}

// originHost extracts the hostname from a git origin URL (git@host:..., or any
// URL form). Returns undefined when no host can be determined.
export function originHost(remote?: string): string | undefined {
	const trimmed = remote?.trim();
	if (!trimmed) return undefined;
	if (trimmed.startsWith("git@")) {
		const host = trimmed.slice(4).split(":")[0].trim();
		return host || undefined;
	}
	try {
		return new URL(trimmed).host || undefined;
	} catch {
		return undefined;
	}
}

// providerFromOrigin classifies a git origin as github or gitlab by inspecting
// its host — not the whole URL, so a GitHub repo merely named "gitlab-*" is not
// misread. github.com / GHES hosts are GitHub; a host containing "gitlab" is a
// self-hosted GitLab. The frontend cannot know AO_GITLAB_HOST, so anything else
// falls back to github (the daemon still routes authoritatively server-side).
export function providerFromOrigin(remote?: string): TrackerProvider {
	const host = originHost(remote)
		?.toLowerCase()
		.replace(/^www\./, "");
	if (!host) return "github";
	if (host === "github.com" || host.endsWith(".github.com") || host.endsWith(".ghe.io")) return "github";
	if (host.includes("gitlab")) return "gitlab";
	return "github";
}

// deriveGitHubRepo mirrors the daemon's parseGitHubRepoNative (observer.go):
// derive "owner/repo" from a git origin URL for display only. The daemon does
// the authoritative derivation server-side at poll time; this is purely so a
// settings card can show which repo intake will actually poll.
export function deriveGitHubRepo(remote?: string): string | undefined {
	const path = originPath(remote);
	if (!path) return undefined;
	const parts = path.split("/");
	if (parts.length < 2) return undefined;
	const owner = parts[parts.length - 2].trim();
	const repo = parts[parts.length - 1].trim();
	return owner && repo ? `${owner}/${repo}` : undefined;
}

// deriveGitLabRepo mirrors the daemon's parseGitLabRepoNative (observer.go):
// GitLab supports nested groups, so the full "group/sub/proj" path is preserved
// rather than truncated to two segments.
export function deriveGitLabRepo(remote?: string): string | undefined {
	const path = originPath(remote);
	if (!path || !path.includes("/")) return undefined;
	const segments = path.split("/");
	if (segments.some((seg) => seg.trim() === "")) return undefined;
	return path;
}

// deriveTrackerRepo returns the provider-native repo key for display, matching
// the daemon's per-provider derivation.
export function deriveTrackerRepo(remote: string | undefined, provider: TrackerProvider): string | undefined {
	return provider === "gitlab" ? deriveGitLabRepo(remote) : deriveGitHubRepo(remote);
}

// deriveRepoWebURL builds the browser URL for the intake repo (both providers),
// used to link the repo-preview row. Returns undefined when the origin lacks a
// host or a usable path.
export function deriveRepoWebURL(remote?: string): string | undefined {
	const host = originHost(remote);
	if (!host) return undefined;
	const path = deriveTrackerRepo(remote, providerFromOrigin(remote));
	return path ? `https://${host}/${path}` : undefined;
}

// IntakeFields renders the shared "Tracker intake" controls: an enable checkbox
// that reveals the eligibility inputs. It is deliberately card-agnostic (no
// <Card> wrapper) so the create sheet and the settings form can frame it
// however they like.
//
// repoPreview is only meaningful once a project exists and its git origin is
// known: pass `{ show: true, value }` from settings to render the repo link
// row, and omit it from the create sheet (the origin URL isn't available there,
// and the daemon derives the repo regardless).
export function IntakeFields({
	form,
	onChange,
	repoPreview,
	compact = false,
}: {
	form: IntakeForm;
	onChange: (patch: Partial<IntakeForm>) => void;
	repoPreview?: { value?: string; url?: string };
	// compact drops the descriptive/help prose and folds the explanation into an
	// info-icon tooltip — used by the create-project sheet, which stays minimal.
	compact?: boolean;
}) {
	const needsRule = intakeNeedsRule(form);
	return (
		<div className="flex flex-col gap-4">
			{!compact && (
				<p className="text-[12px] leading-5 text-muted-foreground">
					Auto-spawn worker sessions from matching tracker issues.
				</p>
			)}
			<div className="flex items-center gap-2">
				<label className="flex items-center gap-2.5 text-[13px] text-foreground">
					<input
						type="checkbox"
						className="h-4 w-4 accent-accent"
						checked={form.enabled}
						onChange={(e) => onChange({ enabled: e.target.checked })}
					/>
					Enable issue intake
				</label>
				{compact && (
					<TooltipProvider delayDuration={0}>
						<Tooltip>
							<TooltipTrigger asChild>
								<button
									type="button"
									className="grid size-4 place-items-center rounded-full text-muted-foreground hover:text-foreground focus-visible:outline-none"
									aria-label="What does enabling issue intake do?"
								>
									<Info className="size-3.5" aria-hidden="true" />
								</button>
							</TooltipTrigger>
							<TooltipContent>Auto-spawns a worker session for each matching tracker issue.</TooltipContent>
						</Tooltip>
					</TooltipProvider>
				)}
			</div>
			{form.enabled && (
				<>
					{repoPreview && (
						<IntakeField label="Repository">
							{repoPreview.value ? (
								repoPreview.url ? (
									<a
										href={repoPreview.url}
										target="_blank"
										rel="noopener noreferrer"
										className="text-[13px] text-accent hover:underline"
									>
										{repoPreview.value}
									</a>
								) : (
									<span className="text-[13px] text-foreground">{repoPreview.value}</span>
								)
							) : (
								<span className="text-[13px] text-muted-foreground">
									Could not detect a tracker repo from this project's git origin.
								</span>
							)}
						</IntakeField>
					)}
					<IntakeField label="Assignee" htmlFor="intakeAssignee">
						<input
							id="intakeAssignee"
							className="h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak"
							value={form.assignee}
							onChange={(e) => onChange({ assignee: e.target.value })}
							placeholder="type username or * for any"
						/>
					</IntakeField>
					{!compact && needsRule && (
						<p className="text-[12px] leading-5 text-error">Enabling intake requires an assignee.</p>
					)}
				</>
			)}
		</div>
	);
}

function IntakeField({ label, htmlFor, children }: { label: string; htmlFor?: string; children: React.ReactNode }) {
	return (
		<div className="flex flex-col gap-1.5">
			<Label htmlFor={htmlFor} className="text-[12px] text-muted-foreground">
				{label}
			</Label>
			{children}
		</div>
	);
}
