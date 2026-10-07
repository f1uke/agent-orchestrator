import { CheckCircle2, Loader2, TriangleAlert, XCircle } from "lucide-react";
import type { RunXcodegenResult, XcodegenDirResult } from "../../main/run-xcodegen";
import { Sheet, SheetContent, SheetDescription, SheetHeader, SheetTitle } from "./ui/sheet";

/**
 * The lifecycle of a single "Run xcodegen" invocation as the menu sees it:
 * pending (`running`), a resolved backend `result`, or an unexpected IPC/main
 * failure (`error`).
 */
export type XcodegenViewState =
	{ phase: "running" } | { phase: "error" } | { phase: "done"; result: RunXcodegenResult };

type XcodegenResultSheetProps = {
	open: boolean;
	onOpenChange: (open: boolean) => void;
	state: XcodegenViewState | null;
};

function subtitle(state: XcodegenViewState | null): string {
	if (!state || state.phase === "running") return "Searching for project.yml and generating Xcode projects…";
	if (state.phase === "error") return "The command could not be run.";
	switch (state.result.status) {
		case "not-installed":
			return "xcodegen is not available.";
		case "no-specs":
			return "No xcodegen spec found.";
		case "ran": {
			const total = state.result.results.length;
			const ok = state.result.results.filter((r) => r.ok).length;
			return `Ran in ${total} ${total === 1 ? "directory" : "directories"} · ${ok}/${total} succeeded.`;
		}
	}
}

/** A short, single-line, monospace command output block (scrolls if long). */
function OutputBlock({ output }: { output: string }) {
	if (!output) return null;
	return (
		<pre className="mt-2 max-h-64 overflow-auto whitespace-pre-wrap break-all rounded-md bg-muted/60 p-2 font-mono text-[12px] text-muted-foreground">
			{output}
		</pre>
	);
}

function DirRow({ result }: { result: XcodegenDirResult }) {
	return (
		<li className="rounded-md border border-border bg-card/40 p-3">
			<div className="flex items-center gap-2">
				{result.ok ? (
					<CheckCircle2 className="h-4 w-4 shrink-0 text-success" aria-hidden="true" />
				) : (
					<XCircle className="h-4 w-4 shrink-0 text-destructive" aria-hidden="true" />
				)}
				<code className="font-mono text-[13px] text-foreground">{result.dir}</code>
				{!result.ok && (
					<span className="ml-auto text-[12px] text-destructive">exited {result.exitCode ?? "with an error"}</span>
				)}
			</div>
			<OutputBlock output={result.output} />
		</li>
	);
}

function Body({ state }: { state: XcodegenViewState | null }) {
	if (!state || state.phase === "running") {
		return (
			<div className="flex items-center gap-2 text-[13px] text-muted-foreground">
				<Loader2 className="h-4 w-4 animate-spin" aria-hidden="true" />
				Running xcodegen…
			</div>
		);
	}

	if (state.phase === "error") {
		return (
			<div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-[13px] text-destructive">
				<TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
				<span>Something went wrong running xcodegen. Please try again.</span>
			</div>
		);
	}

	const result = state.result;
	if (result.status === "not-installed") {
		return (
			<div className="space-y-3">
				<div className="flex items-start gap-2 rounded-md border border-destructive/40 bg-destructive/10 p-3 text-[13px] text-destructive">
					<TriangleAlert className="mt-0.5 h-4 w-4 shrink-0" aria-hidden="true" />
					<span>xcodegen isn't installed or isn't on your PATH.</span>
				</div>
				<div className="text-[13px] text-muted-foreground">
					Install it, then try again:
					<pre className="mt-2 rounded-md bg-muted/60 p-2 font-mono text-[12px] text-foreground">
						brew install xcodegen
					</pre>
				</div>
			</div>
		);
	}

	if (result.status === "no-specs") {
		return (
			<div className="text-[13px] text-muted-foreground">
				No <code className="font-mono text-foreground">project.yml</code> found under{" "}
				<code className="font-mono text-foreground">{result.root}</code>. Nothing to generate.
			</div>
		);
	}

	return (
		<ul className="space-y-2">
			{result.results.map((r) => (
				<DirRow key={r.dir} result={r} />
			))}
		</ul>
	);
}

/**
 * A slide-in panel presenting the outcome of a "Run xcodegen" action: a running
 * spinner, then per-directory success/failure with output, or a friendly
 * "no spec found" / "not installed" message.
 */
export function XcodegenResultSheet({ open, onOpenChange, state }: XcodegenResultSheetProps) {
	return (
		<Sheet open={open} onOpenChange={onOpenChange}>
			<SheetContent side="right" className="w-full gap-0 sm:max-w-lg">
				<SheetHeader className="border-b border-border">
					<SheetTitle>Run xcodegen</SheetTitle>
					<SheetDescription>{subtitle(state)}</SheetDescription>
				</SheetHeader>
				<div className="flex-1 overflow-y-auto p-4">
					<Body state={state} />
				</div>
			</SheetContent>
		</Sheet>
	);
}
