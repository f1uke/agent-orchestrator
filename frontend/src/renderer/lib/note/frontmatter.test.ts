import { describe, expect, it } from "vitest";
import { addProperty, formatDate, readFrontmatter, writeProperty } from "./frontmatter";

const NOTE = [
	"---",
	"title: MOBILITY-4713-Webview-Zoom - Tasks",
	"type: tasks",
	"updated: 2026-09-02",
	"tags:",
	"  - work",
	"  - mobile",
	"aliases: [zoom, webview]",
	"done: false",
	"count: 12",
	"---",
	"",
	"# MOBILITY-4713-Webview-Zoom - Tasks",
	"",
	"Body prose.",
	"",
].join("\n");

function property(content: string, key: string) {
	const found = readFrontmatter(content).properties.find((p) => p.key === key);
	if (!found) throw new Error(`no property ${key}`);
	return found;
}

describe("reading properties", () => {
	it("finds every key in order, with its inferred display type", () => {
		const { properties } = readFrontmatter(NOTE);
		expect(properties.map((p) => [p.key, p.type])).toEqual([
			["title", "text"],
			["type", "text"],
			["updated", "date"],
			["tags", "list"],
			["aliases", "list"],
			["done", "checkbox"],
			["count", "number"],
		]);
	});

	it("owns exactly the bytes it names", () => {
		for (const p of readFrontmatter(NOTE).properties) {
			expect(NOTE.slice(p.valueStart, p.valueEnd)).toBe(p.raw);
		}
	});

	it("reads a block list's items and a flow list's items alike", () => {
		expect(property(NOTE, "tags").values).toEqual(["work", "mobile"]);
		expect(property(NOTE, "aliases").values).toEqual(["zoom", "webview"]);
	});

	it("reports no frontmatter as no frontmatter, not as an empty one", () => {
		const bare = readFrontmatter("# Title\n\nbody\n");
		expect(bare.block).toBeNull();
		expect(bare.properties).toEqual([]);
	});

	it("does not mistake a horizontal rule for frontmatter", () => {
		expect(readFrontmatter("body\n\n---\n\nmore\n").block).toBeNull();
	});
});

/**
 * The write path's whole promise: one key's value changes and the rest of the
 * file — every other key, the comments, the body — is identical byte for byte.
 */
describe("writing one property", () => {
	function assertOnlyValueChanged(before: string, after: string, key: string) {
		const p = property(before, key);
		expect(after.slice(0, p.valueStart)).toBe(before.slice(0, p.valueStart));
		expect(after.slice(after.length - (before.length - p.valueEnd))).toBe(before.slice(p.valueEnd));
	}

	it("rewrites a scalar and nothing else", () => {
		const after = writeProperty(NOTE, property(NOTE, "type"), ["notes"]);
		expect(after).toContain("type: notes\n");
		expect(after).toContain("title: MOBILITY-4713-Webview-Zoom - Tasks\n");
		expect(after).toContain("# MOBILITY-4713-Webview-Zoom - Tasks\n");
		assertOnlyValueChanged(NOTE, after, "type");
	});

	it("rewrites a block list keeping its indentation and its dash", () => {
		const after = writeProperty(NOTE, property(NOTE, "tags"), ["work", "mobile", "zoom"]);
		expect(after).toContain("tags:\n  - work\n  - mobile\n  - zoom\naliases:");
		assertOnlyValueChanged(NOTE, after, "tags");
	});

	it("keeps a flow list a flow list", () => {
		const after = writeProperty(NOTE, property(NOTE, "aliases"), ["zoom"]);
		expect(after).toContain("aliases: [zoom]\n");
		expect(after).not.toContain("aliases:\n  -");
	});

	it("writing a value back unchanged is a no-op, for every key", () => {
		for (const p of readFrontmatter(NOTE).properties) {
			expect(writeProperty(NOTE, p, p.values)).toBe(NOTE);
		}
	});

	it("keeps the quoting style the author used", () => {
		const quoted = "---\ntitle: \"Quoted: with a colon\"\nother: 'single'\n---\n\nbody\n";
		expect(writeProperty(quoted, property(quoted, "title"), ["Still: quoted"])).toContain('title: "Still: quoted"\n');
		expect(writeProperty(quoted, property(quoted, "other"), ["plain"])).toContain("other: 'plain'\n");
	});

	it("quotes a bare value that would otherwise break the block", () => {
		const after = writeProperty(NOTE, property(NOTE, "type"), ["urgent: really"]);
		expect(after).toContain('type: "urgent: really"\n');
		// And reading it back gives what was typed, not the quotes.
		expect(property(after, "type").values).toEqual(["urgent: really"]);
	});

	it("leaves a bare value bare when it does not need quoting", () => {
		expect(writeProperty(NOTE, property(NOTE, "type"), ["a plain phrase"])).toContain("type: a plain phrase\n");
	});

	it("refuses to write a property whose bytes have moved", () => {
		const p = property(NOTE, "type");
		// The agent added a key above this one, so every offset below it shifted.
		const drifted = NOTE.replace("title:", "added: by the agent\ntitle:");
		expect(() => writeProperty(drifted, p, ["x"])).toThrow(/no longer/);
	});

	it("preserves comments and blank lines inside the block", () => {
		const commented = "---\n# what this note is\ntype: tasks\n\ntitle: T\n---\n\nbody\n";
		const after = writeProperty(commented, property(commented, "type"), ["notes"]);
		expect(after).toBe("---\n# what this note is\ntype: notes\n\ntitle: T\n---\n\nbody\n");
	});
});

