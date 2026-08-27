/**
 * The Files rail's centred empty state — an icon, a title, a sentence.
 *
 * Extracted from `FilesPanel` when Search became the rail's third mode: all
 * three modes have to say "there is nothing here" in the same voice and the same
 * place, and a second copy of six lines is how two rails start looking like two
 * apps.
 */
export function EmptyState({ icon, title, detail }: { icon: React.ReactNode; title: string; detail: string }) {
	return (
		<div className="files-panel__empty">
			<span className="files-panel__empty-icon">{icon}</span>
			<span className="files-panel__empty-title">{title}</span>
			<span className="files-panel__empty-text">{detail}</span>
		</div>
	);
}
