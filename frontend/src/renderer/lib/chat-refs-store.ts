import type { components } from "../../api/schema";
import { getApiBaseUrl } from "./api-client";

export type ResolvedFile = components["schemas"]["ChatFileMatch"];
export type ChatFileRef = components["schemas"]["ChatFileRef"];
export type ResolveFiles = (refs: string[]) => Promise<ChatFileRef[]>;

// The resolve endpoint refuses a body with more refs than this.
const MAX_BATCH = 64;

const none: readonly ResolvedFile[] = [];

/**
 * What a chat session's files names resolve to, asked once per ref.
 *
 * `ensure` collects the refs of one tick into a single resolve call. An answer, or
 * two failed calls, is kept for good, so a path that names nothing costs no second
 * request and never shows an error. `snapshot` changes identity only when an answer
 * lands, which is what useSyncExternalStore needs.
 */
export class RefStore {
	private known: ReadonlyMap<string, ResolvedFile[]> = new Map();
	private asked = new Set<string>();
	private queue: string[] = [];
	private listeners = new Set<() => void>();

	constructor(private readonly resolve: ResolveFiles) {}

	ensure(refs: readonly string[]): void {
		const fresh = refs.filter((ref) => !this.asked.has(ref));
		if (fresh.length === 0) return;
		for (const ref of fresh) this.asked.add(ref);
		const first = this.queue.length === 0;
		this.queue.push(...fresh);
		if (first) queueMicrotask(() => this.flush());
	}

	matches(ref: string): ResolvedFile[] | undefined {
		return this.known.get(ref);
	}

	subscribe = (listener: () => void): (() => void) => {
		this.listeners.add(listener);
		return () => {
			this.listeners.delete(listener);
		};
	};

	snapshot = (): ReadonlyMap<string, ResolvedFile[]> => this.known;

	private flush(): void {
		const refs = this.queue;
		this.queue = [];
		for (let at = 0; at < refs.length; at += MAX_BATCH) void this.ask(refs.slice(at, at + MAX_BATCH));
	}

	private async ask(refs: string[]): Promise<void> {
		let files: ChatFileRef[] = [];
		try {
			files = await this.resolve(refs).catch(() => this.resolve(refs));
		} catch {
			files = [];
		}
		const next = new Map(this.known);
		for (const ref of refs) next.set(ref, [...(files.find((f) => f.ref === ref)?.matches ?? none)]);
		this.known = next;
		this.listeners.forEach((listener) => listener());
	}
}

// Reads the base URL the way the file bytes do, so both reach the same daemon.
export function resolveOverHttp(sessionId: string): ResolveFiles {
	return async (refs) => {
		const response = await fetch(
			`${getApiBaseUrl()}/api/v1/sessions/${encodeURIComponent(sessionId)}/chat/files/resolve`,
			{
				method: "POST",
				headers: { "Content-Type": "application/json" },
				body: JSON.stringify({ refs }),
			},
		);
		if (!response.ok) throw new Error(`resolve ${response.status}`);
		const body = (await response.json()) as components["schemas"]["ResolveChatFilesResponse"];
		return body.files;
	};
}

export function chatFileUrl(sessionId: string, file: Pick<ResolvedFile, "root" | "path">): string {
	const path = file.path.split("/").map(encodeURIComponent).join("/");
	return `${getApiBaseUrl()}/api/v1/sessions/${encodeURIComponent(sessionId)}/chat/files/${file.root}/${path}`;
}
