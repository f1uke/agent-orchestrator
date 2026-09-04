import { useEffect, useState } from "react";
import { ListChecks, RefreshCw, Settings2, BookText, Search, EyeOff } from "lucide-react";
import { MOCK_GROUPS } from "./mock-rows";
import { TaskRowNext, TaskRowToday, type FromTreatment } from "./TaskRowNext";

type Variant = "today" | FromTreatment;

const VARIANTS: { key: Variant; label: string; blurb: string }[] = [
	{ key: "today", label: "Today", blurb: "The row as it ships in #293 — the thing being replaced." },
	{
		key: "smart",
		label: "A · Drop when it repeats",
		blurb:
			"Provenance leaves the sentence always. It comes back as a quiet chip ONLY when it says something the row does not already say — a “(from: My active items)” sitting above “· My active items” is dropped; “(from: chat 2026-04-30, Mobility HQ)” is kept.",
	},
	{
		key: "always",
		label: "B · Always a chip",
		blurb:
			"Provenance leaves the sentence always and always comes back as a chip, even when it repeats the source line. Nothing is ever lost; the duplicate is still on screen, just no longer competing.",
	},
	{
		key: "hover",
		label: "C · Only on hover",
		blurb:
			"The resting row never shows provenance at all. It appears on the meta line when the row is hovered. Calmest at rest; undiscoverable, and the row shifts under the pointer.",
	},
];

const WIDTHS = [380, 452, 560];

export function Preview() {
	const [theme, setTheme] = useState<"dark" | "light">("dark");
	const [left, setLeft] = useState<Variant>("today");
	const [right, setRight] = useState<Variant>("smart");
	const [width, setWidth] = useState(452);
	const [log, setLog] = useState<string[]>([]);

	useEffect(() => {
		document.documentElement.dataset.theme = theme === "light" ? "light" : "dark";
	}, [theme]);

	const say = (line: string) => setLog((current) => [line, ...current].slice(0, 6));

	return (
		<div className="dp">
			<header className="dp__bar">
				<div className="dp__title">
					Wiki › Tasks — row redesign
					<span className="dp__sub">design preview · mock rows · nothing is written to any vault</span>
				</div>
				<div className="dp__controls">
					<Segmented
						label="Theme"
						options={[
							{ key: "dark", label: "Dark" },
							{ key: "light", label: "Light" },
						]}
						value={theme}
						onChange={(next) => setTheme(next as "dark" | "light")}
					/>
					<Segmented
						label="Rail width"
						options={WIDTHS.map((w) => ({ key: String(w), label: `${w}px` }))}
						value={String(width)}
						onChange={(next) => setWidth(Number(next))}
					/>
				</div>
			</header>

			<div className="dp__stage">
				<Rail
					side="left"
					variant={left}
					onVariant={setLeft}
					width={width}
					onEvent={say}
					// Column A defaults to what ships, so the comparison is a
					// comparison rather than a memory test.
				/>
				<Rail side="right" variant={right} onVariant={setRight} width={width} onEvent={say} />
			</div>

			<footer className="dp__log" aria-live="polite">
				<span className="dp__log-title">Where a click would go</span>
				{log.length === 0 ? (
					<span className="dp__log-empty">
						Click a [[wiki link]] in a row, or the quiet line under it. Navigation is not wired in this preview — the
						destination is printed here instead.
					</span>
				) : (
					<ol className="dp__log-list">
						{log.map((line, index) => (
							<li key={`${line}-${index}`}>{line}</li>
						))}
					</ol>
				)}
			</footer>
		</div>
	);
}

