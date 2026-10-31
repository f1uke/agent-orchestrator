import { describe, expect, it } from "vitest";
import { IMAGE_EXT, imageRefs } from "./chat-refs";

describe("imageRefs", () => {
	it("takes an inline code span that ends in an image extension, as a path, a glob, ~/ or an absolute path", () => {
		const md =
			"See `shots/a.png`, `shots/clash-*.png`, `~/.ao/knowledge/p/plans/x.jpeg` and `/work/project/docs/y.webp`.";
		expect(imageRefs(md)).toEqual([
			"shots/a.png",
			"shots/clash-*.png",
			"~/.ao/knowledge/p/plans/x.jpeg",
			"/work/project/docs/y.webp",
		]);
	});

	it("takes a bare file name in a code span, and every extension in IMAGE_EXT in any case", () => {
		const md = IMAGE_EXT.map((ext) => `\`pic.${ext}\``).join(" ") + " `UP.PNG`";
		expect(imageRefs(md)).toEqual([...IMAGE_EXT.map((ext) => `pic.${ext}`), "UP.PNG"]);
	});

	it("takes the image words of a fenced block, one line at a time, and skips lines and words that are not images", () => {
		const md = ["Files:", "```sh", "ls shots", "cp shots/one.png out/", "  shots/two-*.gif  ", "notes.md", "```"].join(
			"\n",
		);
		expect(imageRefs(md)).toEqual(["shots/one.png", "shots/two-*.gif"]);
		expect(imageRefs("```\ncp a.png b/c.jpg\n```")).toEqual(["a.png", "b/c.jpg"]);
		expect(imageRefs("```\nshots/open.png")).toEqual(["shots/open.png"]);
		expect(imageRefs("```\nshots/in.png\n```\nshots/out.png is prose")).toEqual(["shots/in.png", "shots/out.png"]);
	});

	it("takes the target of a markdown image and of a link to an image, not a link to a page", () => {
		const md = '![before](shots/before.png) and [after](shots/after.jpg "title") and [docs](docs/readme.md)';
		expect(imageRefs(md)).toEqual(["shots/before.png", "shots/after.jpg"]);
	});

	it("takes a bare path in prose when it has a slash or starts with ~/, and drops sentence punctuation", () => {
		const md = "Saved to shots/a.png, then (~/b.png) and finally **docs/c.svg**. A lone d.png has no slash.";
		expect(imageRefs(md)).toEqual(["shots/a.png", "~/b.png", "docs/c.svg"]);
	});

	it("ignores URLs, data: URIs, protocol-relative links, non-image extensions and words without a slash", () => {
		const md = [
			"https://example.test/a.png http://example.test/b.png data:image/png;base64,AAAA //cdn.test/c.png",
			"`https://example.test/d.png` `data:image/png;base64,BBBB`",
			"![x](https://example.test/e.png) [y](data:image/png;base64,CCCC)",
			"`shots/notes.md` `shots/` `.png` `png` plans/readme.txt the word image.png",
		].join("\n");
		expect(imageRefs(md)).toEqual([]);
	});

	it("keeps the order of appearance across code, links and prose, and lists each ref once", () => {
		const md = "`z/1.png` then ![a](z/2.png) then z/3.png then `z/1.png` and z/2.png again";
		expect(imageRefs(md)).toEqual(["z/1.png", "z/2.png", "z/3.png"]);
	});

	it("joins a bare name or glob to the directory a code span named before it, and keeps the bare form too", () => {
		expect(imageRefs("in `plans/shots/` the file `clash-*.png`")).toEqual(["clash-*.png", "plans/shots/clash-*.png"]);
	});

	it("reads a worker report that names a directory, then globs without an extension", () => {
		const report =
			"ภาพอยู่ใน `plans/feature-chat-trajectory--screens-pr1e/`:\n\n" +
			"- `clash-one-group-turn-tall-*` คือ turn กลุ่มเดียว\n" +
			"- `clash-one-group-turn-open-*` และ `clash-one-group-turn-group-open-*` คือ turn หลายกลุ่ม";
		expect(imageRefs(report)).toEqual([
			"plans/feature-chat-trajectory--screens-pr1e/clash-one-group-turn-tall-*",
			"plans/feature-chat-trajectory--screens-pr1e/clash-one-group-turn-open-*",
			"plans/feature-chat-trajectory--screens-pr1e/clash-one-group-turn-group-open-*",
		]);
	});

	it("takes a glob without an extension only under a directory span, never alone", () => {
		expect(imageRefs("run `make-*` then `build-*`")).toEqual([]);
	});

	it("keeps a path the human pasted in a question card", () => {
		const q =
			"ภาพเทียบอยู่ใน ~/.ao/knowledge/agent-orchestrator/plans/feature-chat-trajectory--screens-pr1f/compare-466-inline-tasks-1920x1080.png ครับ";
		expect(imageRefs(q)).toEqual([
			"~/.ao/knowledge/agent-orchestrator/plans/feature-chat-trajectory--screens-pr1f/compare-466-inline-tasks-1920x1080.png",
		]);
	});

	it("drops a candidate over 1024 bytes and keeps at most 64", () => {
		const long = `${"a".repeat(1021)}.png`;
		expect(imageRefs(`\`${long}\` \`ok.png\``)).toEqual(["ok.png"]);
		const many = Array.from({ length: 80 }, (_, i) => `\`s/${i}.png\``).join(" ");
		const refs = imageRefs(many);
		expect(refs).toHaveLength(64);
		expect(refs[0]).toBe("s/0.png");
		expect(refs[63]).toBe("s/63.png");
	});

	it("finds nothing in text without refs", () => {
		expect(imageRefs("")).toEqual([]);
		expect(imageRefs("plain words only")).toEqual([]);
	});
});
