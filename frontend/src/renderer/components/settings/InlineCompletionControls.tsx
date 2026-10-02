import { useState } from "react";
import { Loader2 } from "lucide-react";
import type { InlineCompletionStatus } from "../../../main/inline-completion/service";
import { inlineCompletionBridge } from "../../lib/inline-completion/bridge";
import { useInlineCompletionStatus } from "../../lib/inline-completion/status";
import { cn } from "../../lib/utils";
import { formatBytes } from "../../../shared/format-bytes";
import { Button } from "../ui/button";
import { Switch } from "../ui/switch";

// The predictive-completion controls, shared by Settings › Code editor and the
// editor header's popover so the two can never disagree. Everything here is an
// INSTANT action, outside the save bar: it starts or stops a process and starts
// or cancels a download, and a control with a visible consequence that waited
// for Save would read as broken.

function modelLabel(status: InlineCompletionStatus, id: string): string {
	return status.models.find((m) => m.id === id)?.label ?? id;
}

/** What the feature is doing, in the fewest words - the settings row's value and the header chip's tooltip. */
export function inlineCompletionSummary(status: InlineCompletionStatus | null): string {
	if (!status) return "…";
	if (status.unsupported) return "Unavailable";
	if (status.download) {
		const pct =
			status.download.totalBytes > 0
				? Math.floor((100 * status.download.receivedBytes) / status.download.totalBytes)
				: 0;
		return `Downloading ${pct}%`;
	}
	// Asked, not yet answered: the switch reads on, so the summary must not say Off.
	if (status.confirm) return "Needs download";
	if (!status.enabled) return "Off";
	switch (status.server) {
		case "ready":
			return "On";
		case "starting":
			return "Starting";
		case "error":
			return "Error";
		default:
			return "Off";
	}
}

/**
 * The switch, the line saying what is happening, and - only when there is one -
 * the question about a download (with its size, before anything starts) or the
 * download's progress with its Cancel.
 */
export function InlineCompletionControls({ id = "inlineCompletionEnabled" }: { id?: string }) {
	const status = useInlineCompletionStatus();
	const [busy, setBusy] = useState(false);
	const act = (fn: () => Promise<void>) => {
		setBusy(true);
		void fn().finally(() => setBusy(false));
	};

	if (!status) {
		return <span className="text-[12px] text-passive">Reading…</span>;
	}
	if (status.unsupported) {
		return <p className="max-w-[62ch] text-[12px] leading-[1.6] text-passive">{status.unsupported}</p>;
	}

	const model = status.models.find((m) => m.id === status.modelId);
	// On while it runs, and ALSO while the question it raised is open: the person
	// just flipped it, and snapping back before they answered would undo them.
	const checked = status.enabled || status.confirm !== null || status.download !== null;

	return (
		<div className="flex flex-col gap-3" data-testid="inline-completion-controls">
			<div className="flex items-center gap-3">
				<Switch
					id={id}
					checked={checked}
					disabled={busy}
					onCheckedChange={(on) =>
						act(() =>
							on
								? inlineCompletionBridge().enable()
								: status.enabled
									? inlineCompletionBridge().disable()
									: inlineCompletionBridge().cancelDownload(),
						)
					}
				/>
				<label htmlFor={id} className="text-[12px] text-muted-foreground">
					Predict code as I type
				</label>
			</div>

			{/* What runs - one model, so a line rather than a picker. */}
			{model && <p className="max-w-[62ch] text-[11.5px] leading-[1.55] text-passive">{model.blurb}</p>}

			<StatusLine status={status} onRetry={() => act(() => inlineCompletionBridge().enable())} />

			{status.confirm && !status.download && (
				<div
					data-testid="inline-completion-confirm"
					className="flex max-w-[460px] flex-col gap-2.5 rounded-md border border-border bg-background/60 px-3 py-2.5"
				>
					<p className="text-[12px] leading-[1.55] text-foreground">
						Download {modelLabel(status, status.confirm.modelId)}
						{status.confirm.bytes > 0 ? (
							<>
								{" "}
								- <span className="tabular-nums">{formatBytes(status.confirm.bytes)}</span>
							</>
						) : null}
						?
					</p>
					<p className="text-[11.5px] leading-[1.55] text-passive">
						{status.confirm.runtimeBytes > 0 ? (
							<>
								{formatBytes(status.confirm.bytes - status.confirm.runtimeBytes)} model and{" "}
								{formatBytes(status.confirm.runtimeBytes)} llama.cpp runtime, into{" "}
							</>
						) : (
							<>Into </>
						)}
						<span className="font-mono">~/.ao/llm</span>. It runs only on this Mac.
						{status.confirm.freeBytes !== null && <> {formatBytes(status.confirm.freeBytes)} free.</>}
					</p>
					<div className="flex items-center gap-2">
						<Button
							type="button"
							size="sm"
							variant="primary"
							disabled={busy}
							onClick={() => act(() => inlineCompletionBridge().confirmDownload())}
						>
							Download
						</Button>
						<Button
							type="button"
							size="sm"
							variant="outline"
							disabled={busy}
							onClick={() => act(() => inlineCompletionBridge().cancelDownload())}
						>
							Cancel
						</Button>
					</div>
				</div>
			)}

			{status.download && (
				<DownloadProgress
					download={status.download}
					onCancel={() => act(() => inlineCompletionBridge().cancelDownload())}
				/>
			)}

			{status.downloadError && (
				<div className="flex max-w-[62ch] items-start gap-2 text-[12px] leading-[1.55]">
					<p className="min-w-0 text-error" data-testid="inline-completion-download-error">
						{status.downloadError}
					</p>
					{/* Asks again, with what is LEFT to fetch: a part already on disk is kept. */}
					<button
						type="button"
						onClick={() => act(() => inlineCompletionBridge().enable())}
						className="!text-[12px] shrink-0 text-foreground underline-offset-2 hover:underline"
					>
						Try again
					</button>
				</div>
			)}
		</div>
	);
}

