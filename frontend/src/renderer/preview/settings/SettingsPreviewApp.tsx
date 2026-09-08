import { useMemo, useState } from "react";
import { Check, ChevronDown, FolderGit2, Globe, Moon, Search, Sun } from "lucide-react";
import { Button } from "../../components/ui/button";
import { cn } from "../../lib/utils";
import { SettingRow } from "./SettingRow";
import {
	GLOBAL_SECTIONS_A,
	GLOBAL_SECTIONS_B,
	GLOBAL_SETTINGS,
	PROJECTS,
	PROJECT_SECTIONS_A,
	PROJECT_SECTIONS_B,
	projectSettings,
	type Section,
	type Setting,
} from "./model";
import { AppFrame } from "./AppFrame";
import { TodayShape } from "./TodayShape";

// The preview harness. It is NOT part of the design: the strip along the top only
// exists so Fluke can flip between the structures being compared, switch scope,
// and check both themes without reloading. Everything below the strip is the
// proposal.
//
// B was chosen on 2026-09-08. Today and A are kept in the switcher as the
// comparison whoever implements B will want to see, not as live options.

export type Variant = "today" | "a" | "b";

const VARIANTS: { id: Variant; label: string; blurb: string }[] = [
	{ id: "today", label: "Today", blurb: "what ships now - every explanation expanded, one long column" },
	{
		id: "a",
		label: "A · Same sections, calmer rows",
		blurb: "today's taxonomy, prose folded, scope + timing on every row",
	},
	{
		id: "b",
		label: "B · Re-cut by what it acts on",
		blurb:
			'CHOSEN (2026-09-08) - sections named for what a setting acts on, so the heading answers "where is it" before the chips do',
	},
];

export function SettingsPreviewApp() {
	const [variant, setVariant] = useState<Variant>("b");
	const [scope, setScope] = useState<string>("global");
	const [theme, setTheme] = useState<"dark" | "light">(
		document.documentElement.getAttribute("data-theme") === "light" ? "light" : "dark",
	);

	const toggleTheme = () => {
		const next = theme === "dark" ? "light" : "dark";
		setTheme(next);
		if (next === "light") document.documentElement.setAttribute("data-theme", "light");
		else document.documentElement.removeAttribute("data-theme");
	};

	const project = PROJECTS.find((p) => p.id === scope);
	const settings = project ? projectSettings(project) : GLOBAL_SETTINGS;
	const sections = project
		? variant === "b"
			? PROJECT_SECTIONS_B
			: PROJECT_SECTIONS_A
		: variant === "b"
			? GLOBAL_SECTIONS_B
			: GLOBAL_SECTIONS_A;

	return (
		<div className="flex h-screen min-h-0 flex-col bg-background text-foreground">
			<header className="flex shrink-0 flex-wrap items-center gap-4 border-b border-border-strong bg-surface px-4 py-2.5">
				<span className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-passive">Design preview</span>
				<div className="flex items-center gap-1">
					{VARIANTS.map((v) => (
						<button
							key={v.id}
							type="button"
							onClick={() => setVariant(v.id)}
							title={v.blurb}
							className={cn(
								"!text-[12px] rounded-md px-2.5 py-1 transition-colors",
								variant === v.id
									? "bg-accent-weak !font-semibold text-accent"
									: "text-muted-foreground hover:bg-interactive-hover hover:text-foreground",
							)}
						>
							{v.label}
						</button>
					))}
				</div>
				<div className="ml-auto flex items-center gap-2">
					<ScopePicker scope={scope} onScope={setScope} />
					<Button type="button" variant="outline" size="sm" onClick={toggleTheme}>
						{theme === "dark" ? <Moon className="h-3.5 w-3.5" /> : <Sun className="h-3.5 w-3.5" />}
						{theme === "dark" ? "Dark" : "Light"}
					</Button>
				</div>
				<p className="w-full text-[11.5px] text-passive">{VARIANTS.find((v) => v.id === variant)?.blurb}</p>
			</header>

			<div className="min-h-0 flex-1 overflow-hidden">
				<AppFrame>
					{variant === "today" ? (
						<TodayShape
							settings={settings}
							sections={project ? PROJECT_SECTIONS_A : GLOBAL_SECTIONS_A}
							project={project}
						/>
					) : (
						<SettingsSurface
							key={`${variant}-${scope}`}
							variant={variant}
							settings={settings}
							sections={sections}
							title={project ? "Settings" : "Global settings"}
							subtitle={project ? project.path : "Settings that apply across every project on this Mac"}
							scopeLabel={project ? project.name : "Global settings"}
							isProject={Boolean(project)}
						/>
					)}
				</AppFrame>
			</div>
		</div>
	);
}

