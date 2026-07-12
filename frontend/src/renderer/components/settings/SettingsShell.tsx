import type { ReactNode } from "react";
import { DashboardSubhead } from "../DashboardSubhead";
import { SettingsNav } from "./SettingsNav";
import type { SectionMeta, SettingsScope } from "./settings-sections";

// SettingsShell is the shared two-pane frame both scopes render into (Direction
// A, proposal §4): the app subhead, a left section sub-nav (scope switcher +
// search + sections), a scrollable content pane, and one sticky save bar pinned
// to the bottom of the content column. It is presentational/controlled — the
// scope container owns section + search + save state and passes them down.
export function SettingsShell({
	scope,
	projectName,
	title,
	subtitle,
	sections,
	activeSection,
	onSelectSection,
	search,
	onSearch,
	saveBar,
	children,
}: {
	scope: SettingsScope;
	projectName?: string;
	title: string;
	subtitle: string;
	sections: SectionMeta[];
	activeSection: string;
	onSelectSection: (key: string) => void;
	search: string;
	onSearch: (value: string) => void;
	saveBar?: ReactNode;
	children: ReactNode;
}) {
	return (
		<div className="flex h-full min-h-0 flex-col bg-background text-foreground">
			<DashboardSubhead title={title} subtitle={subtitle} />
			<div className="relative grid min-h-0 flex-1 grid-cols-[218px_minmax(0,1fr)]">
				<SettingsNav
					scope={scope}
					projectName={projectName}
					sections={sections}
					activeSection={activeSection}
					onSelectSection={onSelectSection}
					search={search}
					onSearch={onSearch}
				/>
				<section className="min-h-0 overflow-y-auto px-6 pb-24 pt-2">
					<div className="mx-auto max-w-[680px]">{children}</div>
				</section>
				{saveBar && <div className="absolute inset-x-0 bottom-0 left-[218px]">{saveBar}</div>}
			</div>
		</div>
	);
}
