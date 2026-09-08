import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import type { UpdateChannel } from "../../../main/update-settings";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";
import { SectionHeading, SettingRow, SettingRows } from "./SettingRow";
import { SettingEditorControl } from "./SettingEditorControl";
import { CompanionControls } from "./CompanionControls";
import { CompanionPreview } from "./CompanionPreview";
import { PetLibrary } from "./PetLibrary";
import { MigrationControls, NotificationsControls, UpdateActions } from "./SystemActions";
import { RESPONSE_LANGUAGE_OPTIONS } from "./response-language";
import { GLOBAL_SECTIONS } from "./settings-sections";
import type { PromptKind } from "./useGlobalSettingsForm";
import type { useGlobalSettingsForm } from "./useGlobalSettingsForm";

const INPUT_CLASS =
	"h-8 w-full max-w-[340px] rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak";

const PROMPT_LABELS: Record<PromptKind, string> = {
	orchestrator: "Orchestrator",
	worker: "Worker",
	qa: "QA",
	reviewer: "Reviewer",
};

// One honest sentence per prompt kind: what that base is, not what a prompt is.
const PROMPT_SUMMARY: Record<PromptKind, string> = {
	orchestrator: "The editable text every orchestrator session starts from.",
	worker: "The editable text every worker session starts from.",
	// The qa member of a crew is a worker SESSION doing a different job, so it has
	// a base of its own.
	qa: "The base the qa member of a crew starts from - a worker session doing a different job.",
	reviewer: "The base the agent that reviews a worker's pull request starts from.",
};

// What each runtime message is FOR. The template name alone says when it is sent
// to a machine, not to a person.
const TEMPLATE_SUMMARY: Record<string, string> = {
	"review-comment-dispatch": "What AO types into a worker's terminal when its pull request picks up review feedback.",
	"ci-failing": "Sent when the worker's pull request goes red.",
	"merge-conflict": "Sent when the worker's branch stops merging cleanly into its target.",
	"pr-base-mismatch":
		"Sent when a pull request would merge somewhere other than where the session was spawned to land.",
	"tracker-bot-comment": "Sent when the tracker issue behind a session gets a new comment.",
	"ao-reviewer-batch": "Sent when AO's own reviewer returns more than one finding at once.",
	"ao-reviewer-single": "Sent when AO's own reviewer returns exactly one finding.",
};

const CHANNEL_OPTIONS: { value: UpdateChannel; label: string }[] = [
	{ value: "latest", label: "Stable (latest release)" },
	{ value: "nightly", label: "Nightly (pre-release)" },
];

type GlobalForm = ReturnType<typeof useGlobalSettingsForm>;

function hint(key: string): string {
	return GLOBAL_SECTIONS.find((s) => s.key === key)?.hint ?? "";
}

// GlobalSettingsContent renders the active Global section against the shared form
// hook. Only one section shows at a time, but the draft lives in the hook above
// it, so navigating between sections never loses an edit and one save bar commits
// the whole global config. Rows marked "Instant, not saved" (Send test, Check for
// updates, Run migration, Purge now, the companion switch, the pet library) act on
// click and are outside the bar by design.
export function GlobalSettingsContent({ form, activeSection }: { form: GlobalForm; activeSection: string }) {
	switch (activeSection) {
		case "running":
			return <WhileWorkRunsSection form={form} />;
		case "cleanup":
			return <CleaningUpSection form={form} />;
		case "mac":
			return <ThisMacSection form={form} />;
		default:
			return <EveryAgentSection form={form} />;
	}
}

function EveryAgentSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionHeading title="Every agent" hint={hint("every-agent")} />
			<SettingRows>
				<SettingRow
					name="Default response language"
					summary="The language every agent writes its human-facing prose in - status updates, reports, questions, PR review comments."
					detail="Code, commit messages, PR/MR titles and bodies, branch names and identifiers always stay English; only prose addressed to a person changes. English is the shipped default and injects no directive at all, so the default path is byte-for-byte the prompt it always was."
					ownership={{ kind: "global-overridable" }}
					timing="next-worker"
					value={draft.responseLanguage || "English"}
					modified={isFieldDirty("responseLanguage")}
					controlId="responseLanguage"
				>
					<LanguageSelect
						id="responseLanguage"
						value={draft.responseLanguage}
						onChange={(v) => setField("responseLanguage", v)}
					/>
				</SettingRow>

				{form.prompts.map((p) => {
					const value = form.draft.prompts[p.kind] ?? p.override ?? p.default;
					return (
						<SettingRow
							key={p.kind}
							name={PROMPT_LABELS[p.kind]}
							summary={PROMPT_SUMMARY[p.kind]}
							detail={
								<>
									AO always appends a protected coordination floor, the confidentiality guard, and dynamic context (git
									convention, spawn-confirm, session and project ids) on top of whatever you write here - those are not
									shown and cannot be removed. Use <code>{"{{.ProjectID}}"}</code> to insert the project id.
								</>
							}
							ownership={{ kind: "global-appendable" }}
							timing={p.kind === "orchestrator" ? "next-orchestrator" : "next-worker"}
							value={value === p.default ? "Default" : "Customised"}
							modified={form.isPromptDirty(p.kind)}
						>
							<SettingEditorControl
								name={PROMPT_LABELS[p.kind]}
								textareaLabel={`${PROMPT_LABELS[p.kind]} system prompt`}
								value={value}
								defaultValue={p.default}
								onChange={(v) => form.setPrompt(p.kind, v)}
							/>
						</SettingRow>
					);
				})}
			</SettingRows>
		</>
	);
}

function WhileWorkRunsSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionHeading title="While work runs" hint={hint("running")} />
			<SettingRows>
				{form.templates.map((t) => {
					const value = form.draft.templates[t.name] ?? t.override ?? t.default;
					const placeholders = t.placeholders ?? [];
					return (
						<SettingRow
							key={t.name}
							name={t.name}
							summary={TEMPLATE_SUMMARY[t.name] ?? "A runtime message AO sends into a worker's terminal."}
							detail={
								<>
									Dynamic values are inserted with Go text/template. A bad edit is caught when the message is sent and
									falls back to the built-in default, so a broken template never blocks a nudge.
								</>
							}
							ownership={{ kind: "global-only" }}
							// Read at send time: the very next nudge uses whatever is saved.
							timing="live"
							value={value === t.default ? "Default" : "Customised"}
							modified={form.isTemplateDirty(t.name)}
						>
							<SettingEditorControl
								name={t.name}
								description={
									placeholders.length > 0 ? `Placeholders (Go text/template): ${placeholders.join(" ")}` : undefined
								}
								textareaLabel={`${t.name} message template`}
								value={value}
								defaultValue={t.default}
								onChange={(v) => form.setTemplate(t.name, v)}
								placeholders={t.placeholders}
							/>
						</SettingRow>
					);
				})}

				<SettingRow
					name="Confirm before spawning workers"
					summary="The orchestrator shows you the task, the source branch, the new branch and the PR target, and waits for your yes before it spawns."
					detail="This is rendered into the orchestrator's own system prompt when that session starts, so the orchestrator you have open right now keeps behaving the way it was started until it is restarted. Turning it off lets the orchestrator spawn workers directly."
					ownership={{ kind: "global-only" }}
					timing="next-orchestrator"
					value={draft.spawnConfirm ? "On" : "Off"}
					modified={isFieldDirty("spawnConfirm")}
					controlId="spawnConfirmEnabled"
				>
					<OnOffSelect
						id="spawnConfirmEnabled"
						value={draft.spawnConfirm}
						onChange={(v) => setField("spawnConfirm", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Auto-send unresolved PR comments"
					summary="A session whose pull request gets review feedback nudges its worker on its own, instead of waiting for you to send it."
					// The old copy called this "the default for new sessions". It is read at
					// the moment the feedback arrives (lifecycle/reactions.go), so it also
					// changes sessions that are already running.
					detail="This is read at the moment the feedback arrives, not when a session starts - so changing it here changes what every session already running does, unless that session set its own answer in its Reviews tab. AO fetches and stores the comments either way; this only decides whether the worker is told."
					ownership={{ kind: "global-session-override" }}
					timing="live"
					value={draft.autoNudge ? "On" : "Off"}
					modified={isFieldDirty("autoNudge")}
				>
					<div className="flex items-center gap-3">
						<Switch
							id="autoNudgeEnabled"
							checked={draft.autoNudge}
							onCheckedChange={(checked) => setField("autoNudge", checked)}
						/>
						<label htmlFor="autoNudgeEnabled" className="text-[12px] text-muted-foreground">
							Enabled by default
						</label>
					</div>
				</SettingRow>
			</SettingRows>
		</>
	);
}

function CleaningUpSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionHeading title="Cleaning up" hint={hint("cleanup")} />
			<SettingRows>
				<SettingRow
					name="Auto-reclaim"
					summary="Once a session is merged or terminated, AO tears down its tmux window and its worktree."
					detail={
						<>
							The git branch is always kept, so a reclaimed session can still be restored, and a worktree with
							uncommitted or untracked changes is never reclaimed. Every reclaim and every refusal is recorded in{" "}
							<code>~/.ao/data/reclaim.jsonl</code>, with what was removed, why it qualified, how much it freed, and the
							branch it left behind.
						</>
					}
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.reclaimEnabled ? "Enabled" : "Disabled"}
					modified={isFieldDirty("reclaimEnabled")}
					controlId="reclaimEnabled"
				>
					<OnOffSelect
						id="reclaimEnabled"
						value={draft.reclaimEnabled}
						onChange={(v) => setField("reclaimEnabled", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Grace period (minutes)"
					summary="How long AO waits after a session finishes before it reclaims anything."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={`${draft.reclaimGrace} min`}
					modified={isFieldDirty("reclaimGrace")}
					controlId="reclaimGrace"
				>
					<input
						id="reclaimGrace"
						type="number"
						min={0}
						className={INPUT_CLASS}
						value={draft.reclaimGrace}
						onChange={(e) => setField("reclaimGrace", Math.max(0, Number(e.target.value) || 0))}
					/>
				</SettingRow>

				<SettingRow
					name="Clear build output"
					summary="Treat derivedDataPath, Pods and node_modules as disposable rather than as work worth keeping."
					detail={
						<>
							Untracked build output would otherwise keep a finished worktree on disk for ever, so AO clears it out of
							the way - and only ever when nothing else in the worktree has changed. Turn this off to treat build output
							as work too.
						</>
					}
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.reclaimArtifacts ? "Enabled" : "Disabled"}
					modified={isFieldDirty("reclaimArtifacts")}
					controlId="reclaimArtifacts"
				>
					<OnOffSelect
						id="reclaimArtifacts"
						value={draft.reclaimArtifacts}
						onChange={(v) => setField("reclaimArtifacts", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Retention"
					summary="Screenshots and clips you attach in the Tests tab are deleted once they pass the age below."
					detail="Evidence is stored on disk under ~/.ao and the age is measured from when it was captured. Set this to Disabled to keep evidence for ever."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.evidenceRetentionEnabled ? "Enabled" : "Disabled"}
					modified={isFieldDirty("evidenceRetentionEnabled")}
					controlId="evidenceRetentionEnabled"
				>
					<OnOffSelect
						id="evidenceRetentionEnabled"
						value={draft.evidenceRetentionEnabled}
						onChange={(v) => setField("evidenceRetentionEnabled", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Delete evidence older than (days)"
					summary="The age at which an attached screenshot or clip is swept."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={`${draft.evidenceRetentionDays} days`}
					modified={isFieldDirty("evidenceRetentionDays")}
					controlId="evidenceRetentionDays"
				>
					<input
						id="evidenceRetentionDays"
						type="number"
						min={1}
						max={3650}
						className={INPUT_CLASS}
						value={draft.evidenceRetentionDays}
						onChange={(e) =>
							setField("evidenceRetentionDays", Math.max(1, Math.min(3650, Number(e.target.value) || 1)))
						}
					/>
				</SettingRow>

				<SettingRow
					name="Purge evidence now"
					summary="Runs the age sweep immediately and reports what it removed."
					detail="It uses the SAVED retention age, not the number in the box above - save a change first for it to count."
					ownership={{ kind: "global-only" }}
					timing="instant"
				>
					<EvidenceRetentionPurgeButton />
				</SettingRow>
			</SettingRows>
		</>
	);
}

// EvidenceRetentionPurgeButton is an instant action (outside the save bar) that
// runs the age-based sweep now with the CURRENTLY-SAVED TTL and reports what it
// removed. Save any TTL change first for it to take effect.
function EvidenceRetentionPurgeButton() {
	const [status, setStatus] = useState<string | null>(null);
	const purge = useMutation({
		mutationFn: async () => {
			const { data, error } = await apiClient.POST("/api/v1/settings/evidence-retention/sweep", {});
			if (error) throw new Error(apiErrorMessage(error));
			return data as { purged: number; freedBytes: number };
		},
		onSuccess: (r) =>
			setStatus(
				r.purged > 0
					? `Purged ${r.purged} item${r.purged === 1 ? "" : "s"} · freed ${formatBytes(r.freedBytes)}.`
					: "Nothing to purge — no evidence is past the retention age.",
			),
		onError: (e) => setStatus(apiErrorMessage(e, "Sweep failed.")),
	});
	return (
		<div className="flex items-center gap-3">
			<Button type="button" variant="outline" onClick={() => purge.mutate()} disabled={purge.isPending}>
				{purge.isPending ? "Purging…" : "Purge now"}
			</Button>
			{status && <span className="text-[12px] text-muted-foreground">{status}</span>}
		</div>
	);
}

function formatBytes(n: number): string {
	if (n < 1024) return `${n} B`;
	const units = ["KB", "MB", "GB", "TB"];
	let v = n / 1024;
	let i = 0;
	while (v >= 1024 && i < units.length - 1) {
		v /= 1024;
		i++;
	}
	return `${v.toFixed(v >= 10 || i === 0 ? 0 : 1)} ${units[i]}`;
}

function ThisMacSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionHeading title="This Mac" hint={hint("mac")} />
			<SettingRows>
				<SettingRow
					name="Automatic updates"
					summary="AO downloads and installs new builds of the desktop app on its own."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.updatesEnabled ? "Enabled" : "Disabled"}
					modified={isFieldDirty("updatesEnabled")}
					controlId="updatesEnabled"
				>
					<OnOffSelect
						id="updatesEnabled"
						value={draft.updatesEnabled}
						onChange={(v) => setField("updatesEnabled", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Update channel"
					summary="Which build stream automatic updates follow."
					detail={
						<>
							Stable follows tagged releases.{" "}
							{/* The warning appears only while Nightly is actually selected: a caution
							    printed on every visit stops being read as a caution. */}
							{draft.updateChannel === "nightly" && draft.updatesEnabled && (
								<span className="text-warning">
									Nightly builds are cut every day and can be unstable or lose data. Only use Nightly if you are
									comfortable with that.
								</span>
							)}
						</>
					}
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={CHANNEL_OPTIONS.find((c) => c.value === draft.updateChannel)?.label ?? draft.updateChannel}
					modified={isFieldDirty("updateChannel")}
					controlId="updateChannel"
				>
					<Select
						value={draft.updateChannel}
						onValueChange={(v) => setField("updateChannel", v as UpdateChannel)}
						disabled={!draft.updatesEnabled}
					>
						<SelectTrigger id="updateChannel" className="h-8 w-full max-w-[340px] text-[13px]">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							{CHANNEL_OPTIONS.map((opt) => (
								<SelectItem key={opt.value} value={opt.value}>
									{opt.label}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				</SettingRow>

				<SettingRow
					name="Updates"
					summary="Looks for a newer build right now, downloads it, and restarts into it - even when automatic updates are off."
					ownership={{ kind: "global-only" }}
					timing="instant"
				>
					<UpdateActions />
				</SettingRow>

				{/* The Wiki is one personal note vault, not a project - so its path is a
				    global setting, and an empty path hides the destination entirely. */}
				<SettingRow
					name="Vault folder"
					summary="A folder of markdown notes you can ask an agent about. Setting a path adds a Wiki entry above Projects in the sidebar."
					detail="An absolute path, or one starting with ~/. The agent runs with your notes as its working directory and can edit and create them. Leave it empty to turn the Wiki off and remove the sidebar entry."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.wikiVaultPath || "Off"}
					modified={isFieldDirty("wikiVaultPath")}
					controlId="wikiVaultPath"
				>
					<Input
						id="wikiVaultPath"
						className="h-8 max-w-[340px] font-mono text-[12.5px]"
						placeholder="~/Notes"
						spellCheck={false}
						value={draft.wikiVaultPath}
						onChange={(e) => setField("wikiVaultPath", e.target.value)}
					/>
				</SettingRow>

				<SettingRow
					name="Notifications"
					summary="Sends a native banner down the exact path a real notification takes, so you can confirm macOS is letting them through."
					detail="The banner should appear whether or not the Agent Orchestrator window is focused."
					ownership={{ kind: "global-only" }}
					timing="instant"
				>
					<NotificationsControls />
				</SettingRow>

				<SettingRow
					name="Desktop companion"
					summary="Shows one small character per session along the bottom of your screen - working, waiting on you, or done."
					detail="It sits above the Dock and clicks pass straight through it, except on a character itself. The switch opens or closes a window immediately; a control with a visible consequence that waited for Save would read as broken."
					ownership={{ kind: "global-only" }}
					timing="instant"
				>
					<div className="flex flex-col gap-3">
						<CompanionControls />
						<CompanionPreview />
					</div>
				</SettingRow>

				<SettingRow
					name="Pet library"
					summary="Which creature each project's sessions appear as in the companion."
					detail="Also reachable by right-clicking a character on the desktop, which is where most people find it."
					ownership={{ kind: "global-only" }}
					timing="instant"
					defaultOpen
				>
					<PetLibrary />
				</SettingRow>

				<SettingRow
					name="Migration"
					summary="Imports projects and orchestrator sessions from an earlier Agent Orchestrator install."
					detail="Your old files are never modified, and this is safe to run more than once."
					ownership={{ kind: "global-only" }}
					timing="instant"
				>
					<MigrationControls />
				</SettingRow>
			</SettingRows>
		</>
	);
}

function LanguageSelect({ id, value, onChange }: { id: string; value: string; onChange: (value: string) => void }) {
	// An unknown stored value (a free-form language set via API/CLI) is still shown
	// so the user never silently loses it: append it as an extra option.
	const options = RESPONSE_LANGUAGE_OPTIONS.includes(value as (typeof RESPONSE_LANGUAGE_OPTIONS)[number])
		? RESPONSE_LANGUAGE_OPTIONS
		: [value, ...RESPONSE_LANGUAGE_OPTIONS];
	return (
		<Select value={value || "English"} onValueChange={onChange}>
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				{options.map((lang) => (
					<SelectItem key={lang} value={lang}>
						{lang}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}

function OnOffSelect({ id, value, onChange }: { id: string; value: boolean; onChange: (value: boolean) => void }) {
	return (
		<Select value={value ? "on" : "off"} onValueChange={(v) => onChange(v === "on")}>
			<SelectTrigger id={id} className="h-8 w-full max-w-[340px] text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="on">Enabled</SelectItem>
				<SelectItem value="off">Disabled</SelectItem>
			</SelectContent>
		</Select>
	);
}
