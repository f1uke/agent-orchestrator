import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useState } from "react";
import type { components } from "../../api/schema";
import { workspaceQueryKey } from "../hooks/useWorkspaceQuery";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { ProjectSettingsContent } from "./settings/ProjectSettingsContent";
import { PROJECT_SECTIONS } from "./settings/settings-sections";
import { SettingsSaveBar } from "./settings/SettingsSaveBar";
import { SettingsShell } from "./settings/SettingsShell";
import { useProjectSettingsForm } from "./settings/useProjectSettingsForm";

type Project = components["schemas"]["Project"];

const projectQueryKey = (id: string) => ["project", id] as const;

// ProjectSettingsForm is the Project-scope container: it loads the project, then
// renders the unified two-pane SettingsShell (scope=project) with the four
// project sections and one sticky save bar. All editable state + the save/PUT
// lives in useProjectSettingsForm so every field routes through the single bar.
export function ProjectSettingsForm({ projectId }: { projectId: string }) {
	const queryClient = useQueryClient();

	const query = useQuery({
		queryKey: projectQueryKey(projectId),
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/projects/{id}", {
				params: { path: { id: projectId } },
			});
			if (error) throw new Error(apiErrorMessage(error));
			if (data?.status !== "ok") throw new Error("Project config is unavailable (degraded).");
			return data.project as Project;
		},
	});

	if (query.isLoading) {
		return <CenteredNote>Loading project settings…</CenteredNote>;
	}
	if (query.isError || !query.data) {
		return (
			<CenteredNote>{query.error instanceof Error ? query.error.message : "Could not load project."}</CenteredNote>
		);
	}

	return (
		<ProjectSettingsBody
			key={projectId}
			project={query.data}
			projectId={projectId}
			onSaved={() => queryClient.invalidateQueries({ queryKey: workspaceQueryKey })}
		/>
	);
}

// Project carries no typed provider/host field (see components["schemas"]["Project"]):
// the daemon resolves GitLab vs GitHub server-side from the git origin host; the
// form mirrors that client-side for display via providerFromOrigin (in the hook).
function ProjectSettingsBody({
	project,
	projectId,
	onSaved,
}: {
	project: Project;
	projectId: string;
	onSaved: () => void;
}) {
	const form = useProjectSettingsForm({ project, projectId, onSaved });
	const [activeSection, setActiveSection] = useState<string>("general");
	const [search, setSearch] = useState("");
	const { dirty, mutation, savedAt, replacementError, validationError, submit, discard } = form;

	// Transient save messages travel in the bar so they stay visible regardless of
	// scroll position — identical copy/logic to the pre-redesign form footer.
	const status = (
		<>
			{validationError && <span className="text-[12px] text-error">{validationError}</span>}
			{mutation.isError && (
				<span className="text-[12px] text-error">
					{mutation.error instanceof Error ? mutation.error.message : "Save failed"}
				</span>
			)}
			{savedAt && !mutation.isPending && !mutation.isError && <span className="text-[12px] text-success">Saved.</span>}
			{replacementError && !mutation.isPending && !mutation.isError && (
				<span className="text-[12px] text-warning">Orchestrator restart failed: {replacementError}</span>
			)}
		</>
	);

	return (
		<SettingsShell
			scope="project"
			projectName={project.name}
			title="Settings"
			subtitle={project.path}
			sections={PROJECT_SECTIONS}
			activeSection={activeSection}
			onSelectSection={setActiveSection}
			search={search}
			onSearch={setSearch}
			saveBar={
				<SettingsSaveBar
					dirty={dirty}
					saving={mutation.isPending}
					idleNote={activeSection === "agents" ? "Refresh agents runs immediately — it isn't part of Save" : undefined}
					status={status}
					onDiscard={discard}
					onSave={submit}
				/>
			}
		>
			<ProjectSettingsContent project={project} form={form} activeSection={activeSection} />
		</SettingsShell>
	);
}

function CenteredNote({ children }: { children: React.ReactNode }) {
	return (
		<div className="grid h-full place-items-center bg-background p-6 text-center text-[12px] text-passive">
			{children}
		</div>
	);
}
