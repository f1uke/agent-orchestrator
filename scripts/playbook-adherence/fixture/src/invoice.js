import { toHours } from "./duration.js";

export function buildInvoice(entries, client, rate) {
	const lines = entries
		.filter((e) => e.client === client)
		.map((e) => ({ date: e.date, project: e.project, note: e.note, hours: toHours(e.seconds) }));
	const hours = lines.reduce((sum, l) => sum + l.hours, 0);
	return { client, rate, lines, hours, total: Math.round(hours * rate * 100) / 100 };
}

export function formatInvoice(invoice) {
	const rows = invoice.lines.map(
		(l) => `${l.date}  ${l.project.padEnd(14)} ${l.hours.toFixed(2).padStart(6)}h  ${l.note}`,
	);
	return [
		`Invoice: ${invoice.client}`,
		...rows,
		`Total: ${invoice.hours.toFixed(2)}h x ${invoice.rate} = ${invoice.total.toFixed(2)}`,
	].join("\n");
}