/**
 * A shape this scanner cannot rewrite with certainty is offered read-only. A
 * property you cannot rewrite safely is one you do not let anyone edit.
 */
describe("what is deliberately read-only", () => {
	const shapes: [string, string, string][] = [
		["a block scalar", "---\nnote: |\n  line one\n  line two\n---\n\nbody\n", "note"],
		["a folded scalar", "---\nnote: >\n  folded text\n---\n\nbody\n", "note"],
		["an anchor", "---\nbase: &anchor value\n---\n\nbody\n", "base"],
		["an alias", "---\nother: *anchor\n---\n\nbody\n", "other"],
		["a tagged value", "---\nwhen: !!timestamp 2026-09-02\n---\n\nbody\n", "when"],
		["an inline map", "---\nmeta: {a: 1}\n---\n\nbody\n", "meta"],
		["a nested map", "---\nmeta:\n  a: 1\n  b: 2\n---\n\nbody\n", "meta"],
	];

	for (const [name, note, key] of shapes) {
		it(`refuses ${name}`, () => {
			const p = property(note, key);
			expect(p.editable).toBe(false);
			expect(p.readOnlyReason).toBeTruthy();
			expect(() => writeProperty(note, p, ["anything"])).toThrow(/Refusing to write/);
		});
	}

	it("still offers the editable keys beside a shape it refuses", () => {
		const mixed = "---\nnote: |\n  block\ntitle: T\n---\n\nbody\n";
		expect(readFrontmatter(mixed).properties.map((p) => [p.key, p.editable])).toEqual([
			["note", false],
			["title", true],
		]);
	});
});

describe("adding a property", () => {
	it("joins the end of an existing block and moves nothing else", () => {
		const after = addProperty(NOTE, readFrontmatter(NOTE), "status", "in progress");
		expect(after).toContain("count: 12\nstatus: in progress\n---\n");
		expect(after.slice(after.indexOf("\n---\n") + 5)).toBe(NOTE.slice(NOTE.indexOf("\n---\n") + 5));
	});

	it("creates a block on a note that has none, leaving the body untouched", () => {
		const bare = "# Title\n\nbody with **markup**\n";
		const after = addProperty(bare, readFrontmatter(bare), "type", "notes");
		expect(after).toBe("---\ntype: notes\n---\n# Title\n\nbody with **markup**\n");
		expect(after.endsWith(bare)).toBe(true);
	});

	it("refuses a duplicate key rather than writing a second one", () => {
		expect(() => addProperty(NOTE, readFrontmatter(NOTE), "title", "x")).toThrow(/already has/);
	});

	it("refuses a name YAML could not hold", () => {
		for (const bad of ["", "has space", "2leading", "a:b"]) {
			expect(() => addProperty(NOTE, readFrontmatter(NOTE), bad, "x")).toThrow(/property name/);
		}
	});

	it("reads back exactly what was added", () => {
		const after = addProperty(NOTE, readFrontmatter(NOTE), "status", "in progress");
		expect(property(after, "status").values).toEqual(["in progress"]);
	});
});

describe("formatDate", () => {
	it("writes an ISO date the way a reader reads one", () => {
		expect(formatDate("2026-09-02")).toBe("02/09/2026");
		expect(formatDate("2026-09-02T10:00:00Z")).toBe("02/09/2026");
	});

	it("leaves anything else exactly as stored", () => {
		expect(formatDate("sometime in Q3")).toBe("sometime in Q3");
	});
});
