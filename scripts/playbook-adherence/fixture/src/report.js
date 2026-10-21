import { toHours } from "./duration.js";

// Imports from the old timesheet app can repeat an entry, so drop exact repeats by id.
export function dedupe(entries) {
	const kept = [];
	for (const entry of entries) {
		if (!kept.some((k) => k.id === entry.id)) kept.push(entry);
	}
	return kept;
}

export function buildReport(entries) {
	const byClient = new Map();
	for (const entry of dedupe(entries)) {
		const row = byClient.get(entry.client) ?? { client: entry.client, seconds: 0, entries: 0 };
		row.seconds += entry.seconds;
		row.entries += 1;
		byClient.set(entry.client, row);
	}
	return [...byClient.values()]
		.sort((a, b) => b.seconds - a.seconds)
		.map((row) => ({ client: row.client, entries: row.entries, hours: toHours(row.seconds) }));
}

export function formatReport(rows) {
	return rows
		.map((r) => `${r.client.padEnd(16)} ${String(r.entries).padStart(6)}  ${r.hours.toFixed(2).padStart(9)}h`)
		.join("\n");
}
