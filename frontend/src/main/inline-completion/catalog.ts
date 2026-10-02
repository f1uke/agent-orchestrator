/**
 * What the inline-completion runtime is made of, pinned.
 *
 * 🗝 Everything here is fetched on first enable rather than bundled with the app.
 * The feature is opt-in and its first enable already needs a 1.6-8 GB model, so
 * the 12 MB runtime rides the same verified, cancellable download at no cost to
 * the person - while an install that never turns it on carries none of it, and
 * none of llama.cpp's dylibs enter the app's own codesign/notarize chain.
 *
 * Every artifact is pinned by an immutable address AND a sha256, so what lands
 * on disk is exactly what was measured, and a bump is a reviewed code change.
 */

export type DownloadArtifact = {
	/** Shown in the progress line: "Downloading <label>…". */
	label: string;
	url: string;
	sizeBytes: number;
	sha256: string;
};

export type RuntimeArtifact = DownloadArtifact & {
	/** llama.cpp build tag; also the directory the tarball unpacks to. */
	tag: string;
};

/**
 * llama.cpp's own release build. darwin-arm64 only, because that is the
 * platform it was measured on; anything else reports itself unavailable rather
 * than pretending.
 *
 * The binary is ad-hoc linker-signed and loads its dylibs from `@rpath`
 * (= its own directory), so it runs from wherever it is unpacked. Files written
 * by this process carry no `com.apple.quarantine`, so Gatekeeper never sees it.
 */
const RUNTIMES: Partial<Record<string, RuntimeArtifact>> = {
	"darwin-arm64": {
		tag: "b11327",
		label: "llama.cpp b11327",
		url: "https://github.com/ggml-org/llama.cpp/releases/download/b11327/llama-b11327-bin-macos-arm64.tar.gz",
		sizeBytes: 11_829_470,
		sha256: "4f0c209f176536d720c5ceb9e475efba583de5e783fc0b28389e5dc554ddc8e7",
	},
};

export function runtimeFor(platform: NodeJS.Platform, arch: string): RuntimeArtifact | null {
	return RUNTIMES[`${platform}-${arch}`] ?? null;
}

export type ModelId = "qwen2.5-coder-1.5b" | "qwen2.5-coder-3b" | "qwen2.5-coder-7b" | "sweep-next-edit-1.5b";

/**
 * What a model is asked, which decides everything downstream of the download:
 *
 * - `fim`: fill-in-the-middle over llama-server's `/infill` - text AT the
 *   cursor, shown as ghost text.
 * - `next-edit`: rewrite the 21 lines around the cursor given the recent edits,
 *   over a raw `/completion` prompt - an edit ANYWHERE in that window, shown at
 *   its own location (see next-edit.ts).
 */
export type ModelKind = "fim" | "next-edit";

export type ModelSpec = DownloadArtifact & {
	id: ModelId;
	kind: ModelKind;
	/**
	 * Whether it ALSO answers fill-in-the-middle over `/infill` well enough to
	 * give ghost text at the cursor. True of every FIM model by definition; a
	 * next-edit model has it only when measured (see the Sweep entry).
	 */
	infill: boolean;
	/** The file name on disk under `llm/models/`. */
	fileName: string;
	/** One honest line for the picker: what this size buys and costs. */
	blurb: string;
};

/**
 * Qwen2.5-Coder BASE models (fill-in-the-middle needs the base, not Instruct),
 * Q8_0, from ggml-org - the same files llama-server's own
 * `--fim-qwen-*-default` presets download. Addressed by the repo COMMIT, not
 * `main`, so a re-upload upstream can never change what AO fetches.
 *
 * The blurbs are the measured trade-off on an M5 Pro (see the PR). 1.5B is the
 * default because it is the one that stays well under half a second to a first
 * prediction while the machine is busy, at a quarter of 7B's memory, and the
 * quality gap to the larger ones (62% / 66% / 69% of lines exactly right on
 * 120 held-out positions) is within the noise of that sample.
 */
