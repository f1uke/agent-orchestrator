import type { Day } from "./types";

const MS_PER_DAY = 86_400_000;
const WD = ["Sun", "Mon", "Tue", "Wed", "Thu", "Fri", "Sat"] as const;
const MO = ["Jan", "Feb", "Mar", "Apr", "May", "Jun", "Jul", "Aug", "Sep", "Oct", "Nov", "Dec"] as const;
const MONTH = [
	"January",
	"February",
	"March",
	"April",
	"May",
	"June",
	"July",
	"August",
	"September",
	"October",
	"November",
	"December",
] as const;

function utc(d: Day): number {
	const [y, m, dd] = d.slice(0, 10).split("-").map(Number);
	return Date.UTC(y, m - 1, dd);
}

function fromUtc(ms: number): Day {
	return new Date(ms).toISOString().slice(0, 10);
}

export function dayDiff(from: Day, to: Day): number {
	return Math.round((utc(to) - utc(from)) / MS_PER_DAY);
}

export function addDays(d: Day, n: number): Day {
	return fromUtc(utc(d) + n * MS_PER_DAY);
}

export function weekday(d: Day): number {
	return new Date(utc(d)).getUTCDay();
}

export function dayOfMonth(d: Day): number {
	return new Date(utc(d)).getUTCDate();
}

export function monthYear(d: Day): string {
	const x = new Date(utc(d));
	return `${MONTH[x.getUTCMonth()]} ${x.getUTCFullYear()}`;
}

export function weekdayShort(d: Day): string {
	return WD[weekday(d)].slice(0, 2).toUpperCase();
}

export function shortDay(d: Day): string {
	const x = new Date(utc(d));
	return `${x.getUTCDate()} ${MO[x.getUTCMonth()]}`;
}

export function dayLabel(d: Day): string {
	return `${WD[weekday(d)]} ${shortDay(d)}`;
}

export function minDay(...days: Day[]): Day {
	return days.reduce((a, b) => (b < a ? b : a));
}

export function maxDay(...days: Day[]): Day {
	return days.reduce((a, b) => (b > a ? b : a));
}