function Rail({
	side,
	variant,
	onVariant,
	width,
	onEvent,
}: {
	side: "left" | "right";
	variant: Variant;
	onVariant: (next: Variant) => void;
	width: number;
	onEvent: (line: string) => void;
}) {
	const blurb = VARIANTS.find((entry) => entry.key === variant)?.blurb ?? "";
	return (
		<section className="dp__col">
			<div className="dp__col-head">
				<span className="dp__col-tag">{side === "left" ? "A" : "B"}</span>
				<select
					className="dp__select"
					value={variant}
					aria-label={`Variant shown in column ${side === "left" ? "A" : "B"}`}
					onChange={(event) => onVariant(event.target.value as Variant)}
				>
					{VARIANTS.map((entry) => (
						<option key={entry.key} value={entry.key}>
							{entry.label}
						</option>
					))}
				</select>
			</div>
			<p className="dp__blurb">{blurb}</p>

			<div className="wiki-rail" style={{ width, height: 640 }}>
				<div className="session-inspector__tabs">
					<button type="button" className="session-inspector__tab">
						<span className="session-inspector__tab-icon">
							<BookText aria-hidden="true" />
						</span>
						<span className="session-inspector__tab-label">Notes</span>
					</button>
					<button type="button" className="session-inspector__tab">
						<span className="session-inspector__tab-icon">
							<Search aria-hidden="true" />
						</span>
						<span className="session-inspector__tab-label">Search</span>
					</button>
					<button type="button" className="session-inspector__tab is-active">
						<span className="session-inspector__tab-icon">
							<ListChecks aria-hidden="true" />
						</span>
						<span className="session-inspector__tab-label">Tasks</span>
					</button>
				</div>

				<div className="wiki-rail__summary">
					<span className="wiki-rail__count">6 open · 2 filtered out</span>
					<div className="wiki-rail__actions">
						<button type="button" className="wiki-rail__action" aria-label="Choose which tasks are read">
							<Settings2 aria-hidden="true" />
						</button>
						<button type="button" className="wiki-rail__action" aria-label="Re-read the tasks">
							<RefreshCw aria-hidden="true" />
						</button>
					</div>
				</div>

				<div className="wiki-tasks__filters" role="group" aria-label="Whose tasks to show">
					{["All", "Mine", "Others"].map((option) => (
						<button key={option} type="button" className={`wiki-tasks__filter${option === "All" ? " is-active" : ""}`}>
							{option}
						</button>
					))}
				</div>

				<div className="wiki-tasks__cutoff">
					<EyeOff aria-hidden="true" className="wiki-tasks__cutoff-icon" />
					<span>
						14 rows before 2026-01-01 are hidden. They are still in your notes. 6 rows carry no date of their own, so
						the cutoff leaves them here.
					</span>
					<button type="button" className="wiki-tasks__cutoff-toggle">
						Show them
					</button>
				</div>

				<div className="wiki-rail__tree">
					{MOCK_GROUPS.map((group) => (
						<div
							key={group.key}
							className={`wiki-tasks__group${variant === "today" ? "" : " wiki-tasks__group--next"}`}
						>
							<button type="button" className="wiki-tasks__group-head">
								<span className={`wiki-tasks__group-label${group.overdue ? " is-overdue" : ""}`}>{group.label}</span>
								<span className="wiki-rail__age">{group.rows.length}</span>
							</button>
							{group.rows.map((row) =>
								variant === "today" ? (
									<TaskRowToday
										key={row.id}
										row={row}
										onOpenSource={(path, line) => onEvent(`open note ${path} → scroll to line ${line}`)}
									/>
								) : (
									<TaskRowNext
										key={row.id}
										row={row}
										treatment={variant}
										onOpenWikilink={(target, anchor) =>
											onEvent(`open wikilink [[${target}${anchor ? `#${anchor}` : ""}]]`)
										}
										onOpenSource={(path, line) => onEvent(`open note ${path} → scroll to line ${line}`)}
									/>
								),
							)}
						</div>
					))}
				</div>
			</div>
		</section>
	);
}

function Segmented({
	label,
	options,
	value,
	onChange,
}: {
	label: string;
	options: { key: string; label: string }[];
	value: string;
	onChange: (next: string) => void;
}) {
	return (
		<div className="dp__seg" role="group" aria-label={label}>
			<span className="dp__seg-label">{label}</span>
			{options.map((option) => (
				<button
					key={option.key}
					type="button"
					className={`wiki-tasks__filter${value === option.key ? " is-active" : ""}`}
					aria-pressed={value === option.key}
					onClick={() => onChange(option.key)}
				>
					{option.label}
				</button>
			))}
		</div>
	);
}
