import { Check, Minus } from "lucide-react";
import type { UpdateChannel } from "../../../main/update-settings";
import { Button } from "../ui/button";
import { Input } from "../ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../ui/select";
import { Switch } from "../ui/switch";
import { Textarea } from "../ui/textarea";
import { SectionHeading, SettingRow, SettingRows } from "./SettingRow";
import { SettingEditorControl } from "./SettingEditorControl";
import { CompanionControls } from "./CompanionControls";
import { InlineCompletionControls, inlineCompletionSummary } from "./InlineCompletionControls";
import { useInlineCompletionStatus } from "../../lib/inline-completion/status";
import { CompanionPreview } from "./CompanionPreview";
import { PetLibrary } from "./PetLibrary";
import { MigrationControls, NotificationsControls, UpdateActions } from "./SystemActions";
import { RESPONSE_LANGUAGE_OPTIONS } from "./response-language";
import { ShortcutField } from "./ShortcutField";
import { DEFAULT_EDITOR_SETTINGS } from "../../../shared/editor-settings";
import { shortcutLabel } from "../../../shared/editor-shortcuts";
import { isMacPlatform } from "../../lib/platform";
import { caFilesCount, formatCaFileLines, parseCaFileLines, sameCaFiles } from "../../lib/sim-trust";
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
// updates, Run migration, the companion switch, the pet library) act on
// click and are outside the bar by design.
export function GlobalSettingsContent({ form, activeSection }: { form: GlobalForm; activeSection: string }) {
	switch (activeSection) {
		case "running":
			return <WhileWorkRunsSection form={form} />;
		case "cleanup":
			return <CleaningUpSection form={form} />;
		case "editor":
			return <CodeEditorSection form={form} />;
		case "devices":
			return <SimulatorsSection form={form} />;
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
					const warnings = p.warnings ?? [];
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
									{warnings.map((w) => (
										<span key={w} className="mt-1.5 block text-warning">
											{w}
										</span>
									))}
								</>
							}
							ownership={{ kind: "global-appendable" }}
							timing={p.kind === "orchestrator" ? "next-orchestrator" : "next-worker"}
							value={value === p.default ? "Default" : "Customised"}
							modified={form.isPromptDirty(p.kind)}
							// A folded row would hide the warning from the human it is for.
							defaultOpen={warnings.length > 0}
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
			</SettingRows>
		</>
	);
}

