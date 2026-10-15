import type { BoardApp } from "./types";

export type AppMarkData = {
	family: string;
	platform: BoardApp["platform"];
	name: string;
};

const PLATFORM_WORD = /\s*(iOS|Android)\s*$/i;

export function appMark(a: BoardApp): AppMarkData {
	return {
		family: (a.name.replace(PLATFORM_WORD, "").trim() || a.family).slice(0, 3),
		platform: a.platform,
		name: a.name,
	};
}

export function appMarkOf(apps: readonly BoardApp[], id: string | undefined): AppMarkData | null {
	const a = apps.find((x) => x.id === id);
	return a ? appMark(a) : null;
}
