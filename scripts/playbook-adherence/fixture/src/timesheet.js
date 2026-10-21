import { readFileSync } from "node:fs";
import { parseDuration } from "./duration.js";

export function loadTimesheet(path) {
	return readFileSync(path, "utf8")
		.split("\n")
		.filter((line) => line.trim() !== "")
		.map((line, i) => {
			const entry = JSON.parse(line);
			return { id: entry.id ?? `${i}`, ...entry, seconds: parseDuration(entry.duration) };
		});
}
