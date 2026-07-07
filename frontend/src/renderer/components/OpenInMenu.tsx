import { Code2, FolderOpen, Hammer, Share, SquareTerminal } from "lucide-react";
import { useEffect, useRef, useState } from "react";
import type { OpenInTargets } from "../../main/open-in-targets";
import { aoBridge } from "../lib/bridge";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";

type OpenInMenuProps = {
	/** The working directory to open. When absent, the menu renders nothing. */
	directory?: string;
};

// The launchers shell out to macOS-only tools (`open`, Ghostty/Terminal, Xcode),
// so the menu is macOS-only; off-mac the trigger is hidden and the main-process
// handlers no-op defensively.
function isMacPlatform(): boolean {
	return typeof navigator !== "undefined" && /Mac|iPod|iPhone|iPad/.test(navigator.userAgent);
}

const TOAST_DISMISS_MS = 4000;

/**
 * A share-style terminal-toolbar button that opens the session's working
 * directory in Terminal (Ghostty when installed, else Terminal.app), Finder,
 * VS Code (when installed), or an Xcode workspace/project detected at the
 * directory root. Detection runs in the Electron main process (fs + installed
 * apps); a failed launch surfaces a toast rather than crashing.
 */
export function OpenInMenu({ directory }: OpenInMenuProps) {
	const [open, setOpen] = useState(false);
	const [targets, setTargets] = useState<OpenInTargets | null>(null);
	const [toast, setToast] = useState<string | null>(null);
	const toastTimer = useRef<ReturnType<typeof setTimeout> | null>(null);

	// Detect conditional targets when the menu opens (and whenever the directory
	// changes while open). Terminal + Finder are always shown, so an empty/failed
	// detection just hides the VS Code / Xcode items.
	useEffect(() => {
		if (!open || !directory) return;
		let cancelled = false;
		aoBridge.openIn
			.detectTargets(directory)
			.then((result) => {
				if (!cancelled) setTargets(result);
			})
			.catch(() => {
				if (!cancelled) setTargets({ hasVSCode: false });
			});
		return () => {
			cancelled = true;
		};
	}, [open, directory]);

	useEffect(
		() => () => {
			if (toastTimer.current) clearTimeout(toastTimer.current);
		},
		[],
	);

	if (!directory || !isMacPlatform()) return null;

	const showToast = (message: string) => {
		setToast(message);
		if (toastTimer.current) clearTimeout(toastTimer.current);
		toastTimer.current = setTimeout(() => setToast(null), TOAST_DISMISS_MS);
	};

	const run = (label: string, action: () => Promise<void>) => {
		void action().catch((error) => {
			console.error(`"Open in ${label}" failed:`, error);
			showToast(`Couldn't open in ${label}.`);
		});
	};

	const xcode = targets?.xcode;

	return (
		<>
			<DropdownMenu open={open} onOpenChange={setOpen}>
				<DropdownMenuTrigger asChild>
					<button
						aria-label="Open in…"
						className="terminal-toolbar__control terminal-toolbar__control--icon"
						title="Open in…"
						type="button"
					>
						<Share className="h-3.5 w-3.5" aria-hidden="true" />
					</button>
				</DropdownMenuTrigger>
				<DropdownMenuContent align="end" className="min-w-52">
					<DropdownMenuItem onSelect={() => run("Terminal", () => aoBridge.openIn.terminal(directory))}>
						<SquareTerminal aria-hidden="true" />
						Open in Terminal
					</DropdownMenuItem>
					<DropdownMenuItem onSelect={() => run("Finder", () => aoBridge.openIn.finder(directory))}>
						<FolderOpen aria-hidden="true" />
						Open in Finder
					</DropdownMenuItem>
					{(xcode || targets?.hasVSCode) && <DropdownMenuSeparator />}
					{xcode && (
						<DropdownMenuItem onSelect={() => run(xcode.name, () => aoBridge.openIn.xcode(xcode.path))}>
							<Hammer aria-hidden="true" />
							Open {xcode.name}
						</DropdownMenuItem>
					)}
					{targets?.hasVSCode && (
						<DropdownMenuItem onSelect={() => run("Visual Studio Code", () => aoBridge.openIn.editor(directory))}>
							<Code2 aria-hidden="true" />
							Open in Visual Studio Code
						</DropdownMenuItem>
					)}
				</DropdownMenuContent>
			</DropdownMenu>
			{toast && (
				<div
					className="fixed bottom-5 left-1/2 z-[100] -translate-x-1/2 rounded-lg border border-border bg-popover px-3.5 py-2 text-[13px] text-foreground shadow-[var(--shadow)]"
					role="status"
				>
					{toast}
				</div>
			)}
		</>
	);
}
