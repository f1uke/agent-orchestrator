/**
 * A note's YAML frontmatter, as a list of properties with the bytes each one
 * owns.
 *
 * 🗝 THE RULE IS THE ONE `edit.ts` FOLLOWS, applied to the other write path:
 * the YAML document is never re-serialised. Editing one property replaces the
 * bytes of THAT KEY'S VALUE and nothing else, so key order, comments, quoting
 * style, indentation, blank lines and every other key stay exactly as the
 * author wrote them — and the note's body is never touched by this path at all.
 *
 * It is deliberately not a YAML parse, for the same reason `splitFrontmatter`
 * is not: pulling a YAML parser into untrusted text to read a handful of keys
 * is a much larger surface than the feature needs, and a parser hands back
 * VALUES when what a surgical writer needs is OFFSETS. It is a line scanner,
 * and it is honest about what it does not understand: a shape it cannot rewrite
 * safely (a block scalar, a nested map, an anchor or alias, a flow map) is
 * reported read-only rather than guessed at.
 *
 * The types below are for DISPLAY only — they pick an icon and format a date
 * for reading. Nothing converts or normalises a value's stored form because of
 * one.
 */

/** What a value looks like, for the row's icon and its formatting. */
export type PropertyType = "text" | "date" | "list" | "number" | "checkbox";

/** One key in the frontmatter, and the bytes it owns. */
export type NoteProperty = {
	key: string;
	/** The whole entry: the key's line, plus a block list's item lines. */
	start: number;
	end: number;
	/** The VALUE alone — the only bytes an edit ever replaces. */
	valueStart: number;
	valueEnd: number;
	/** The value exactly as written, between those offsets. */
	raw: string;
	/** What to draw: one entry for a scalar, one per item for a list. */
	values: string[];
	type: PropertyType;
	/** False when this value's YAML shape cannot be rewritten in place. */
	editable: boolean;
	/** Which shape, when it cannot. Shown to the reader. */
	readOnlyReason?: string;
};

export type Frontmatter = {
	/** The whole `---` block including both fences, or null when there is none. */
	block: { start: number; end: number } | null;
	/** Where a new key's line goes: just before the closing fence. */
	insertAt: number;
	properties: NoteProperty[];
};

/** `key:` at the top level of the block, with whatever follows on that line. */
const KEY_LINE = /^([A-Za-z_][\w.@-]*)([ \t]*:)([ \t]*)(.*)$/;
/** An item of a block list, at any indentation. */
const LIST_ITEM = /^([ \t]+)(-[ \t]+)(.*)$/;
/** A line that is only a comment, which the scanner passes over untouched. */
const COMMENT = /^[ \t]*#/;

/**
 * Reads the frontmatter of a note.
 *
 * A note with no frontmatter comes back with `block: null` and no properties,
 * which is what lets the panel offer "Add property" on a note that has never
 * had one.
 */
