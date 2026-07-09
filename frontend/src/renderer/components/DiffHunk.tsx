import { useEffect, useRef, useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { Check, Copy } from "lucide-react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { highlightLine, languageForPath } from "../lib/highlight";
import type { components } from "../../api/schema";

type DiffContext = components["schemas"]["DiffContextResponse"];
type Mode = "hunk" | "file";

const COPIED_STATE_MS = 1500;

/**
 * Shows the code a review comment anchors to: the surrounding diff hunk by
 * default, expandable to the full file. Renders nothing when the backend has
 * no code context available (e.g. preview/demo mode) so the thread still
 * shows its file:line header and comments.
 */
export function DiffHunk({
	sessionId,
	prUrl,
	path,
	line,
}: {
	sessionId: string;
	prUrl: string;
	path: string;
	line: number;
}) {
	const [mode, setMode] = useState<Mode>("hunk");
	const [copied, setCopied] = useState(false);
	const copiedTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

	useEffect(
		() => () => {
			if (copiedTimer.current) clearTimeout(copiedTimer.current);
		},
		[],
	);

	const query = useQuery({
		queryKey: ["diff-context", sessionId, prUrl, path, line, mode],
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/sessions/{sessionId}/diff-context", {
				params: { path: { sessionId }, query: { prUrl, path, line, mode } },
			});
			if (error) throw new Error(apiErrorMessage(error, "Unable to load code"));
			return data as DiffContext;
		},
	});

	if (query.isLoading) {
		return <div className="px-3 py-1 text-[11px] text-muted-foreground">Loading code…</div>;
	}
	const ctx = query.data;
	if (!ctx || !ctx.available || ctx.lines.length === 0) {
		// No code context; the thread still shows its file:line header and
		// comments (also keeps preview/demo mode clean when the backend isn't reachable).
		return null;
	}
	const lang = languageForPath(path);

	const copyCode = () => {
		const code = ctx.lines.map((l) => l.text).join("\n");
		if (!navigator.clipboard) return;
		navigator.clipboard
			.writeText(code)
			.then(() => {
				setCopied(true);
				if (copiedTimer.current) clearTimeout(copiedTimer.current);
				copiedTimer.current = setTimeout(() => setCopied(false), COPIED_STATE_MS);
			})
			.catch(() => {
				// Clipboard write failed (e.g. permission denied); no-op.
			});
	};

	return (
		<div className="group relative border-b border-border bg-raised font-mono text-[11.5px]">
			<button
				type="button"
				aria-label={copied ? "Copied" : "Copy code"}
				className="absolute right-1.5 top-1.5 z-10 rounded border border-border bg-raised/90 p-1 text-muted-foreground opacity-0 transition hover:text-foreground group-hover:opacity-100"
				onClick={copyCode}
			>
				{copied ? <Check className="h-3 w-3" aria-hidden="true" /> : <Copy className="h-3 w-3" aria-hidden="true" />}
			</button>
			<div className="overflow-x-auto">
				{/* w-max + min-w-full: rows stretch to the widest line so the add/del
				    tint covers the full scroll width (not just the visible viewport). */}
				<div className="w-max min-w-full">
					{ctx.lines.map((l, i) => (
						<div
							key={`${l.kind}-${l.oldLine}-${l.newLine}-${i}`}
							className={
								l.kind === "add"
									? "bg-success/10 text-success leading-[1.45]"
									: l.kind === "del"
										? "bg-error/10 text-error leading-[1.45]"
										: "text-muted-foreground leading-[1.45]"
							}
						>
							<span className="inline-block w-10 shrink-0 select-none pr-2 text-right opacity-50">
								{l.newLine || l.oldLine || ""}
							</span>
							<span className="select-none opacity-70">{lineSign(l.kind)}</span>
							<span className="whitespace-pre" dangerouslySetInnerHTML={{ __html: highlightLine(l.text, lang) }} />
						</div>
					))}
				</div>
			</div>
			{mode === "hunk" && (
				<button
					type="button"
					className="w-full py-1 text-[11px] text-accent hover:underline"
					onClick={() => setMode("file")}
				>
					Expand full file
				</button>
			)}
			{mode === "file" && ctx.truncated && (
				<div className="px-3 py-1 text-[11px] text-muted-foreground">File truncated…</div>
			)}
		</div>
	);
}

function lineSign(kind: string): string {
	if (kind === "add") return "+";
	if (kind === "del") return "-";
	return " ";
}
