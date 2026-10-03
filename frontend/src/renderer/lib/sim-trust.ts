import type { components } from "../../api/schema";
import { apiClient, apiErrorMessage } from "./api-client";

// The root-CA files AO makes an iOS simulator trust when it boots or claims one,
// so HTTPS through a debugging proxy on this Mac (Proxyman, Charles, mitmproxy)
// works inside the app. A simulator does not inherit the Mac's trust store.
export type SimTrustSettings = components["schemas"]["SimTrustSettingsResponse"];

/**
 * The global list, shared by both settings scopes: the Global form edits it and
 * the Project form states it at the per-project override. A Global save writes
 * the daemon's answer straight into this key, so both read the saved list.
 */
export const simTrustSettingsQueryKey = ["settings", "simTrust"] as const;

export async function fetchSimTrustSettings(): Promise<SimTrustSettings> {
	const { data, error } = await apiClient.GET("/api/v1/settings/sim-trust", {});
	if (error) throw new Error(apiErrorMessage(error));
	return normalizeSimTrustSettings(data);
}

// An older daemon (or a stub) may omit any of the three lists; read each as
// empty rather than letting a missing key crash the row.
export function normalizeSimTrustSettings(data: unknown): SimTrustSettings {
	const body = (data ?? {}) as Partial<SimTrustSettings>;
	return {
		caFiles: Array.isArray(body.caFiles) ? body.caFiles : [],
		defaultCaFiles: Array.isArray(body.defaultCaFiles) ? body.defaultCaFiles : [],
		found: Array.isArray(body.found) ? body.found : [],
	};
}

// The textarea edits one path per line. Lines are trimmed and blank ones dropped,
// so a stray space or a trailing newline never reaches the daemon (which refuses
// a path with surrounding whitespace).
export function parseCaFileLines(text: string): string[] {
	return text
		.split("\n")
		.map((line) => line.trim())
		.filter((line) => line !== "");
}

export function formatCaFileLines(files: readonly string[]): string {
	return files.join("\n");
}

export function sameCaFiles(a: readonly string[], b: readonly string[]): boolean {
	return a.length === b.length && a.every((file, i) => file === b[i]);
}

// The at-rest value of a row: a count, since the paths themselves are long
// enough to crowd the row's summary.
export function caFilesCount(files: readonly string[]): string {
	if (files.length === 0) return "None";
	return `${files.length} file${files.length === 1 ? "" : "s"}`;
}

// The short description of a list: its file names, since the folders
// are long and say little. Empty is a real choice - nothing is trusted.
export function caFilesSummary(files: readonly string[]): string {
	if (files.length === 0) return "None";
	return files.map((file) => file.slice(file.lastIndexOf("/") + 1) || file).join(", ");
}