export function readFrontmatter(content: string): Frontmatter {
	const match = /^---\r?\n([\s\S]*?)\r?\n---(\r?\n|$)/.exec(content);
	if (!match) return { block: null, insertAt: 0, properties: [] };

	const inner = match[1];
	const innerStart = content.indexOf("\n", 0) + 1;
	const properties: NoteProperty[] = [];
	const lines = inner.split("\n");

	// Offset of each inner line within the file.
	const lineStart: number[] = [];
	let at = innerStart;
	for (const line of lines) {
		lineStart.push(at);
		at += line.length + 1;
	}

	for (let i = 0; i < lines.length; i += 1) {
		const line = lines[i];
		if (line.trim() === "" || COMMENT.test(line)) continue;
		const key = KEY_LINE.exec(line);
		if (!key) continue;

		const [, name, colon, gap, rest] = key;
		const valueStart = lineStart[i] + name.length + colon.length + gap.length;
		const inlineValue = trimCR(rest);
		const lineEnd = valueStart + inlineValue.length;

		if (inlineValue !== "") {
			properties.push(scalarOrFlow(name, lineStart[i], lineEnd, valueStart, lineEnd, inlineValue));
			continue;
		}

		// `key:` with its value on the lines beneath. Which shape those lines are
		// decides whether this key can be written at all.
		const items: string[] = [];
		let j = i + 1;
		let indented = false;
		while (j < lines.length && (lines[j].trim() === "" || /^[ \t]/.test(lines[j]))) {
			if (lines[j].trim() !== "") {
				indented = true;
				const item = LIST_ITEM.exec(trimCR(lines[j]));
				if (!item) {
					// An indented line that is not a list item is a nested map (or a
					// continuation of one), and rewriting a nested structure in place is
					// not a thing this scanner can promise.
					break;
				}
				items.push(unquote(item[3].trim()));
			}
			j += 1;
		}
		const blockEnd = j < lines.length ? lineStart[j] - 1 : lineStart[lines.length - 1] + lines[lines.length - 1].length;
		const nested = indented && items.length === 0;

		if (nested) {
			properties.push({
				key: name,
				start: lineStart[i],
				end: blockEnd,
				valueStart: lineEnd,
				valueEnd: blockEnd,
				raw: content.slice(lineEnd, blockEnd),
				values: [],
				type: "text",
				editable: false,
				readOnlyReason: "a nested map",
			});
			i = j - 1;
			continue;
		}
		if (items.length > 0) {
			properties.push({
				key: name,
				start: lineStart[i],
				end: blockEnd,
				valueStart: lineEnd,
				valueEnd: blockEnd,
				raw: content.slice(lineEnd, blockEnd),
				values: items,
				type: "list",
				editable: true,
			});
			i = j - 1;
			continue;
		}
		// `key:` with nothing under it: an empty scalar, which is editable.
		properties.push({
			key: name,
			start: lineStart[i],
			end: lineEnd,
			valueStart: lineEnd,
			valueEnd: lineEnd,
			raw: "",
			values: [""],
			type: "text",
			editable: true,
		});
	}

	return {
		block: { start: 0, end: match[0].length },
		// Just before the closing fence, so a new key joins the block's end
		// without moving anything that is already in it.
		insertAt: match[0].length - "---".length - match[2].length,
		properties,
	};
}

