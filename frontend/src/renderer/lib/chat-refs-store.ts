import type { components } from "../../api/schema";

export type ResolvedFile = components["schemas"]["ChatFileMatch"];
export type ChatFileRef = components["schemas"]["ChatFileRef"];
export type ResolveFiles = (refs: string[]) => Promise<ChatFileRef[]>;

const empty: ReadonlyMap<string, ResolvedFile[]> = new Map();

export class RefStore {
	constructor(_resolve: ResolveFiles) {}

	ensure(_refs: readonly string[]): void {}

	matches(_ref: string): ResolvedFile[] | undefined {
		return undefined;
	}

	subscribe =
		(_listener: () => void): (() => void) =>
		() =>
			undefined;

	snapshot = (): ReadonlyMap<string, ResolvedFile[]> => empty;
}

export function resolveOverHttp(_sessionId: string): ResolveFiles {
	return async () => [];
}

export function chatFileUrl(_sessionId: string, _file: Pick<ResolvedFile, "root" | "path">): string {
	return "";
}
