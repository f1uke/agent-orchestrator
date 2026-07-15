import * as Dialog from "@radix-ui/react-dialog";
import { TodoSpecEditor } from "./TodoSpecEditor";
import type { WorkspaceSession } from "../types/workspace";

type TodoDetailDialogProps = {
	/** The TODO session to inspect/edit, or null when the dialog is closed. */
	session: WorkspaceSession | null;
	onOpenChange: (open: boolean) => void;
	/** Called after a successful Start with the now-live session id. */
	onStarted: (sessionId: string) => void;
};

/**
 * The Board's TODO detail modal: a Radix dialog wrapping the shared
 * {@link TodoSpecEditor}. The editor owns every field and the Start / Delete /
 * autosave behaviour; this component only supplies the modal chrome (overlay,
 * centered card, grey TODO accent) and closes the dialog when the editor
 * finishes. The same editor renders as a first-class page in the session-detail
 * route ({@link TodoSessionPane}).
 */
export function TodoDetailDialog({ session, onOpenChange, onStarted }: TodoDetailDialogProps) {
	if (!session) return null;

	return (
		<Dialog.Root open onOpenChange={(next) => !next && onOpenChange(false)}>
			<Dialog.Portal>
				<Dialog.Overlay className="fixed inset-0 z-50 bg-black/55 data-[state=open]:animate-overlay-in" />
				<Dialog.Content
					aria-describedby={undefined}
					// Grey top accent marks the TODO detail modal (vs the create modal's blue).
					style={{ borderTopColor: "var(--lane-todo)", borderTopWidth: 3 }}
					className="fixed left-1/2 top-1/2 z-50 flex max-h-[calc(100vh-32px)] w-[min(600px,calc(100vw-32px))] -translate-x-1/2 -translate-y-1/2 flex-col rounded-lg border border-border bg-popover p-0 text-popover-foreground shadow-xl data-[state=open]:animate-modal-in"
				>
					{/* Radix requires a Title for the dialog's accessible name; the visible
					    editable name input carries its own aria-label, so this stays sr-only. */}
					<Dialog.Title className="sr-only">Edit TODO task</Dialog.Title>
					<TodoSpecEditor
						session={session}
						onStarted={(id) => {
							onStarted(id);
							onOpenChange(false);
						}}
						onDeleted={() => onOpenChange(false)}
						onClose={() => onOpenChange(false)}
					/>
				</Dialog.Content>
			</Dialog.Portal>
		</Dialog.Root>
	);
}
