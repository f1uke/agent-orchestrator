/**
 * Filename-first file/line header for a review thread: the basename is
 * prominent, the directory is dimmed and left-truncated, and the full path
 * is available as a tooltip via `title`.
 */
export function FileHeader({ path, line }: { path: string; line: number }) {
	const slash = path.lastIndexOf("/");
	const dir = slash >= 0 ? path.slice(0, slash + 1) : "";
	const name = slash >= 0 ? path.slice(slash + 1) : path;
	return (
		<span className="flex min-w-0 items-baseline gap-0 font-mono text-[11.5px]" title={path}>
			{dir && (
				<span className="truncate text-muted-foreground" dir="rtl">
					{dir}
				</span>
			)}
			<span className="shrink-0 text-foreground">{name}</span>
			{line > 0 && <span className="shrink-0 text-muted-foreground">:{line}</span>}
		</span>
	);
}
