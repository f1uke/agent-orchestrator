import { useState } from "react";
import { useMutation } from "@tanstack/react-query";
import { Bot, MessageSquare } from "lucide-react";
import type { UpdateChannel } from "../../../main/update-settings";
import { apiClient, apiErrorMessage } from "../../lib/api-client";
import { Button } from "../ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";
import { SettingsGroup } from "./SettingsGroup";
import { SettingsField } from "./SettingsField";
import { SettingEditorRow } from "./SettingEditorRow";
import { MigrationControls, NotificationsControls, UpdateActions } from "./SystemActions";
import { RESPONSE_LANGUAGE_OPTIONS } from "./response-language";
import type { PromptKind } from "./useGlobalSettingsForm";
import type { useGlobalSettingsForm } from "./useGlobalSettingsForm";

const INPUT_CLASS =
	"h-8 w-full rounded-md border border-input bg-transparent px-2.5 text-[13px] text-foreground placeholder:text-passive focus-visible:border-accent focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-accent-weak";

const PROMPT_LABELS: Record<PromptKind, string> = {
	orchestrator: "Orchestrator",
	worker: "Worker",
	reviewer: "Reviewer",
};
const PROMPT_PURPOSE: Record<PromptKind, string> = {
	orchestrator: "Global base the orchestrator starts from",
	worker: "Global base each worker starts from",
	reviewer: "Global base the reviewer starts from",
};

const CHANNEL_OPTIONS: { value: UpdateChannel; label: string }[] = [
	{ value: "latest", label: "Stable (latest release)" },
	{ value: "nightly", label: "Nightly (pre-release)" },
];

type GlobalForm = ReturnType<typeof useGlobalSettingsForm>;

// GlobalSettingsContent renders the active Global section against the shared form
// hook. Only one section shows at a time (the two-pane surface), but the draft
// lives in the hook above it, so navigating between sections never loses an edit
// and one save bar commits the whole global config. The System section is the
// exception the save bar tolerates: its update channel routes through Save, while
// Send test / Check for updates / Run migration are instant actions (SystemActions).
export function GlobalSettingsContent({ form, activeSection }: { form: GlobalForm; activeSection: string }) {
	switch (activeSection) {
		case "messages":
			return <MessagesSection form={form} />;
		case "automation":
			return <AutomationSection form={form} />;
		case "system":
			return <SystemSection form={form} />;
		default:
			return <PromptsSection form={form} />;
	}
}

function SectionTitle({ title, hint }: { title: string; hint: string }) {
	return (
		<div className="mb-4 flex items-center gap-2.5 border-b border-border pb-3 pt-2.5">
			<h2 className="text-[15px] font-semibold tracking-[-0.01em] text-foreground">{title}</h2>
			<span className="text-[12px] font-normal text-passive">· {hint}</span>
		</div>
	);
}

function PromptsSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionTitle title="Prompts" hint="the global base each session kind starts from" />

			<SettingsGroup title="Human-facing response language">
				<p className="text-[12px] leading-5 text-muted-foreground">
					The language every agent (orchestrator, worker, reviewer) writes its human-facing output in - status updates,
					reports, questions, and PR/MR review comments. Code, commit messages, PR/MR titles and bodies, branch names,
					and identifiers always stay English. English (the default) injects no directive. A project can override this
					from its own Settings.
				</p>
				<SettingsField
					label="Default response language"
					htmlFor="responseLanguage"
					modified={isFieldDirty("responseLanguage")}
				>
					<LanguageSelect
						id="responseLanguage"
						value={draft.responseLanguage}
						onChange={(v) => setField("responseLanguage", v)}
					/>
				</SettingsField>
			</SettingsGroup>

			<p className="mb-4 mt-6 text-[12px] leading-relaxed text-passive">
				Edit the global base each session kind starts from. AO always appends a protected coordination floor, the
				confidentiality guard, and dynamic context (git convention, spawn-confirm, session and project ids) — those are
				not shown here. Use <code>{"{{.ProjectID}}"}</code> in the orchestrator base to insert the project id.
			</p>
			{form.prompts.map((p) => (
				<SettingEditorRow
					key={p.kind}
					icon={Bot}
					name={PROMPT_LABELS[p.kind]}
					purpose={PROMPT_PURPOSE[p.kind]}
					textareaLabel={`${PROMPT_LABELS[p.kind]} system prompt`}
					value={form.draft.prompts[p.kind] ?? p.override ?? p.default}
					defaultValue={p.default}
					modified={form.isPromptDirty(p.kind)}
					onChange={(v) => form.setPrompt(p.kind, v)}
				/>
			))}
		</>
	);
}

