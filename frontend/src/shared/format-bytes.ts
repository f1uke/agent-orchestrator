/**
 * A byte count the way macOS states sizes (Finder, About This Mac, the disk
 * itself): DECIMAL units. A download shown as "1.5 GB" here and "1.65 GB" on the
 * model's own page reads as two different files.
 */
export function formatBytes(bytes: number): string {
	if (bytes >= 1e9) return `${(bytes / 1e9).toFixed(1)} GB`;
	if (bytes >= 1e6) return `${Math.round(bytes / 1e6)} MB`;
	if (bytes >= 1e3) return `${Math.round(bytes / 1e3)} KB`;
	return `${bytes} B`;
}
