import type { InfillRequest } from "../../../main/inline-completion/infill";

/**
 * Answers already paid for, keyed by where they were asked.
 *
 * 🗝 The useful hit is not the exact one. A person typing THROUGH a ghost text
 * produces a new request on every keystroke, each with a prompt one character
 * longer - and each of those is already answered by the first response, minus
 * the characters typed since. So a miss is retried with the prompt shortened
 * by 1..N characters, and a cached completion that starts with exactly those
 * characters is returned with them removed (llama.vim's trick). Typing along a
 * suggestion therefore costs no model time at all.
 *
 * The extra-context chunks are left out of the key on purpose: they shift what
 * the model would say only slightly, and a key that included them would miss on
 * every ring update.
 */
export const CACHE_SIZE = 250;
/** How far back a typed-through lookup reaches. */
export const MAX_TYPED_THROUGH = 64;

function keyOf(prefix: string, prompt: string, suffix: string): string {
	// FNV-1a over the three parts. Collisions are harmless (a wrong ghost text the
	// person can ignore) and astronomically rare at 250 entries; holding the raw
	// strings would keep up to 250 copies of 200 lines of buffer alive.
	let h1 = 0x811c9dc5;
	let h2 = 0x01000193;
	const mix = (s: string) => {
		for (let i = 0; i < s.length; i++) {
			const c = s.charCodeAt(i);
			h1 = Math.imul(h1 ^ c, 0x01000193);
			h2 = Math.imul(h2 ^ c, 0x5bd1e995);
		}
		h1 = Math.imul(h1 ^ 0x1f, 0x01000193);
		h2 = Math.imul(h2 ^ 0x1f, 0x5bd1e995);
	};
	mix(prefix);
	mix(prompt);
	mix(suffix);
	return `${(h1 >>> 0).toString(36)}.${(h2 >>> 0).toString(36)}.${prompt.length}`;
}

export class CompletionCache {
	private readonly entries = new Map<string, string>();

	constructor(private readonly capacity = CACHE_SIZE) {}

	get size(): number {
		return this.entries.size;
	}

	set(request: InfillRequest, completion: string): void {
		const key = keyOf(request.inputPrefix, request.prompt, request.inputSuffix);
		this.entries.delete(key);
		this.entries.set(key, completion);
		while (this.entries.size > this.capacity) {
			const oldest = this.entries.keys().next().value;
			if (oldest === undefined) break;
			this.entries.delete(oldest);
		}
	}

	/** An exact answer, or one typed through; null when neither exists. */
	get(request: InfillRequest): string | null {
		const exact = this.touch(keyOf(request.inputPrefix, request.prompt, request.inputSuffix));
		if (exact !== null) return exact;
		const reach = Math.min(MAX_TYPED_THROUGH, request.prompt.length);
		for (let i = 1; i <= reach; i++) {
			const earlier = this.touch(keyOf(request.inputPrefix, request.prompt.slice(0, -i), request.inputSuffix));
			if (earlier === null) continue;
			const typed = request.prompt.slice(-i);
			// Only a suggestion the person is typing ALONG counts; one they typed
			// past or away from is not an answer for here.
			if (earlier.length > i && earlier.startsWith(typed)) return earlier.slice(i);
			return null;
		}
		return null;
	}

	clear(): void {
		this.entries.clear();
	}

	private touch(key: string): string | null {
		const value = this.entries.get(key);
		if (value === undefined) return null;
		this.entries.delete(key);
		this.entries.set(key, value);
		return value;
	}
}
