import { useState } from "react";
import { useQuery } from "@tanstack/react-query";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import type { components } from "../../api/schema";

type DiffContext = components["schemas"]["DiffContextResponse"];
type Mode = "hunk" | "file";

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
	return (
		<div className="overflow-x-auto border-b border-border bg-raised font-mono text-[11.5px]">
			{ctx.lines.map((l, i) => (
				<div
					key={`${l.kind}-${l.oldLine}-${l.newLine}-${i}`}
					className={
						l.kind === "add"
							? "bg-success/10 text-success"
							: l.kind === "del"
								? "bg-error/10 text-error"
								: "text-muted-foreground"
					}
				>
					<span className="inline-block w-10 shrink-0 select-none pr-2 text-right opacity-50">
						{l.newLine || l.oldLine || ""}
					</span>
					<span className="select-none opacity-70">{lineSign(l.kind)}</span>
					<span className="whitespace-pre">{l.text}</span>
				</div>
			))}
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
