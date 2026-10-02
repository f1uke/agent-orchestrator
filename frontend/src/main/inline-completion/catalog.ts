/**
 * What the inline-completion runtime is made of, pinned.
 *
 * 🗝 Everything here is fetched on first enable rather than bundled with the app.
 * The feature is opt-in and its first enable already needs a 1.5 GB model, so
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

export type ModelId = "sweep-next-edit-1.5b";

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
	/** One honest line about what it does and costs, shown beside the switch. */
	blurb: string;
};

/**
 * sweep-next-edit 1.5B: Qwen2.5-Coder 1.5B fine-tuned for next-edit by Sweep,
 * Apache-2.0. Sweep's own GGUF - the only one it publishes - addressed by the
 * repo COMMIT, not `main`, so a re-upload upstream can never change what AO
 * fetches. Asked two ways, by one server (service.ts `NEXT_EDIT_ARGS`):
 *
 * - its own next-edit prompt over `/completion` (next-edit.ts): the change the
 *   person is likely to make next, anywhere in the 21 lines around the cursor;
 * - fill-in-the-middle over `/infill`, which it kept from its base model: on
 *   120 held-out lines of 2026 code, 53% exactly right - the same as the
 *   Qwen2.5-Coder 1.5B AO used to ship (53%), where its own rewrite managed 34%.
 *
 * 🗝 It is the ONLY model. It does everything the Qwen FIM models did, as well
 * as the 1.5B and at its speed, plus next edits; 7B's edge over 1.5B was within
 * the noise of the sample at 8.7 GB, and 3B is under Qwen's non-commercial
 * research licence. A setting that still names a Qwen model reads as this one
 * (settings.ts).
 */
export const MODELS: readonly ModelSpec[] = [
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
			"Sweep Next-Edit 1.5B: finishes the line you are typing, and when there is nothing to add there, suggests the change you are likely to make next. About 0.4 s a suggestion; about 2.4 GB of memory while on.",
	},
];

export const DEFAULT_MODEL_ID: ModelId = "sweep-next-edit-1.5b";

export function modelById(id: string): ModelSpec | null {
	return MODELS.find((m) => m.id === id) ?? null;
}

export function isModelId(value: unknown): value is ModelId {
	return typeof value === "string" && MODELS.some((m) => m.id === value);
}