function MessagesSection({ form }: { form: GlobalForm }) {
	return (
		<>
			<SectionTitle title="Messages" hint="runtime nudge messages sent into a worker" />
			<p className="mb-4 text-[12px] leading-relaxed text-passive">
				Edit the runtime messages AO sends into a worker's terminal. Dynamic values are inserted via each message's
				placeholders (Go text/template). A bad edit falls back to the built-in default.
			</p>
			{form.templates.map((t) => (
				<SettingEditorRow
					key={t.name}
					icon={MessageSquare}
					name={t.name}
					purpose={
						(t.placeholders ?? []).length > 0 ? `Placeholders: ${(t.placeholders ?? []).join(" ")}` : "No placeholders"
					}
					description={
						(t.placeholders ?? []).length > 0
							? `Placeholders (Go text/template): ${(t.placeholders ?? []).join(" ")}`
							: undefined
					}
					textareaLabel={`${t.name} message template`}
					value={form.draft.templates[t.name] ?? t.override ?? t.default}
					defaultValue={t.default}
					modified={form.isTemplateDirty(t.name)}
					onChange={(v) => form.setTemplate(t.name, v)}
					placeholders={t.placeholders}
				/>
			))}
		</>
	);
}

function AutomationSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionTitle title="Automation" hint="orchestrator & daemon automatic behaviour" />

			<SettingsGroup title="Confirm before spawning workers">
				<p className="text-[12px] leading-5 text-muted-foreground">
					When on, the orchestrator shows a summary — the task, the source branch, the new branch, and the pull-request
					target — and waits for your approval in chat before it runs <code>ao spawn</code>. When off, it spawns workers
					directly.
				</p>
				<SettingsField
					label="Confirm before spawning"
					htmlFor="spawnConfirmEnabled"
					modified={isFieldDirty("spawnConfirm")}
				>
					<OnOffSelect
						id="spawnConfirmEnabled"
						value={draft.spawnConfirm}
						onChange={(v) => setField("spawnConfirm", v)}
					/>
				</SettingsField>
			</SettingsGroup>

			<SettingsGroup title="Auto-send unresolved PR comments to the worker">
				<p className="text-[12px] leading-5 text-muted-foreground">
					When on, a session whose pull request gets an unresolved review comment (or a changes-requested review)
					automatically nudges its worker. This is the default for new sessions — each session can override it from its
					Reviews tab.
				</p>
				<div className="flex items-center gap-3">
					<Switch
						id="autoNudgeEnabled"
						checked={draft.autoNudge}
						onCheckedChange={(checked) => setField("autoNudge", checked)}
					/>
					<label htmlFor="autoNudgeEnabled" className="text-[12px] text-muted-foreground">
						Enabled by default
					</label>
					{isFieldDirty("autoNudge") && (
						<span className="font-mono text-[10px] tracking-[0.04em] text-warning" aria-label="modified">
							● Modified
						</span>
					)}
				</div>
			</SettingsGroup>

			<SettingsGroup title="Auto-reclaim finished sessions">
				<p className="text-[12px] leading-5 text-muted-foreground">
					When a session is merged or terminated, AO tears down its tmux and worktree after the grace period. The git
					branch is kept, so the session can still be restored.
				</p>
				<SettingsField label="Auto-reclaim" htmlFor="reclaimEnabled" modified={isFieldDirty("reclaimEnabled")}>
					<OnOffSelect
						id="reclaimEnabled"
						value={draft.reclaimEnabled}
						onChange={(v) => setField("reclaimEnabled", v)}
					/>
				</SettingsField>
				<SettingsField label="Grace period (minutes)" htmlFor="reclaimGrace" modified={isFieldDirty("reclaimGrace")}>
					<input
						id="reclaimGrace"
						type="number"
						min={0}
						className={INPUT_CLASS}
						value={draft.reclaimGrace}
						onChange={(e) => setField("reclaimGrace", Math.max(0, Number(e.target.value) || 0))}
					/>
				</SettingsField>
			</SettingsGroup>

			<SettingsGroup title="Smoke-test evidence retention">
				<p className="text-[12px] leading-5 text-muted-foreground">
					Screenshots and clips you attach in the Tests tab are stored on disk under <code>~/.ao</code>. AO
					automatically deletes evidence older than the age below, measured from when it was captured. Set Retention to
					Disabled to keep evidence forever.
				</p>
				<SettingsField
					label="Retention"
					htmlFor="evidenceRetentionEnabled"
					modified={isFieldDirty("evidenceRetentionEnabled")}
				>
					<OnOffSelect
						id="evidenceRetentionEnabled"
						value={draft.evidenceRetentionEnabled}
						onChange={(v) => setField("evidenceRetentionEnabled", v)}
					/>
				</SettingsField>
				<SettingsField
					label="Delete evidence older than (days)"
					htmlFor="evidenceRetentionDays"
					modified={isFieldDirty("evidenceRetentionDays")}
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
				</SettingsField>
				<EvidenceRetentionPurgeButton />
			</SettingsGroup>
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
		<div className="mt-1 flex items-center gap-3">
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

function SystemSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty } = form;
	return (
		<>
			<SectionTitle title="System" hint="notifications, updates & migration" />

			<SettingsGroup title="Updates">
				<SettingsField label="Automatic updates" htmlFor="updatesEnabled" modified={isFieldDirty("updatesEnabled")}>
					<OnOffSelect
						id="updatesEnabled"
						value={draft.updatesEnabled}
						onChange={(v) => setField("updatesEnabled", v)}
					/>
				</SettingsField>
				<SettingsField label="Update channel" htmlFor="updateChannel" modified={isFieldDirty("updateChannel")}>
					<Select
						value={draft.updateChannel}
						onValueChange={(v) => setField("updateChannel", v as UpdateChannel)}
						disabled={!draft.updatesEnabled}
					>
						<SelectTrigger id="updateChannel" className="h-8 w-full text-[13px]">
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
				</SettingsField>
				{draft.updateChannel === "nightly" && draft.updatesEnabled && (
					<p className="text-[12px] leading-5 text-warning">
						Nightly builds are cut every day and can be unstable or lose data. Only use Nightly if you are comfortable
						with that.
					</p>
				)}
				{/* Instant, out of the save bar: version + Check / Update / Restart. */}
				<div className="border-t border-border pt-4">
					<UpdateActions />
				</div>
			</SettingsGroup>

			<SettingsGroup title="Notifications">
				<p className="text-[12px] leading-5 text-muted-foreground">
					Send a test banner to confirm macOS notifications are working. The banner should appear whether or not the
					Agent Orchestrator window is focused.
				</p>
				<NotificationsControls />
			</SettingsGroup>

			<SettingsGroup title="Migration">
				<p className="text-[12px] leading-5 text-muted-foreground">
					Import projects and orchestrator sessions from an earlier Agent Orchestrator install. Your old files are never
					modified, and this is safe to run more than once.
				</p>
				<MigrationControls />
			</SettingsGroup>
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
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
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
			<SelectTrigger id={id} className="h-8 w-full text-[13px]">
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				<SelectItem value="on">Enabled</SelectItem>
				<SelectItem value="off">Disabled</SelectItem>
			</SelectContent>
		</Select>
	);
}
