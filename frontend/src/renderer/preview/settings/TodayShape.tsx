import { useState } from "react";
import { Check, ChevronDown, FolderGit2, Globe, Search } from "lucide-react";
import { Button } from "../../components/ui/button";
import { Input } from "../../components/ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "../../components/ui/select";
import { Switch } from "../../components/ui/switch";
import { Textarea } from "../../components/ui/textarea";
import { cn } from "../../lib/utils";
import type { PreviewProject, Section, Setting } from "./model";

// TodayShape is the BEFORE, drawn from the same catalog as the two proposals so
// the comparison is honest: identical settings, identical copy, laid out the way
// the page lays them out now - one card per setting, every explanation expanded,
// no statement of scope or of when the change lands. It is here to be compared
// against, not to be chosen.
export function TodayShape({
	settings,
	sections,
	project,
}: {
	settings: Setting[];
	sections: Section[];
	project?: PreviewProject;
}) {
	const [active, setActive] = useState(sections[0].key);
	const section = sections.find((s) => s.key === active)!;
	const rows = settings.filter((s) => s.a === active);

	return (
		<div className="flex h-full min-h-0 flex-col">
			<div className="flex items-center gap-3 px-[18px] pt-[22px]">
				<div className="flex min-w-0 items-baseline gap-3">
					<h1 className="text-[21px] font-bold tracking-[-0.025em] text-foreground">
						{project ? "Settings" : "Global settings"}
					</h1>
					<span className="text-[12.5px] text-passive">
						{project ? project.path : "Settings that apply across all projects"}
					</span>
				</div>
			</div>
			<div className="relative grid min-h-0 flex-1 grid-cols-[218px_minmax(0,1fr)]">
				<nav className="flex min-h-0 flex-col gap-2.5 overflow-y-auto border-r border-border px-3 py-1.5">
					<div className="flex flex-col gap-1.5">
						<span className="pl-0.5 font-mono text-[9.5px] uppercase tracking-[0.13em] text-passive">Scope</span>
						<div className="flex h-9 items-center gap-2.5 rounded-lg border border-border-strong bg-surface px-2.5 text-[13px]">
							{project ? (
								<FolderGit2 className="h-[15px] w-[15px] text-accent" />
							) : (
								<Globe className="h-[15px] w-[15px] text-muted-foreground" />
							)}
							<span className="min-w-0 flex-1 truncate text-left">{project ? project.name : "Global settings"}</span>
							<ChevronDown className="h-3.5 w-3.5 shrink-0 text-passive" />
						</div>
					</div>
					<div className="flex h-8 items-center gap-2 rounded-md border border-border bg-background px-2.5">
						<Search className="h-3.5 w-3.5 shrink-0 text-passive" />
						<span className="text-[12.5px] text-passive">Search settings</span>
					</div>
					<div className="mt-0.5 flex flex-col gap-0.5">
						{sections.map((s) => (
							<button
								key={s.key}
								type="button"
								onClick={() => setActive(s.key)}
								className={cn(
									"!text-[13px] relative flex h-8 items-center gap-2.5 rounded-md px-2.5 transition-colors",
									s.key === active
										? "bg-secondary text-foreground before:absolute before:inset-y-1.5 before:left-0 before:w-0.5 before:rounded-full before:bg-accent before:content-['']"
										: "text-muted-foreground hover:bg-interactive-hover hover:text-foreground",
								)}
							>
								{s.label}
							</button>
						))}
					</div>
				</nav>

				<section className="min-h-0 overflow-y-auto px-6 pb-24 pt-2">
					<div className="mx-auto max-w-[680px]">
						<div className="mb-4 flex items-center gap-2.5 border-b border-border pb-3 pt-2.5">
							<h2 className="text-[15px] font-semibold tracking-[-0.01em] text-foreground">{section.label}</h2>
							<span className="text-[12px] font-normal text-passive">· {section.hint}</span>
						</div>
						{rows.map((s) => (
							<TodayCard key={s.id} setting={s} />
						))}
					</div>
				</section>

				<div className="absolute inset-x-0 bottom-0 left-[218px]">
					<div className="flex h-14 items-center gap-3.5 border-t border-border-strong bg-background px-6 shadow-[0_-8px_24px_rgba(0,0,0,0.35)]">
						<span className="flex shrink-0 items-center gap-2 text-[12px] text-passive">
							<Check className="h-3.5 w-3.5 text-success" />
							All changes saved
						</span>
					</div>
				</div>
			</div>
		</div>
	);
}

function TodayCard({ setting }: { setting: Setting }) {
	const c = setting.control;
	return (
		<section className="mb-4 rounded-xl border border-border bg-card p-4">
			<h3 className="mb-3.5 text-[13px] font-semibold text-foreground">{setting.name}</h3>
			<div className="flex flex-col gap-3.5">
				<p className="text-[12px] leading-5 text-muted-foreground">
					{setting.summary} {setting.detail}
				</p>
				{c.type === "toggle" && (
					<div className="flex items-center gap-3">
						<Switch checked={c.value} />
						<span className="text-[12px] text-muted-foreground">Enabled</span>
					</div>
				)}
				{c.type === "select" && (
					<Select value={c.value}>
						<SelectTrigger className="h-8 w-full text-[13px]">
							<SelectValue />
						</SelectTrigger>
						<SelectContent>
							{c.options.map((o) => (
								<SelectItem key={o} value={o}>
									{o}
								</SelectItem>
							))}
						</SelectContent>
					</Select>
				)}
				{c.type === "text" && (
					<Input className="h-8 text-[12.5px]" defaultValue={c.value} placeholder={c.placeholder} />
				)}
				{c.type === "number" && <Input type="number" className="h-8 text-[12.5px]" defaultValue={c.value} />}
				{c.type === "editor" && (
					<Textarea defaultValue={c.value} className="min-h-[120px] resize-y font-mono text-[12px] leading-relaxed" />
				)}
				{c.type === "action" && (
					<div className="flex items-center gap-3">
						<Button type="button" variant="outline">
							{c.label}
						</Button>
						{c.note && <span className="text-[12px] text-muted-foreground">{c.note}</span>}
					</div>
				)}
				{c.type === "readonly" && (
					<div className="flex flex-col gap-1 font-mono text-[12px] text-foreground">
						{c.value.split("\n").map((line) => (
							<span key={line}>{line}</span>
						))}
					</div>
				)}
			</div>
		</section>
	);
}
