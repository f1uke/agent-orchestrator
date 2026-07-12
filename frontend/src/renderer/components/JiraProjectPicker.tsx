import { useEffect, useRef, useState } from "react";
import { Check, ChevronDown, Folder, Loader2, Search, Star } from "lucide-react";
import { useJiraProjects, type JiraProject } from "../hooks/useSessionJiraContext";
import { readStarredProjects, writeStarredProjects } from "../lib/jira-starred-projects";
import { SimpleTooltip, TooltipProvider } from "./ui/tooltip";
import { cn } from "../lib/utils";

/**
 * A searchable dropdown of the user's real Jira projects, read LIVE via REST
 * (`/rest/api/3/project/search`). Browse Jira makes you pick a project FIRST, then
 * search issues within it. The last pick is remembered (see jira-last-project) and
 * marked with a "Last used" chip so a return trip lands where you left off. Each
 * row has a star toggle; starred projects pin to a "Starred" group at the top
 * (persisted to localStorage — see jira-starred-projects). A fetch failure (e.g.
 * no JIRA_API_TOKEN) surfaces inline rather than an empty list.
 */
export function JiraProjectPicker({
	value,
	onSelect,
	lastUsedKey,
}: {
	value: JiraProject | null;
	onSelect: (project: JiraProject) => void;
	lastUsedKey?: string;
}) {
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const [debounced, setDebounced] = useState("");
	const [starred, setStarred] = useState<JiraProject[]>(() => readStarredProjects());
	const containerRef = useRef<HTMLDivElement>(null);

	// Debounce the filter so we don't fan out a request per keystroke.
	useEffect(() => {
		const t = setTimeout(() => setDebounced(query), 250);
		return () => clearTimeout(t);
	}, [query]);

	// Close on an outside click (mirrors BranchCombobox's idiom).
	useEffect(() => {
		if (!open) return;
		const onPointerDown = (event: MouseEvent) => {
			if (containerRef.current && !containerRef.current.contains(event.target as Node)) setOpen(false);
		};
		document.addEventListener("mousedown", onPointerDown);
		return () => document.removeEventListener("mousedown", onPointerDown);
	}, [open]);

	// Only fetch while the dropdown is open.
	const { data, isFetching, isError, error } = useJiraProjects(debounced, open);
	const projects = data ?? [];
	const starredKeys = new Set(starred.map((project) => project.key));

	const pick = (project: JiraProject) => {
		onSelect(project);
		setOpen(false);
		setQuery("");
	};

	// Toggle a project's star; starring re-partitions the list so it jumps to the
	// pinned "Starred" group immediately, and the favorites persist to localStorage
	// (with the name, so the group can render it later even off the fetched page).
	const toggleStar = (project: JiraProject) => {
		const next = starredKeys.has(project.key)
			? starred.filter((entry) => entry.key !== project.key)
			: [...starred, { key: project.key, name: project.name }];
		writeStarredProjects(next);
		setStarred(next);
	};

	// The Starred group is sourced from the PERSISTED favorites — not the fetched
	// page — so a favorite always pins to the top even when its project isn't in the
	// current fetch (the list is capped at 100 by key order). Each is resolved to
	// the freshest fetched project (for an up-to-date name) when present, and the
	// group is narrowed by the active query just like the fetched list.
	const projectsByKey = new Map(projects.map((project) => [project.key, project]));
	const q = debounced.trim().toLowerCase();
	const matchesQuery = (project: JiraProject) =>
		!q || project.key.toLowerCase().includes(q) || (project.name ?? "").toLowerCase().includes(q);
	const starredList = starred.map((project) => projectsByKey.get(project.key) ?? project).filter(matchesQuery);
	const otherList = projects.filter((project) => !starredKeys.has(project.key));

	const renderOption = (project: JiraProject) => {
		const selected = value?.key === project.key;
		const isStarred = starredKeys.has(project.key);
		return (
			<div key={project.key} className={cn("jira-proj-picker__item", selected && "is-selected")}>
				<SimpleTooltip label={isStarred ? "Remove from favorites" : "Pin to the top"}>
					<button
						type="button"
						className={cn("jira-proj-picker__star", isStarred && "is-on")}
						aria-pressed={isStarred}
						aria-label={isStarred ? `Unstar ${project.key}` : `Star ${project.key}`}
						onClick={() => toggleStar(project)}
					>
						<Star className="size-3.5" aria-hidden="true" />
					</button>
				</SimpleTooltip>
				<button
					type="button"
					role="option"
					aria-selected={selected}
					className="jira-proj-picker__opt"
					onClick={() => pick(project)}
				>
					<span className="jira-proj-picker__opt-k">{project.key}</span>
					<span className="jira-proj-picker__opt-n">{project.name || project.key}</span>
					{lastUsedKey && lastUsedKey === project.key ? (
						<span className="jira-proj-picker__lastused">Last used</span>
					) : null}
					{selected ? <Check className="jira-proj-picker__chk size-3.5" aria-hidden="true" /> : null}
				</button>
			</div>
		);
	};

	return (
		<TooltipProvider delayDuration={0}>
			<div className="jira-proj-picker" ref={containerRef}>
				<SimpleTooltip label={value ? "Change the Jira project" : "Choose a Jira project"}>
					<button
						type="button"
						className="jira-proj-picker__trigger"
						aria-haspopup="listbox"
						aria-expanded={open}
						onClick={() => setOpen((o) => !o)}
					>
						<Folder className="jira-proj-picker__fol size-3.5" aria-hidden="true" />
						{value ? (
							<>
								<span className="jira-proj-picker__k">{value.key}</span>
								{value.name ? <span className="jira-proj-picker__n">· {value.name}</span> : null}
							</>
						) : (
							<span className="jira-proj-picker__placeholder">Select a project</span>
						)}
						<ChevronDown className="jira-proj-picker__car size-3" aria-hidden="true" />
					</button>
				</SimpleTooltip>

				{open ? (
					<div className="jira-proj-picker__drop">
						<div className="jira-proj-picker__search">
							<Search className="size-3.5 text-passive" aria-hidden="true" />
							<input
								autoFocus
								value={query}
								placeholder="Filter your Jira projects…"
								autoComplete="off"
								autoCapitalize="none"
								spellCheck={false}
								onChange={(event) => setQuery(event.target.value)}
								onKeyDown={(event) => {
									if (event.key === "Escape") setOpen(false);
								}}
							/>
							{isFetching ? <Loader2 className="size-3.5 animate-spin text-passive" aria-hidden="true" /> : null}
						</div>

						<div className="jira-proj-picker__list" role="listbox" aria-label="Jira projects">
							{isError ? (
								<p className="jira-proj-picker__note jira-proj-picker__note--err">
									{error instanceof Error ? error.message : "Couldn't load projects."}
								</p>
							) : isFetching && starredList.length === 0 && otherList.length === 0 ? (
								<p className="jira-proj-picker__note">Loading projects…</p>
							) : starredList.length === 0 && otherList.length === 0 ? (
								<p className="jira-proj-picker__note">No matching projects.</p>
							) : (
								<>
									{starredList.length > 0 ? (
										<>
											<div className="jira-proj-picker__group">Starred</div>
											{starredList.map(renderOption)}
											{otherList.length > 0 ? <div className="jira-proj-picker__group">All projects</div> : null}
										</>
									) : null}
									{otherList.map(renderOption)}
								</>
							)}
						</div>

						<div className="jira-proj-picker__foot">
							{projects.length} {projects.length === 1 ? "project" : "projects"} · ★ star to pin · remembers your last
							pick
						</div>
					</div>
				) : null}
			</div>
		</TooltipProvider>
	);
}
