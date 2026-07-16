import * as Dialog from "@radix-ui/react-dialog";
import { FileCode } from "lucide-react";
import { useOverlayDismissFocus } from "../lib/overlay-focus";

type FileCandidatePickerProps = {
	open: boolean;
	/** Workspace-relative candidate paths a terminal ref resolved to. */
	candidates: string[];
	onPick: (path: string) => void;
	onOpenChange: (open: boolean) => void;
};

/**
 * Disambiguation picker shown when a terminal file reference resolves to more
 * than one workspace file (e.g. a bare `config.go` present in several packages).
 * Picking a row opens that file in the workspace viewer.
 */
export function FileCandidatePicker({ open, candidates, onPick, onOpenChange }: FileCandidatePickerProps) {
	const dismissFocus = useOverlayDismissFocus();
	return (
		<Dialog.Root open={open} onOpenChange={onOpenChange}>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-50 bg-black/50" />
				<Dialog.Content
					{...dismissFocus}
					className="fixed left-1/2 top-1/2 z-50 w-[520px] max-w-[90vw] -translate-x-1/2 -translate-y-1/2 rounded-lg border border-border bg-surface p-4 shadow-lg"
				>
					<Dialog.Title className="text-sm font-medium text-foreground">Open which file?</Dialog.Title>
					<Dialog.Description className="mt-1 text-[12px] text-muted-foreground">
						This reference matches several files in the workspace.
					</Dialog.Description>
					<ul className="mt-3 flex max-h-[320px] flex-col gap-0.5 overflow-auto">
						{candidates.map((path) => (
							<li key={path}>
								<button
									type="button"
									onClick={() => onPick(path)}
									className="flex w-full items-center gap-2 rounded-md px-2.5 py-1.5 text-left font-mono text-[12.5px] text-foreground transition hover:bg-interactive-hover"
								>
									<FileCode aria-hidden="true" className="h-3.5 w-3.5 shrink-0 text-muted-foreground" />
									<span className="truncate">{path}</span>
								</button>
							</li>
						))}
					</ul>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
