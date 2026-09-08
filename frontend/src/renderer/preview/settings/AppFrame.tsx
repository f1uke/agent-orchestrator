import { ChevronDown, FolderGit2, Settings2, SquareKanban } from "lucide-react";
import { PROJECTS } from "./model";

// A static stand-in for the app's own chrome (16rem sidebar + topbar), so the
// settings surface is seen at the width it really has rather than floating in a
// full browser window. Nothing here is part of the redesign and nothing in it
// works - it exists only so the proportions are honest.
export function AppFrame({ children }: { children: React.ReactNode }) {
	return (
		<div className="flex h-full min-h-0">
			<aside className="flex w-64 shrink-0 flex-col border-r border-border bg-sidebar">
				<div className="flex h-14 shrink-0 items-center gap-2.5 px-4">
					<span className="grid h-6 w-6 place-items-center rounded-md bg-accent-weak font-mono text-[11px] font-bold text-accent">
						AO
					</span>
					<span className="text-[13px] font-semibold text-foreground">Agent Orchestrator</span>
				</div>
				<div className="flex flex-col gap-0.5 px-2">
					<span className="px-2 pb-1 pt-2 font-mono text-[9.5px] uppercase tracking-[0.13em] text-passive">
						Projects
					</span>
					{PROJECTS.map((p) => (
						<div key={p.id} className="flex h-8 items-center gap-2 rounded-md px-2 text-[13px] text-muted-foreground">
							<ChevronDown className="h-3.5 w-3.5 text-passive" aria-hidden="true" />
							<FolderGit2 className="h-[15px] w-[15px] text-passive" aria-hidden="true" />
							<span className="truncate">{p.name}</span>
						</div>
					))}
				</div>
				<div className="mt-auto flex flex-col gap-0.5 border-t border-border p-2">
					<div className="flex h-8 items-center gap-2 rounded-md px-2 text-[13px] text-muted-foreground">
						<SquareKanban className="h-[15px] w-[15px] text-passive" aria-hidden="true" />
						Board
					</div>
					<div className="flex h-8 items-center gap-2 rounded-md bg-secondary px-2 text-[13px] text-foreground">
						<Settings2 className="h-[15px] w-[15px]" aria-hidden="true" />
						Settings
					</div>
				</div>
			</aside>
			<div className="min-w-0 flex-1">{children}</div>
		</div>
	);
}
