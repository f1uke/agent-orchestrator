import { useEffect, useState } from "react";
import type { WikiTasksSettings } from "../hooks/useWiki";

/**
 * What the Tasks tab reads, and what it counts as yours.
 *
 * 🗝 Every field here is the READER's vocabulary, and this app ships none of
 * it. There is no default folder, no default section name and no default
 * person: a vault's task convention belongs to whoever writes the vault, and
 * a default baked in here would work for exactly one person's notes.
 *
 * It lives in the tab rather than in Global Settings because these are the
 * controls whose effect you want to see the moment you change them — the list
 * behind the form is the preview.
 */
export function WikiTasksSettingsForm({
	settings,
	saving,
	error,
	onSave,
	onCancel,
}: {
	settings: WikiTasksSettings | undefined;
	saving: boolean;
	error: string | null;
	onSave: (next: WikiTasksSettings) => Promise<void>;
	/** Absent when nothing is configured yet: there is no list to go back to. */
	onCancel?: () => void;
}) {
	const [folders, setFolders] = useState((settings?.folders ?? []).join(", "));
	const [sections, setSections] = useState((settings?.sections ?? []).join(", "));
	const [cutoff, setCutoff] = useState(settings?.cutoff ?? "");
	const [aliases, setAliases] = useState((settings?.ownerAliases ?? []).join(", "));
	const [requireCreated, setRequireCreated] = useState(settings?.requireCreated === true);

	// The saved values arrive after the first render, so the draft has to pick
	// them up — but only while it is untouched, or typing would be overwritten
	// by the query settling.
	const [touched, setTouched] = useState(false);
	useEffect(() => {
		if (touched || !settings) return;
		setFolders((settings.folders ?? []).join(", "));
		setSections((settings.sections ?? []).join(", "));
		setCutoff(settings.cutoff ?? "");
		setAliases((settings.ownerAliases ?? []).join(", "));
		setRequireCreated(settings.requireCreated === true);
	}, [settings, touched]);

	const edit =
		<T,>(set: (value: T) => void) =>
		(value: T) => {
			setTouched(true);
			set(value);
		};

	return (
		<form
			className="wiki-tasks__form"
			onSubmit={(event) => {
				event.preventDefault();
				void onSave({
					folders: splitList(folders),
					sections: splitList(sections),
					cutoff: cutoff.trim(),
					ownerAliases: splitList(aliases),
					requireCreated,
				});
			}}
		>
			<p className="wiki-tasks__form-intro">
				The Tasks tab reads the unchecked <code>- [ ]</code> rows in the folders you name below. Nothing outside them is
				read.
			</p>

			<label className="wiki-tasks__field">
				<span className="wiki-tasks__label">Folders</span>
				<input
					className="wiki-tasks__input"
					placeholder="Areas, Projects"
					spellCheck={false}
					value={folders}
					onChange={(event) => edit(setFolders)(event.target.value)}
				/>
				<span className="wiki-tasks__hint">
					Comma-separated folders inside the vault, written the way their paths are. Leave it empty and the tab reads
					nothing.
				</span>
			</label>

			<label className="wiki-tasks__field">
				<span className="wiki-tasks__label">Sections</span>
				<input
					className="wiki-tasks__input"
					placeholder="every section"
					spellCheck={false}
					value={sections}
					onChange={(event) => edit(setSections)(event.target.value)}
				/>
				<span className="wiki-tasks__hint">
					Comma-separated <code>##</code> headings. Only rows under one of them are listed. Empty means every section.
				</span>
			</label>

			<label className="wiki-tasks__field">
				<span className="wiki-tasks__label">Start from</span>
				<input
					className="wiki-tasks__input"
					type="date"
					value={cutoff}
					onChange={(event) => edit(setCutoff)(event.target.value)}
				/>
				{/*
				 * Which date this is judged by is the checkbox below's business,
				 * so the hint reads the checkbox rather than describing one rule
				 * while the tab applies the other.
				 */}
				<span className="wiki-tasks__hint">
					Rows older than this are hidden from the list — never changed and never deleted.{" "}
					{requireCreated ? (
						<>
							A row's date is its <code>created:</code> field, and only that.
						</>
					) : (
						<>
							A row's date is its <code>created:</code> field, or the date in its <code>(from: …)</code> tag; a row with
							neither has no date and stays.
						</>
					)}{" "}
					The tab says how many of each, and shows the hidden ones on request.
				</span>
			</label>

			{/*
			 * The one control here that can hide a lot of work at once, so it
			 * says what it will cost before it is used rather than after. It is
			 * off unless the reader turns it on, and it still only hides: the
			 * count stays on screen and “Show them” brings the rows back.
			 */}
			<label className="wiki-tasks__check">
				<input
					type="checkbox"
					className="wiki-tasks__checkbox"
					checked={requireCreated}
					onChange={(event) => edit(setRequireCreated)(event.target.checked)}
				/>
				<span className="wiki-tasks__check-body">
					<span className="wiki-tasks__label">Only rows with a “created:” date</span>
					<span className="wiki-tasks__hint">
						Judge “Start from” by <code>created:</code> alone, and hide every row that does not carry one. For a vault
						where <code>created:</code> is written as rows are captured, an untagged row is old rather than undatable.
						Leave it off while you are still adding the field — it hides everything not yet tagged, which at the start
						is almost everything.
					</span>
				</span>
			</label>

			<label className="wiki-tasks__field">
				<span className="wiki-tasks__label">Names that mean you</span>
				<input
					className="wiki-tasks__input"
					placeholder="your name in the vault"
					spellCheck={false}
					value={aliases}
					onChange={(event) => edit(setAliases)(event.target.value)}
				/>
				<span className="wiki-tasks__hint">
					Comma-separated. A row starting with <code>[@Name]</code> or <code>@name</code> belongs to that person; “Mine”
					shows the rows naming one of these, plus every row that names nobody.
				</span>
			</label>

			{error && <p className="wiki-tasks__form-error">{error}</p>}

			<div className="wiki-tasks__form-actions">
				{onCancel && (
					<button type="button" className="wiki-tasks__button" onClick={onCancel} disabled={saving}>
						Cancel
					</button>
				)}
				<button type="submit" className="wiki-tasks__button wiki-tasks__button--primary" disabled={saving}>
					{saving ? "Saving…" : "Save"}
				</button>
			</div>
		</form>
	);
}

/**
 * Splits a comma-separated field, dropping blanks.
 *
 * Entries are NOT lowercased or otherwise rewritten: a section heading and a
 * person's name are theirs to spell, and the matching is case-insensitive
 * where it happens rather than by mangling what was typed.
 */
function splitList(raw: string): string[] {
	return raw
		.split(",")
		.map((entry) => entry.trim())
		.filter((entry) => entry !== "");
}
