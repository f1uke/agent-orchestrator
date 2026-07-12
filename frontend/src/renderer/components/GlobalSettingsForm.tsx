import { useState } from "react";
import { GlobalSettingsContent } from "./settings/GlobalSettingsContent";
import { GLOBAL_SECTIONS } from "./settings/settings-sections";
import { SettingsSaveBar } from "./settings/SettingsSaveBar";
import { SettingsShell } from "./settings/SettingsShell";
import { useGlobalSettingsForm } from "./settings/useGlobalSettingsForm";

// GlobalSettingsForm is the Global-scope container: the unified two-pane
// SettingsShell (scope=global) with the four global sections — Prompts, Messages,
// Automation, System — and one sticky save bar. Every editable setting (system
// prompts, message templates, confirm-before-spawn, auto-send, auto-reclaim,
// update channel) routes through useGlobalSettingsForm's single dirty/save model
// (locked decision 3); its save FANS OUT across the several daemon + bridge
// endpoints. True actions (Send test, Check for updates, Run migration) live in
// the System section and fire instantly, separate from Save.
export function GlobalSettingsForm() {
	const form = useGlobalSettingsForm();
	const [activeSection, setActiveSection] = useState<string>("prompts");
	const [search, setSearch] = useState("");
	const { dirty, mutation, savedAt, save, discard } = form;

	const status = (
		<>
			{mutation.isError && (
				<span className="text-[12px] text-error">
					{mutation.error instanceof Error ? mutation.error.message : "Save failed"}
				</span>
			)}
			{savedAt && !mutation.isPending && !mutation.isError && <span className="text-[12px] text-success">Saved.</span>}
		</>
	);

	return (
		<SettingsShell
			scope="global"
			title="Global settings"
			subtitle="Settings that apply across all projects"
			sections={GLOBAL_SECTIONS}
			activeSection={activeSection}
			onSelectSection={setActiveSection}
			search={search}
			onSearch={setSearch}
			saveBar={
				<SettingsSaveBar
					dirty={dirty}
					saving={mutation.isPending}
					idleNote={
						activeSection === "system"
							? "Send test, Check for updates & Run migration run immediately — they aren't part of Save"
							: undefined
					}
					status={status}
					onDiscard={discard}
					onSave={save}
				/>
			}
		>
			<GlobalSettingsContent form={form} activeSection={activeSection} />
		</SettingsShell>
	);
}