// The built-in editor: predictive completion (both rows instant - they start
// and stop a process and a download, not something to stage behind a Save),
// then indentation and formatting (saved with the bar like everything else).
function CodeEditorSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty } = form;
	const status = useInlineCompletionStatus();
	const mac = isMacPlatform();
	const shown = (shortcut: string) => (shortcut === "" ? "Not set" : shortcutLabel(shortcut, mac));
	return (
		<>
			<SectionHeading title="Code editor" hint={hint("editor")} />
			<SettingRows>
				<SettingRow
					name="Predictive code completion"
					summary="Shows what you are likely to type next as grey text after the cursor, and - when there is nothing to add there - the change you are likely to make next, drawn where it would land. Tab accepts it (jumping there first when it is further away); Esc or typing something else dismisses it."
					detail={
						<>
							A code model (Sweep Next-Edit 1.5B) runs on this Mac: AO downloads llama.cpp and the model into{" "}
							<code>~/.ao/llm</code> the first time, starts its own llama-server while this is on, and stops exactly
							that process when you turn it off or quit. Your code never leaves this Mac. Language-server completions
							(⌃Space) keep working alongside it, and Tab still indents when no prediction is showing.
						</>
					}
					ownership={{ kind: "global-only" }}
					timing="instant"
					value={inlineCompletionSummary(status)}
					defaultOpen
				>
					<InlineCompletionControls />
				</SettingRow>

				<SettingRow
					name="Indent while typing"
					summary="Return starts the new line at the right depth, a closing } ] or ) on its own line moves back to its opener, and pasted code is re-indented to fit where it lands."
					detail="Only indentation changes - nothing else on a line is rewritten. Undo takes back a pasted block's re-indent first, then the paste itself."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.editorIndentOnType ? "Enabled" : "Disabled"}
					modified={isFieldDirty("editorIndentOnType")}
					controlId="editorIndentOnType"
				>
					<OnOffSelect
						id="editorIndentOnType"
						value={draft.editorIndentOnType}
						onChange={(v) => setField("editorIndentOnType", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Re-indent"
					summary="Re-indents the selected lines, or the line with the cursor, like Xcode's Re-Indent. Only indentation changes."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={shown(draft.editorReindentShortcut)}
					modified={isFieldDirty("editorReindentShortcut")}
					controlId="editorReindentShortcut"
				>
					<ShortcutField
						id="editorReindentShortcut"
						value={draft.editorReindentShortcut}
						defaultValue={DEFAULT_EDITOR_SETTINGS.reindentShortcut}
						other={{ shortcut: draft.editorFormatShortcut, what: "Format Document" }}
						onChange={(v) => setField("editorReindentShortcut", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Format Document"
					summary="Formats the whole file with its language's own formatter, as one undo step."
					detail={
						<>
							Go uses gopls, or gofmt when gopls is not running. Swift uses sourcekit-lsp or swift-format, and follows
							the project's .swift-format. TypeScript, JavaScript and the other web languages use Prettier only when the
							project configures it. Any other language with braces is re-indented instead. A formatter that is missing
							or fails leaves the file exactly as it was and says why.
						</>
					}
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={shown(draft.editorFormatShortcut)}
					modified={isFieldDirty("editorFormatShortcut")}
					controlId="editorFormatShortcut"
				>
					<ShortcutField
						id="editorFormatShortcut"
						value={draft.editorFormatShortcut}
						defaultValue={DEFAULT_EDITOR_SETTINGS.formatShortcut}
						other={{ shortcut: draft.editorReindentShortcut, what: "Re-indent" }}
						onChange={(v) => setField("editorFormatShortcut", v)}
					/>
				</SettingRow>

				<SettingRow
					name="Format on save"
					summary="Formats the file just before it is saved."
					detail="If the formatter is missing or refuses the file, it is saved as it is and the editor says why."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.editorFormatOnSave ? "Enabled" : "Disabled"}
					modified={isFieldDirty("editorFormatOnSave")}
					controlId="editorFormatOnSave"
				>
					<OnOffSelect
						id="editorFormatOnSave"
						value={draft.editorFormatOnSave}
						onChange={(v) => setField("editorFormatOnSave", v)}
					/>
				</SettingRow>
			</SettingRows>
		</>
	);
}

// What AO does to a simulator before anyone uses it. Today that is one thing:
// making it trust the root CA of the debugging proxy on this Mac.
function SimulatorsSection({ form }: { form: GlobalForm }) {
	const { draft, setField, isFieldDirty, simTrust } = form;
	const files = parseCaFileLines(draft.simTrustCaFiles);
	const defaults = simTrust?.defaultCaFiles ?? [];
	const atDefault = simTrust !== undefined && sameCaFiles(files, defaults);
	return (
		<>
			<SectionHeading title="Simulators" hint={hint("devices")} />
			<SettingRows>
				<SettingRow
					name="Simulator root CAs"
					summary="Root certificates every iOS simulator AO boots or claims is made to trust, so HTTPS through a debugging proxy such as Proxyman works inside the app."
					detail={
						<>
							A simulator does not share this Mac's trust store: without the proxy's root CA every HTTPS call fails and
							the app can hang on its splash screen. One PEM or DER file per line, as an absolute path or one starting
							with <code>~/</code>. A file that is not on this Mac is skipped silently, so listing a proxy you do not
							use is harmless; an empty list trusts nothing. A simulator picks up a change the next time AO boots or
							claims it, and a project can name its own files instead.
						</>
					}
					ownership={{ kind: "global-overridable" }}
					timing="live"
					value={atDefault ? "Default" : caFilesCount(files)}
					modified={isFieldDirty("simTrustCaFiles")}
					controlId="simTrustCaFiles"
				>
					<div className="flex flex-col gap-2.5">
						<Textarea
							id="simTrustCaFiles"
							className="min-h-20 max-w-[620px] resize-y font-mono text-[12.5px] leading-relaxed"
							wrap="off"
							placeholder={defaults[0] ?? "~/path/to/root-ca.pem"}
							spellCheck={false}
							value={draft.simTrustCaFiles}
							onChange={(e) => setField("simTrustCaFiles", e.target.value)}
						/>
						{simTrust && <SavedCaFiles caFiles={simTrust.caFiles} found={simTrust.found} />}
						<div className="flex items-center gap-2.5">
							<Button
								type="button"
								variant="ghost"
								size="sm"
								disabled={simTrust === undefined || atDefault}
								onClick={() => setField("simTrustCaFiles", formatCaFileLines(defaults))}
							>
								Restore default
							</Button>
						</div>
					</div>
				</SettingRow>
			</SettingRows>
		</>
	);
}

// Whether each SAVED file is on this Mac right now. It describes the saved list,
// not the draft above it, because only a saved list is what a simulator gets.
function SavedCaFiles({ caFiles, found }: { caFiles: string[]; found: boolean[] }) {
	if (caFiles.length === 0) {
		return <p className="text-[11.5px] text-passive">Saved: no files, so simulators trust nothing extra.</p>;
	}
	return (
		<ul aria-label="Saved root CAs on this Mac" className="flex max-w-[620px] flex-col gap-1">
			{caFiles.map((file, i) => {
				const present = found[i] === true;
				return (
					<li key={`${i}-${file}`} className="flex min-w-0 items-center gap-2 text-[11.5px]">
						{present ? (
							<Check className="h-3 w-3 shrink-0 text-success" aria-hidden="true" />
						) : (
							<Minus className="h-3 w-3 shrink-0 text-passive" aria-hidden="true" />
						)}
						<span className="min-w-0 truncate font-mono text-muted-foreground" title={file}>
							{file}
						</span>
						<span className={present ? "shrink-0 text-success" : "shrink-0 text-passive"}>
							{present ? "Found" : "Not on this Mac, skipped"}
						</span>
					</li>
				);
			})}
		</ul>
	);
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

				{/* Where a work-item reference written in plain text opens - today, a
				    Wiki Tasks row. Every field is optional and an empty one leaves that
				    kind of reference as plain text: the app never guesses a URL. */}
				<SettingRow
					name="Jira address"
					summary="Makes a Jira issue key such as ABC-123 in a task clickable, opening the issue in your browser."
					detail="The site's base URL; the key opens at <address>/browse/<KEY>. Leave it empty to keep keys as plain text."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.refJiraBaseUrl || "Off"}
					modified={isFieldDirty("refJiraBaseUrl")}
					controlId="refJiraBaseUrl"
				>
					<Input
						id="refJiraBaseUrl"
						className="h-8 max-w-[340px] font-mono text-[12.5px]"
						placeholder="https://jira.example.com"
						spellCheck={false}
						value={draft.refJiraBaseUrl}
						onChange={(e) => setField("refJiraBaseUrl", e.target.value)}
					/>
				</SettingRow>

				<SettingRow
					name="GitLab address"
					summary="Makes a merge request reference such as !123 in a task clickable, opening it in your browser."
					detail="The GitLab host's base URL. Every merge request link needs it, together with a default repo or a repo alias. Leave it empty to keep them all as plain text."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.refGitlabBaseUrl || "Off"}
					modified={isFieldDirty("refGitlabBaseUrl")}
					controlId="refGitlabBaseUrl"
				>
					<Input
						id="refGitlabBaseUrl"
						className="h-8 max-w-[340px] font-mono text-[12.5px]"
						placeholder="https://gitlab.example.com"
						spellCheck={false}
						value={draft.refGitlabBaseUrl}
						onChange={(e) => setField("refGitlabBaseUrl", e.target.value)}
					/>
				</SettingRow>

				<SettingRow
					name="Default GitLab repo"
					summary="The project a bare !123 means, when no repo alias is written before it."
					detail="A GitLab project path, group/project. Leave it empty to keep bare references as plain text."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={draft.refGitlabDefaultRepo || "None"}
					modified={isFieldDirty("refGitlabDefaultRepo")}
					controlId="refGitlabDefaultRepo"
				>
					<Input
						id="refGitlabDefaultRepo"
						className="h-8 max-w-[340px] font-mono text-[12.5px]"
						placeholder="group/project"
						spellCheck={false}
						value={draft.refGitlabDefaultRepo}
						onChange={(e) => setField("refGitlabDefaultRepo", e.target.value)}
					/>
				</SettingRow>

				<SettingRow
					name="Repo aliases"
					summary="Short names written before a reference, so XYZ !187 (or XYZ!187) opens in the repo XYZ stands for."
					detail="One per line, as alias = group/project. Names match regardless of case. An uppercase name that is not listed here leaves its reference as plain text rather than sending it to the default repo."
					ownership={{ kind: "global-only" }}
					timing="on-save"
					value={aliasCount(draft.refGitlabAliases)}
					modified={isFieldDirty("refGitlabAliases")}
					controlId="refGitlabAliases"
				>
					<Textarea
						id="refGitlabAliases"
						className="min-h-20 max-w-[340px] resize-y font-mono text-[12.5px] leading-relaxed"
						placeholder="XYZ = group/project"
						spellCheck={false}
						value={draft.refGitlabAliases}
						onChange={(e) => setField("refGitlabAliases", e.target.value)}
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

function aliasCount(lines: string): string {
	const n = lines.split("\n").filter((line) => line.trim() !== "").length;
	return n === 0 ? "None" : `${n} alias${n === 1 ? "" : "es"}`;
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
