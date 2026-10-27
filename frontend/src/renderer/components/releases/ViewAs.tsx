import { Check, ChevronDown, TriangleAlert, Users } from "lucide-react";
import { useState } from "react";
import { MATCH_HINT, MATCH_TAG, pickerPeople, SEARCH_FROM, searchPeople, viewAsState } from "../../lib/releases/people";
import type { Board, BoardPerson } from "../../lib/releases/types";
import { Input } from "../ui/input";
import { Popover, PopoverContent, PopoverTrigger } from "../ui/popover";
import { SimpleTooltip } from "../ui/tooltip";
import { Avatar } from "./Avatar";

function Option({
	person,
	label,
	selected,
	onPick,
}: {
	person: BoardPerson | null;
	label?: string;
	selected: boolean;
	onPick: (id: string | undefined) => void;
}) {
	const tag = person ? MATCH_TAG[person.match] : undefined;
	return (
		<button
			type="button"
			role="option"
			aria-selected={selected}
			className="rel-vopt"
			data-testid="view-as-option"
			data-person={person?.id ?? ""}
			title={person ? MATCH_HINT[person.match] : "Show every card and alert"}
			onClick={() => onPick(person?.id)}
		>
			{person ? <Avatar accountId={person.jiraAccountId} name={person.name} size={18} /> : <Users className="size-4" />}
			<span className="nm">
				{label ?? person?.name ?? "Everyone"}
				{label && person && <small>{person.name}</small>}
			</span>
			{tag && <em className="tag">{tag}</em>}
			{selected && <Check className="size-3.5" aria-hidden="true" />}
		</button>
	);
}

function Trigger({ board, person }: { board: Board; person: string | undefined }) {
	const state = viewAsState(board, person);
	if (state.kind === "unknown")
		return (
			<>
				<TriangleAlert className="size-3.5 text-[var(--amber)]" aria-hidden="true" />
				<b>{state.id}</b>
			</>
		);
	if (state.kind === "person")
		return (
			<>
				<Avatar accountId={state.person.jiraAccountId} name={state.person.name} size={18} />
				<b>{state.person.name}</b>
			</>
		);
	return (
		<>
			<Users className="size-3.5" aria-hidden="true" />
			<b>Everyone</b>
		</>
	);
}

/** Whose Mine the page shows. The Board is fetched per person, so this is a URL choice, not a filter on one Board. */
export function ViewAs({
	board,
	person,
	filtering,
	onPick,
}: {
	board: Board;
	person: string | undefined;
	/** The Board carries this person's Mine, so the page is filtered to it. */
	filtering: boolean;
	onPick: (id: string | undefined) => void;
}) {
	const [open, setOpen] = useState(false);
	const [query, setQuery] = useState("");
	const state = viewAsState(board, person);
	const { self, others } = pickerPeople(board.people);
	const searchable = board.people.length > SEARCH_FROM;
	const shown = searchPeople(others, query);
	const showSelf = self !== null && searchPeople([self], query).length > 0;
	const showEveryone = !query.trim() || "everyone".includes(query.trim().toLowerCase());
	const selectedId = state.kind === "person" ? state.person.id : null;
	const pick = (id: string | undefined) => {
		setOpen(false);
		setQuery("");
		onPick(id);
	};
	const trigger = (
		<button
			type="button"
			className="rel-view"
			data-testid="view-as"
			data-person={person ?? ""}
			data-filtering={filtering}
			data-st={state.kind === "unknown" ? "unknown" : undefined}
			aria-label={`View as ${state.kind === "everyone" ? "everyone" : state.kind === "person" ? state.person.name : state.id}`}
			aria-haspopup="listbox"
			aria-expanded={open}
		>
			<span className="lead">View as</span>
			<Trigger board={board} person={person} />
			<ChevronDown className="size-3 opacity-60" aria-hidden="true" />
		</button>
	);
	return (
		<Popover
			open={open}
			onOpenChange={(o) => {
				setOpen(o);
				if (!o) setQuery("");
			}}
		>
			{state.kind === "unknown" ? (
				<SimpleTooltip label={state.reason}>
					<span>
						<PopoverTrigger asChild>{trigger}</PopoverTrigger>
					</span>
				</SimpleTooltip>
			) : (
				<PopoverTrigger asChild>{trigger}</PopoverTrigger>
			)}
			<PopoverContent align="end" className="rel-vmenu" data-testid="view-as-menu">
				{searchable && (
					<Input
						autoFocus
						value={query}
						onChange={(e) => setQuery(e.target.value)}
						placeholder="Find a person"
						aria-label="Find a person"
						data-testid="view-as-search"
						className="rel-vsearch"
					/>
				)}
				<div role="listbox" aria-label="View as" className="rel-vlist">
					{showEveryone && <Option person={null} selected={state.kind === "everyone"} onPick={pick} />}
					{showSelf && self && <Option person={self} label="Me" selected={selectedId === self.id} onPick={pick} />}
					{shown.map((p) => (
						<Option key={p.id} person={p} selected={selectedId === p.id} onPick={pick} />
					))}
					{!showEveryone && !showSelf && shown.length === 0 && <div className="rel-vnone">Nobody matches.</div>}
				</div>
				{board.identity.kind === "unknown" && (
					<div className="rel-vnote">Me needs your Jira and GitLab accounts: {board.identity.reason}</div>
				)}
			</PopoverContent>
		</Popover>
	);
}
