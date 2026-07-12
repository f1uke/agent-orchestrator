import { Search } from "lucide-react";
import { cn } from "../../lib/utils";
import { matchesSearch, type SectionMeta, type SettingsScope } from "./settings-sections";
import { SettingsScopeSwitcher } from "./SettingsScopeSwitcher";

// SettingsNav is the left section sub-nav (sanctioned deviation, proposal §9):
// the scope switcher, a search field atop the list, and the section rows. The
// active row uses the app's own selection idiom (raised fill + a 2px accent left
// bar, like sidebar session rows). Search filters the list by section label and
// field-level keywords.
export function SettingsNav({
	scope,
	projectName,
	sections,
	activeSection,
	onSelectSection,
	search,
	onSearch,
}: {
	scope: SettingsScope;
	projectName?: string;
	sections: SectionMeta[];
	activeSection: string;
	onSelectSection: (key: string) => void;
	search: string;
	onSearch: (value: string) => void;
}) {
	const visible = sections.filter((section) => matchesSearch(section, search));
	return (
		<nav className="flex min-h-0 flex-col gap-2.5 overflow-y-auto border-r border-border px-3 py-1.5">
			<SettingsScopeSwitcher scope={scope} projectName={projectName} />
			<div className="flex h-8 items-center gap-2 rounded-md border border-border bg-background px-2.5">
				<Search className="h-3.5 w-3.5 shrink-0 text-passive" aria-hidden="true" />
				<input
					aria-label="Search settings"
					value={search}
					onChange={(event) => onSearch(event.target.value)}
					placeholder="Search settings"
					className="min-w-0 flex-1 bg-transparent text-[12.5px] text-foreground placeholder:text-passive focus:outline-none"
				/>
			</div>
			<div className="mt-0.5 flex flex-col gap-0.5">
				{visible.map((section) => {
					const active = section.key === activeSection;
					const Icon = section.Icon;
					return (
						<button
							key={section.key}
							type="button"
							aria-current={active ? "page" : undefined}
							onClick={() => onSelectSection(section.key)}
							className={cn(
								"relative flex h-8 items-center gap-2.5 rounded-md px-2.5 text-[13px] transition-colors",
								active
									? "bg-secondary text-foreground before:absolute before:inset-y-1.5 before:left-0 before:w-0.5 before:rounded-full before:bg-accent before:content-['']"
									: "text-muted-foreground hover:bg-interactive-hover hover:text-foreground",
							)}
						>
							<Icon className="h-[15px] w-[15px] opacity-85" aria-hidden="true" />
							{section.label}
						</button>
					);
				})}
				{visible.length === 0 && <p className="px-2.5 py-2 text-[12px] text-passive">No matching settings.</p>}
			</div>
		</nav>
	);
}