function StatusLine({ status, onRetry }: { status: InlineCompletionStatus; onRetry: () => void }) {
	if (!status.enabled && status.server === "off") return null;
	// The download row below already says what is happening, with numbers.
	if (status.server === "off" && status.download) return null;
	const model = modelLabel(status, status.modelId);
	let dot = "bg-passive";
	let text: string;
	switch (status.server) {
		case "ready":
			dot = "bg-success";
			text = `Ready - ${model}`;
			break;
		case "starting":
			dot = "bg-warning";
			text = status.serverDetail ? `Starting - ${status.serverDetail}` : "Starting…";
			break;
		case "error":
			dot = "bg-error";
			text = status.serverDetail ?? "llama-server stopped";
			break;
		default:
			// On, but nothing running: the files are missing (a failed or not yet
			// confirmed download). Saying "Off" under a switch that reads on would
			// contradict it.
			text = status.models.find((m) => m.id === status.modelId)?.installed ? "Off" : `${model} is not downloaded yet`;
	}
	return (
		<div
			className="flex max-w-[62ch] items-start gap-2 text-[12px] leading-[1.5]"
			data-testid="inline-completion-status"
		>
			<span aria-hidden="true" className={cn("mt-[6px] h-1.5 w-1.5 shrink-0 rounded-full", dot)} />
			<span className="flex min-w-0 flex-col gap-0.5">
				<span className={cn(status.server === "error" ? "text-error" : "text-muted-foreground", "break-words")}>
					{text}
				</span>
				{status.server === "ready" && status.pid !== null && (
					<span className="whitespace-nowrap font-mono text-[11px] text-passive">llama-server · pid {status.pid}</span>
				)}
			</span>
			{status.server === "error" && (
				<button
					type="button"
					onClick={onRetry}
					className="!text-[12px] shrink-0 text-foreground underline-offset-2 hover:underline"
				>
					Retry
				</button>
			)}
		</div>
	);
}

function DownloadProgress({
	download,
	onCancel,
}: {
	download: NonNullable<InlineCompletionStatus["download"]>;
	onCancel: () => void;
}) {
	const pct = download.totalBytes > 0 ? Math.min(100, (100 * download.receivedBytes) / download.totalBytes) : 0;
	return (
		<div className="flex max-w-[460px] flex-col gap-2" data-testid="inline-completion-download">
			<div className="flex items-baseline gap-2 text-[12px]">
				<Loader2 className="h-3 w-3 shrink-0 animate-spin self-center text-passive" aria-hidden="true" />
				<span className="min-w-0 truncate text-muted-foreground">Downloading {download.label}</span>
				<span className="ml-auto shrink-0 font-mono text-[11px] text-passive">
					{formatBytes(download.receivedBytes)} / {formatBytes(download.totalBytes)}
				</span>
			</div>
			<div
				className="h-1 w-full overflow-hidden rounded-full bg-border"
				role="progressbar"
				aria-valuemin={0}
				aria-valuemax={100}
				aria-valuenow={Math.floor(pct)}
				aria-label={`Downloading ${download.label}`}
			>
				<div className="h-full rounded-full bg-accent transition-[width] duration-200" style={{ width: `${pct}%` }} />
			</div>
			<div>
				<Button type="button" size="sm" variant="outline" onClick={onCancel}>
					Cancel download
				</Button>
			</div>
		</div>
	);
}