export const MODELS: readonly ModelSpec[] = [
	{
		id: "qwen2.5-coder-1.5b",
		kind: "fim",
		infill: true,
		label: "Qwen2.5-Coder 1.5B",
		fileName: "qwen2.5-coder-1.5b-q8_0.gguf",
		url: "https://huggingface.co/ggml-org/Qwen2.5-Coder-1.5B-Q8_0-GGUF/resolve/8be1b8a895a84beea772817caaa71eba6b6e0d07/qwen2.5-coder-1.5b-q8_0.gguf",
		sizeBytes: 1_646_573_056,
		sha256: "29871c94d15727a6e243f79a37113d4ae625a6215b5e800bf41a23af2da32832",
		blurb: "Fastest, and the default: a prediction about 0.3 s after you start typing. About 2 GB of memory while on.",
	},
	{
		id: "qwen2.5-coder-3b",
		kind: "fim",
		infill: true,
		label: "Qwen2.5-Coder 3B",
		fileName: "qwen2.5-coder-3b-q8_0.gguf",
		url: "https://huggingface.co/ggml-org/Qwen2.5-Coder-3B-Q8_0-GGUF/resolve/9c1de162ae417c9c3aacde97c729c4128de047d8/qwen2.5-coder-3b-q8_0.gguf",
		sizeBytes: 3_285_476_160,
		sha256: "a522a906e299ed34db738b9626b2cd0da9e446c14674468a22fc2eae3dbd344d",
		blurb: "Slightly better guesses, about twice as slow to the first one. About 3.7 GB of memory while on.",
	},
	{
		id: "qwen2.5-coder-7b",
		kind: "fim",
		infill: true,
		label: "Qwen2.5-Coder 7B",
		fileName: "qwen2.5-coder-7b-q8_0.gguf",
		url: "https://huggingface.co/ggml-org/Qwen2.5-Coder-7B-Q8_0-GGUF/resolve/bca77e0a8c88fc224882bcc404170c3ef17efacc/qwen2.5-coder-7b-q8_0.gguf",
		sizeBytes: 8_098_525_600,
		sha256: "0ef48dc94a3c551a6736ac2601de38413dc9aa9318534b16e8baee08290a4aaf",
		blurb:
			"Best guesses, about three times as slow. About 8.7 GB of memory while on - heavy next to agents and language servers.",
	},
	/**
	 * sweep-next-edit 1.5B (Qwen2.5-Coder 1.5B fine-tuned for next-edit by Sweep,
	 * Apache-2.0). Sweep's own GGUF - the only one it publishes - addressed by
	 * commit like the rest. Not a FIM model: it is asked through `/completion`
	 * with its own prompt format (next-edit.ts), and the server runs it with
	 * n-gram speculative decoding and a slot per kind of prompt (service.ts
	 * `NEXT_EDIT_ARGS`).
	 *
	 * It ALSO kept its base model's fill-in-the-middle: over `/infill`, on the
	 * same 120 held-out lines, 53% exactly right - the same as Qwen2.5-Coder 1.5B
	 * (53%) - where its own rewrite managed 34%. So it gives ghost text at the
	 * cursor through the FIM path, and next edits where there is nothing to add.
	 */
	{
		id: "sweep-next-edit-1.5b",
		kind: "next-edit",
		infill: true,
		label: "Sweep Next-Edit 1.5B",
		fileName: "sweep-next-edit-1.5b-q8_0-v2.gguf",
		url: "https://huggingface.co/sweepai/sweep-next-edit-1.5B/resolve/409016591c6c1a94f545f22328a85a3516118f34/sweep-next-edit-1.5b.q8_0.v2.gguf",
		sizeBytes: 1_537_269_856,
		sha256: "1321ea5e5d7529e60f9770c6a0b3a965f89542d16cf4ae51bab267f6a88150da",
		blurb:
			"Completes the line like Qwen 1.5B, and when there is nothing to add it predicts your next edit: carries a change you just made to the lines around it, and Tab jumps there. About 0.4 s a suggestion; about 2.4 GB of memory while on.",
	},
];

export const DEFAULT_MODEL_ID: ModelId = "qwen2.5-coder-1.5b";

export function modelById(id: string): ModelSpec | null {
	return MODELS.find((m) => m.id === id) ?? null;
}

export function isModelId(value: unknown): value is ModelId {
	return typeof value === "string" && MODELS.some((m) => m.id === value);
}
