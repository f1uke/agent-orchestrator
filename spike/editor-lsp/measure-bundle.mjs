// THROWAWAY SPIKE. Reports what a build costs on disk and, separately, what a
// cold page load actually pulls (eager chunks only) — the two numbers that
// matter for a desktop app the human keeps open all day.
import { readdirSync, statSync, readFileSync } from "node:fs";
import { gzipSync } from "node:zlib";
import path from "node:path";

const dir = process.argv[2] ?? "dist";
let total = 0,
	totalGz = 0;
const rows = [];
const walk = (d) => {
	for (const e of readdirSync(d, { withFileTypes: true })) {
		const p = path.join(d, e.name);
		if (e.isDirectory()) {
			walk(p);
			continue;
		}
		const b = readFileSync(p);
		const gz = gzipSync(b).length;
		total += b.length;
		totalGz += gz;
		rows.push({ file: path.relative(dir, p), bytes: b.length, gz });
	}
};
walk(dir);
// eager = anything the entry html or entry chunk references without dynamic import
const html = rows.find((r) => r.file.endsWith(".html"));
const htmlText = html ? readFileSync(path.join(dir, html.file), "utf8") : "";
const entry = [...htmlText.matchAll(/src="\/?([^"]+\.js)"/g)].map((m) => m[1].replace(/^assets\//, "assets/"));
rows.sort((a, b) => b.bytes - a.bytes);
console.log(`# ${dir}`);
console.log(`files: ${rows.length}  total: ${(total / 1e6).toFixed(2)} MB  gzip: ${(totalGz / 1e6).toFixed(2)} MB`);
console.log(`entry scripts in index.html: ${entry.join(", ") || "(none)"}`);
console.log("top 12 by raw size:");
for (const r of rows.slice(0, 12))
	console.log(
		`  ${(r.bytes / 1e3).toFixed(0).padStart(7)} kB  gz ${(r.gz / 1e3).toFixed(0).padStart(6)} kB  ${r.file}`,
	);
