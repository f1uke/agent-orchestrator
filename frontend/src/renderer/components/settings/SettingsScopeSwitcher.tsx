import { useNavigate } from "@tanstack/react-router";
import { ChevronDown, FolderGit2, Globe } from "lucide-react";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuLabel,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "../ui/dropdown-menu";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import type { SettingsScope } from "./settings-sections";

// SettingsScopeSwitcher unifies the two settings destinations into one shell
// (sanctioned deviation, proposal §9): the top-of-nav control shows the current
// scope and switches between Global and any registered Project. Switching just
// navigates between the two existing routes, so deep links and the popover
// entries keep working.
export function SettingsScopeSwitcher({ scope, projectName }: { scope: SettingsScope; projectName?: string }) {
	const navigate = useNavigate();
	const workspaces = useWorkspaceQuery().data ?? [];
	const label = scope === "global" ? "Global settings" : (projectName ?? "Project");

	return (
		<div className="flex flex-col gap-1.5">
			<span className="pl-0.5 font-mono text-[9.5px] uppercase tracking-[0.13em] text-passive">Scope</span>
			<DropdownMenu>
				<DropdownMenuTrigger asChild>
					<button
						type="button"
						className="flex h-9 items-center gap-2.5 rounded-lg border border-border-strong bg-surface px-2.5 text-[13px] text-foreground transition-colors hover:bg-raised"
					>
						{scope === "global" ? (
							<Globe className="h-[15px] w-[15px] text-muted-foreground" aria-hidden="true" />
						) : (
							<FolderGit2 className="h-[15px] w-[15px] text-accent" aria-hidden="true" />
						)}
						<span className="min-w-0 flex-1 truncate text-left">{label}</span>
						<ChevronDown className="h-3.5 w-3.5 shrink-0 text-passive" aria-hidden="true" />
					</button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="start" className="w-[var(--radix-dropdown-menu-trigger-width)] min-w-52">
					{workspaces.length > 0 && <DropdownMenuLabel>Project</DropdownMenuLabel>}
					{workspaces.map((workspace) => (
						<DropdownMenuItem
							key={workspace.id}
							onSelect={() =>
								void navigate({ to: "/projects/$projectId/settings", params: { projectId: workspace.id } })
							}
						>
							<FolderGit2 aria-hidden="true" />
							<span className="truncate">{workspace.name}</span>
						</DropdownMenuItem>
					))}
					{workspaces.length > 0 && <DropdownMenuSeparator />}
					<DropdownMenuItem onSelect={() => void navigate({ to: "/settings" })}>
						<Globe aria-hidden="true" />
						Global settings
					</DropdownMenuItem>
				</DropdownMenuContent>
			</DropdownMenu>
		</div>
	);
}