function ScopePicker({ scope, onScope }: { scope: string; onScope: (s: string) => void }) {
	return (
		<div className="flex items-center gap-1 rounded-md border border-border bg-background p-0.5">
			<button
				type="button"
				onClick={() => onScope("global")}
				className={cn(
					"!text-[12px] flex items-center gap-1.5 rounded px-2 py-1",
					scope === "global" ? "bg-secondary text-foreground" : "text-muted-foreground hover:text-foreground",
				)}
			>
				<Globe className="h-3.5 w-3.5" /> Global
			</button>
			{PROJECTS.map((p) => (
				<button
					key={p.id}
					type="button"
					onClick={() => onScope(p.id)}
					className={cn(
						"!text-[12px] flex items-center gap-1.5 rounded px-2 py-1",
						scope === p.id ? "bg-secondary text-foreground" : "text-muted-foreground hover:text-foreground",
					)}
				>
					<FolderGit2 className="h-3.5 w-3.5" /> {p.name}
				</button>
			))}
		</div>
	);
}

// The two-pane shell #86/#87 chose, kept deliberately: nav on the left, one
// scrollable column, one save bar. Only what is INSIDE the column changes.
function SettingsSurface({
	variant,
	settings,
	sections,
	title,
	subtitle,
	scopeLabel,
	isProject,
}: {
	variant: Exclude<Variant, "today">;
	settings: Setting[];
	sections: Section[];
	title: string;
	subtitle: string;
	scopeLabel: string;
	isProject: boolean;
}) {
	const [active, setActive] = useState(sections[0].key);
	const [search, setSearch] = useState("");
	const [changedOnly, setChangedOnly] = useState(false);

	const key = variant === "b" ? "b" : "a";
	const counts = useMemo(() => {
		const map: Record<string, number> = {};
		for (const s of settings) map[s[key]] = (map[s[key]] ?? 0) + 1;
		return map;
	}, [settings, key]);
	const changedCount = settings.filter((s) => s.changed).length;

	const q = search.trim().toLowerCase();
	const inSection = settings.filter((s) => s[key] === active);
	const matched = inSection.filter(
		(s) =>
			(!changedOnly || s.changed) &&
			(!q ||
				s.name.toLowerCase().includes(q) ||
				s.summary.toLowerCase().includes(q) ||
				(s.detail ?? "").toLowerCase().includes(q)),
	);
	// A only: today's sections are heterogeneous, so the set-once rows are pushed
	// below a fold to keep the section short. B does not need one - its sections
	// are cut small enough by meaning.
	const common = variant === "a" ? matched.filter((s) => !s.rare) : matched;
	const rare = variant === "a" ? matched.filter((s) => s.rare) : [];
	const section = sections.find((s) => s.key === active)!;

	return (
		<div className="flex h-full min-h-0 flex-col">
			<div className="flex items-center gap-3 px-[18px] pt-[22px]">
				<div className="flex min-w-0 items-baseline gap-3">
					<h1 className="text-[21px] font-bold tracking-[-0.025em] text-foreground">{title}</h1>
					<span className="text-[12.5px] text-passive">{subtitle}</span>
				</div>
			</div>
			<div className="relative grid min-h-0 flex-1 grid-cols-[236px_minmax(0,1fr)]">
				<nav className="flex min-h-0 flex-col gap-2.5 overflow-y-auto border-r border-border px-3 py-1.5">
					<div className="flex flex-col gap-1.5">
						<span className="pl-0.5 font-mono text-[9.5px] uppercase tracking-[0.13em] text-passive">Scope</span>
						<div className="flex h-9 items-center gap-2.5 rounded-lg border border-border-strong bg-surface px-2.5 text-[13px]">
							{isProject ? (
								<FolderGit2 className="h-[15px] w-[15px] text-accent" />
							) : (
								<Globe className="h-[15px] w-[15px] text-muted-foreground" />
							)}
							<span className="min-w-0 flex-1 truncate text-left">{scopeLabel}</span>
							<ChevronDown className="h-3.5 w-3.5 shrink-0 text-passive" />
						</div>
					</div>
					<div className="flex h-8 items-center gap-2 rounded-md border border-border bg-background px-2.5">
						<Search className="h-3.5 w-3.5 shrink-0 text-passive" />
						<input
							aria-label="Search settings"
							value={search}
							onChange={(e) => setSearch(e.target.value)}
							placeholder="Search settings"
							className="min-w-0 flex-1 bg-transparent text-[12.5px] text-foreground placeholder:text-passive focus:outline-none"
						/>
					</div>
					<div className="mt-0.5 flex flex-col gap-0.5">
						{sections.map((s) => {
							const isActive = s.key === active;
							return (
								<button
									key={s.key}
									type="button"
									aria-current={isActive ? "page" : undefined}
									onClick={() => setActive(s.key)}
									className={cn(
										"!text-[13px] relative flex h-8 items-center gap-2.5 rounded-md px-2.5 transition-colors",
										isActive
											? "bg-secondary text-foreground before:absolute before:inset-y-1.5 before:left-0 before:w-0.5 before:rounded-full before:bg-accent before:content-['']"
											: "text-muted-foreground hover:bg-interactive-hover hover:text-foreground",
									)}
								>
									<span className="min-w-0 flex-1 truncate text-left">{s.label}</span>
									<span className="shrink-0 font-mono text-[10.5px] text-passive">{counts[s.key] ?? 0}</span>
								</button>
							);
						})}
					</div>
					{changedCount > 0 && (
						<button
							type="button"
							onClick={() => setChangedOnly((v) => !v)}
							className={cn(
								"!text-[12px] mt-1 flex h-7 items-center gap-2 rounded-md px-2.5 transition-colors",
								changedOnly
									? "bg-accent-weak text-accent"
									: "text-muted-foreground hover:bg-interactive-hover hover:text-foreground",
							)}
						>
							<span className="h-1.5 w-1.5 rounded-full bg-accent" />
							Changed from default
							<span className="ml-auto font-mono text-[10.5px]">{changedCount}</span>
						</button>
					)}
				</nav>

				<section className="min-h-0 overflow-y-auto px-6 pb-24 pt-2">
					{/* Left-aligned rather than centred (the shipping page centres a 680px
					    column): with a nav on the left, centring opens a dead gap the eye has
					    to jump. */}
					<div className="max-w-[760px]">
						<div className="mb-3 flex items-baseline gap-2.5 border-b border-border pb-3 pt-2.5">
							<h2 className="text-[15px] font-semibold tracking-[-0.01em] text-foreground">{section.label}</h2>
							<span className="text-[12px] text-passive">· {section.hint}</span>
						</div>
						<div className="flex flex-col gap-0.5">
							{common.map((s) => (
								<SettingRow key={s.id} setting={s} />
							))}
						</div>
						{matched.length === 0 && <p className="px-3 py-6 text-[12px] text-passive">Nothing here matches that.</p>}
						{rare.length > 0 && <RareFold settings={rare} />}
					</div>
				</section>

				<div className="absolute inset-x-0 bottom-0 left-[236px]">
					<div className="flex h-14 items-center gap-3.5 border-t border-border-strong bg-background px-6 shadow-[0_-8px_24px_rgba(0,0,0,0.35)]">
						<span className="flex shrink-0 items-center gap-2 text-[12px] text-passive">
							<Check className="h-3.5 w-3.5 text-success" />
							All changes saved
						</span>
						<span className="text-[11.5px] text-passive">
							· rows marked <span className="text-warning">Instant, not saved</span> act on click and never wait for
							Save
						</span>
					</div>
				</div>
			</div>
		</div>
	);
}

function RareFold({ settings }: { settings: Setting[] }) {
	const [open, setOpen] = useState(false);
	return (
		<div className="mt-4 border-t border-border pt-3">
			<button
				type="button"
				onClick={() => setOpen((o) => !o)}
				className="!text-[12px] flex items-center gap-2 rounded px-3 py-1.5 text-muted-foreground hover:text-foreground"
			>
				<ChevronDown className={cn("h-3.5 w-3.5 transition-transform", !open && "-rotate-90")} />
				Rarely changed
				<span className="font-mono text-[10.5px] text-passive">{settings.length}</span>
			</button>
			{open && (
				<div className="mt-1 flex flex-col gap-0.5">
					{settings.map((s) => (
						<SettingRow key={s.id} setting={s} />
					))}
				</div>
			)}
		</div>
	);
}
