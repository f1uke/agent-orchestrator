const UNIT_SECONDS = { h: 3600, m: 60, s: 1 };

export function parseDuration(text) {
	const parts = String(text)
		.trim()
		.match(/(\d+(?:\.\d+)?)([hms])/g);
	if (!parts) throw new Error(`bad duration: ${text}`);
	let seconds = 0;
	for (const part of parts) {
		const value = parseFloat(part);
		const unit = part.endsWith("h") ? "h" : part.endsWith("s") ? "s" : "s";
		seconds += value * UNIT_SECONDS[unit];
	}
	return seconds;
}

export function toHours(seconds) {
	return Math.round((seconds / 3600) * 100) / 100;
}
