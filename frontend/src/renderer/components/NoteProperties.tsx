import { type KeyboardEvent, useEffect, useRef, useState } from "react";
import { Calendar, Hash, List, Lock, Plus, SquareCheck, Type } from "lucide-react";
import { formatDate, type NoteProperty, type PropertyType } from "../lib/note/frontmatter";

/**
 * A note's YAML frontmatter, shown and edited the way Obsidian shows it: a
 * Properties list under the title rather than a block of syntax the reader has
 * to decode — or, as it was until now, nothing at all.
 *
 * 🗝 Editing here is a SECOND write path, and it follows the same rule as the
 * body's: one key's value is spliced back into the file and every other byte —
 * the other keys, their order, their comments, their quoting, and the whole
 * body — is left exactly as it was. `lib/note/frontmatter.ts` owns that, and
 * owns the list of YAML shapes it refuses to rewrite. A property it refuses is
 * drawn with a lock and cannot be opened.
 *
 * The type beside each key is INFERRED FOR DISPLAY: it picks the icon and reads
 * a date back in the reader's order. Nothing here converts a value's stored
 * form because of it — opening a date for editing shows the date as the file
 * spells it.
 */

const ICONS: Record<PropertyType, typeof Type> = {
	text: Type,
	date: Calendar,
	list: List,
	number: Hash,
	checkbox: SquareCheck,
};

export type NotePropertiesProps = {
	properties: NoteProperty[];
	/** True while a write is in flight: nothing new opens until it lands. */
	busy: boolean;
	onEdit: (property: NoteProperty, values: string[]) => void;
	onAdd: (key: string, value: string) => void;
	/** Set when the last add was refused — a duplicate key, or an illegal name. */
	addError?: string;
	onDismissAddError: () => void;
};

export function NoteProperties({ properties, busy, onEdit, onAdd, addError, onDismissAddError }: NotePropertiesProps) {
	const [openKey, setOpenKey] = useState<string | null>(null);
	const [adding, setAdding] = useState(false);

	// A refused add keeps the form open so the reader can fix the name rather
	// than retype the whole thing.
	useEffect(() => {
		if (addError) setAdding(true);
	}, [addError]);

	return (
		<div className="note-props">
			<div className="note-props__head">Properties</div>
			<div className="note-props__rows">
				{properties.map((property) => (
					<PropertyRow
						key={`${property.key}:${property.start}`}
						property={property}
						open={openKey === property.key}
						busy={busy}
						onOpen={() => setOpenKey(property.key)}
						onClose={() => setOpenKey(null)}
						onCommit={(values) => {
							setOpenKey(null);
							onEdit(property, values);
						}}
					/>
				))}
				{properties.length === 0 && !adding && (
					<div className="note-props__empty">This note has no properties yet.</div>
				)}
			</div>

			{adding ? (
				<AddProperty
					busy={busy}
					error={addError}
					onCancel={() => {
						setAdding(false);
						onDismissAddError();
					}}
					onAdd={(key, value) => {
						setAdding(false);
						onDismissAddError();
						onAdd(key, value);
					}}
				/>
			) : (
				<button type="button" className="note-props__add" disabled={busy} onClick={() => setAdding(true)}>
					<Plus aria-hidden="true" />
					Add property
				</button>
			)}
		</div>
	);
}

function PropertyRow({
	property,
	open,
	busy,
	onOpen,
	onClose,
	onCommit,
}: {
	property: NoteProperty;
	open: boolean;
	busy: boolean;
	onOpen: () => void;
	onClose: () => void;
	onCommit: (values: string[]) => void;
}) {
	const Icon = ICONS[property.type];
	return (
		<div className="note-props__row">
			<span className="note-props__icon" aria-hidden="true">
				{property.editable ? <Icon /> : <Lock />}
			</span>
			<span className="note-props__key">{property.key}</span>
			{open ? (
				<PropertyEditor property={property} onCommit={onCommit} onCancel={onClose} />
			) : (
				<PropertyValue property={property} busy={busy} onOpen={onOpen} />
			)}
		</div>
	);
}