/** A one-line value: a flow list, a shape we refuse, or a plain scalar. */
function scalarOrFlow(
	key: string,
	start: number,
	end: number,
	valueStart: number,
	valueEnd: number,
	raw: string,
): NoteProperty {
	const base = { key, start, end, valueStart, valueEnd, raw };
	const refuse = (reason: string): NoteProperty => ({
		...base,
		values: [raw],
		type: "text",
		editable: false,
		readOnlyReason: reason,
	});

	if (/^[|>]/.test(raw)) return refuse("a multi-line block scalar");
	if (/^&/.test(raw)) return refuse("a YAML anchor");
	if (/^\*/.test(raw)) return refuse("a YAML alias");
	if (/^!/.test(raw)) return refuse("an explicitly tagged value");
	if (/^\{/.test(raw)) return refuse("an inline map");

	if (/^\[.*\]$/.test(raw)) {
		const body = raw.slice(1, -1).trim();
		const values = body === "" ? [] : body.split(",").map((part) => unquote(part.trim()));
		return { ...base, values, type: "list", editable: true };
	}

	const value = unquote(raw);
	return { ...base, values: [value], type: scalarType(value), editable: true };
}

/**
 * The type a value READS as. Display only: nothing below is used to convert or
 * re-store a value, only to pick its icon and to format a date for a human.
 */
function scalarType(value: string): PropertyType {
	if (/^(true|false|yes|no)$/i.test(value)) return "checkbox";
	if (/^-?\d+(\.\d+)?$/.test(value)) return "number";
	if (/^\d{4}-\d{2}-\d{2}([T ]|$)/.test(value)) return "date";
	return "text";
}

/** A date written the way a reader reads one. Falls back to what was stored. */
export function formatDate(value: string): string {
	const match = /^(\d{4})-(\d{2})-(\d{2})/.exec(value);
	return match ? `${match[3]}/${match[2]}/${match[1]}` : value;
}

function trimCR(line: string): string {
	return line.endsWith("\r") ? line.slice(0, -1) : line;
}

function unquote(value: string): string {
	const match = /^(["'])([\s\S]*)\1$/.exec(value);
	return match ? match[2] : value;
}

/**
 * A value written back the way it was stored.
 *
 * A value that arrived quoted stays quoted in the same style; one that arrived
 * bare stays bare unless it now holds something YAML would misread, in which
 * case it is quoted rather than silently corrupting the block.
 */
function writeScalar(previousRaw: string, value: string): string {
	const quote = /^(["'])[\s\S]*\1$/.exec(previousRaw)?.[1];
	if (quote) return `${quote}${value.split(quote).join(`\\${quote}`)}${quote}`;
	return needsQuoting(value) ? `"${value.split('"').join('\\"')}"` : value;
}

function needsQuoting(value: string): boolean {
	if (value === "") return false;
	if (value !== value.trim()) return true;
	if (/^[-?:,[\]{}#&*!|>'"%@`]/.test(value)) return true;
	return value.includes(": ") || value.includes(" #") || value.endsWith(":");
}

/**
 * The note with ONE property's value replaced, and every other byte identical.
 *
 * Throws rather than writing if the property's bytes are not the ones that were
 * read — the note moved, and a save is not the place to discover that by
 * overwriting something else.
 */
export function writeProperty(content: string, property: NoteProperty, values: string[]): string {
	if (!property.editable) throw new Error(`Refusing to write: ${property.key} is ${property.readOnlyReason}.`);
	if (content.slice(property.valueStart, property.valueEnd) !== property.raw) {
		throw new Error("Refusing to write: this property is no longer where it was read.");
	}
	return content.slice(0, property.valueStart) + nextValue(property, values) + content.slice(property.valueEnd);
}

/** The replacement bytes for a property's value, in the shape it was stored. */
function nextValue(property: NoteProperty, values: string[]): string {
	if (property.type !== "list") return writeScalar(property.raw, values[0] ?? "");

	// A flow list stays a flow list; a block list keeps its own indentation and
	// dash, read back out of the bytes it already occupies.
	if (/^[ \t]*\[/.test(property.raw)) {
		return `[${values.map((value) => writeScalar("", value)).join(", ")}]`;
	}
	const item = /(\r?\n)([ \t]+)(-[ \t]+)/.exec(property.raw);
	const newline = item?.[1] ?? "\n";
	const indent = item?.[2] ?? "  ";
	const dash = item?.[3] ?? "- ";
	if (values.length === 0) return "";
	return values.map((value) => `${newline}${indent}${dash}${writeScalar("", value)}`).join("");
}

/**
 * The note with one NEW property, and the body byte-for-byte untouched.
 *
 * A note that has no frontmatter gets a well-formed block put in front of it —
 * nothing of what was already written moves or changes.
 */
export function addProperty(content: string, frontmatter: Frontmatter, key: string, value: string): string {
	const name = key.trim();
	if (!/^[A-Za-z_][\w.@-]*$/.test(name)) {
		throw new Error("A property name can hold letters, digits, and - _ . @ — and must start with a letter.");
	}
	if (frontmatter.properties.some((property) => property.key === name)) {
		throw new Error(`This note already has a “${name}” property.`);
	}
	const line = `${name}: ${writeScalar("", value)}`;
	if (!frontmatter.block) return `---\n${line}\n---\n${content}`;
	return `${content.slice(0, frontmatter.insertAt)}${line}\n${content.slice(frontmatter.insertAt)}`;
}