/** A property's value at rest: chips for a list, formatted text for a date. */
function PropertyValue({ property, busy, onOpen }: { property: NoteProperty; busy: boolean; onOpen: () => void }) {
	const body =
		property.type === "list" ? (
			property.values.length === 0 ? (
				<span className="note-props__unset">Empty</span>
			) : (
				property.values.map((value, index) => (
					<span className="note-props__chip" key={index}>
						{value}
					</span>
				))
			)
		) : property.values[0] === "" ? (
			<span className="note-props__unset">Empty</span>
		) : (
			<span>{property.type === "date" ? formatDate(property.values[0]) : property.values[0]}</span>
		);

	if (!property.editable) {
		return (
			<span
				className="note-props__value note-props__value--locked"
				title={`Read-only: this value is ${property.readOnlyReason}.`}
			>
				{property.values[0] || <span className="note-props__unset">Empty</span>}
			</span>
		);
	}
	return (
		<button
			type="button"
			className="note-props__value note-props__value--editable"
			disabled={busy}
			title="Click to edit"
			onClick={onOpen}
		>
			{body}
		</button>
	);
}

/**
 * One property, open.
 *
 * A list is edited as its items joined by commas, which is how a vault writes
 * `tags: [a, b]` anyway; committing splits it back and the writer rebuilds the
 * list in whichever shape — flow or block — the file already used.
 */
function PropertyEditor({
	property,
	onCommit,
	onCancel,
}: {
	property: NoteProperty;
	onCommit: (values: string[]) => void;
	onCancel: () => void;
}) {
	// The value as the FILE spells it, not as the row displayed it: a date opens
	// on its stored form, because that is what gets written back.
	const initial = property.type === "list" ? property.values.join(", ") : (property.values[0] ?? "");
	const [text, setText] = useState(initial);
	const input = useRef<HTMLInputElement | null>(null);
	const settled = useRef(false);

	useEffect(() => {
		input.current?.focus();
		input.current?.select();
	}, []);

	const parsed = () =>
		property.type === "list"
			? text
					.split(",")
					.map((part) => part.trim())
					.filter((part) => part !== "")
			: [text];

	const commit = () => {
		if (settled.current) return;
		settled.current = true;
		if (text === initial) onCancel();
		else onCommit(parsed());
	};
	const cancel = () => {
		if (settled.current) return;
		settled.current = true;
		onCancel();
	};

	return (
		<input
			ref={input}
			className="note-props__value note-props__input"
			aria-label={`Edit ${property.key}`}
			value={text}
			spellCheck={false}
			onChange={(event) => setText(event.target.value)}
			onBlur={commit}
			onKeyDown={(event: KeyboardEvent) => {
				if (event.key === "Enter") {
					event.preventDefault();
					commit();
				} else if (event.key === "Escape") {
					event.preventDefault();
					cancel();
				}
			}}
		/>
	);
}

function AddProperty({
	busy,
	error,
	onAdd,
	onCancel,
}: {
	busy: boolean;
	error?: string;
	onAdd: (key: string, value: string) => void;
	onCancel: () => void;
}) {
	const [key, setKey] = useState("");
	const [value, setValue] = useState("");
	const name = useRef<HTMLInputElement | null>(null);

	useEffect(() => {
		name.current?.focus();
	}, []);

	const submit = () => {
		if (key.trim() === "") {
			onCancel();
			return;
		}
		onAdd(key.trim(), value);
	};

	return (
		<div className="note-props__new">
			<div className="note-props__row">
				<span className="note-props__icon" aria-hidden="true">
					<Plus />
				</span>
				<input
					ref={name}
					className="note-props__key note-props__input"
					aria-label="New property name"
					placeholder="name"
					value={key}
					spellCheck={false}
					onChange={(event) => setKey(event.target.value)}
					onKeyDown={(event) => {
						if (event.key === "Enter") submit();
						if (event.key === "Escape") onCancel();
					}}
				/>
				<input
					className="note-props__value note-props__input"
					aria-label="New property value"
					placeholder="value"
					value={value}
					spellCheck={false}
					onChange={(event) => setValue(event.target.value)}
					onKeyDown={(event) => {
						if (event.key === "Enter") submit();
						if (event.key === "Escape") onCancel();
					}}
				/>
			</div>
			{error && (
				<p className="note-props__error" role="alert">
					{error}
				</p>
			)}
			<div className="note-props__new-actions">
				<button type="button" className="note-props__save" disabled={busy} onClick={submit}>
					Add
				</button>
				<button type="button" className="note-props__cancel" onClick={onCancel}>
					Cancel
				</button>
			</div>
		</div>
	);
}
