import { useState, type ReactNode } from "react";
import { Check, Copy, Lock } from "lucide-react";

// SettingsReadOnlyPanel is the distinct read-only surface for facts the user
// cannot edit — Identity (id/kind/path/repo) and Workspace repos (proposal §6).
// It sits above the editable controls, on a flatter/recessed surface with a
// READ-ONLY mono eyebrow + lock, so it is visually separated from the raised
// group cards and no Save affordance touches it.
export function SettingsReadOnlyPanel({ title, children }: { title: string; children: ReactNode }) {
	return (
		<div className="mb-5 rounded-xl border border-border bg-background/60 px-4 py-3.5">
			<div className="mb-3 flex items-center gap-2">
				<Lock className="h-3 w-3 text-passive" aria-hidden="true" />
				<span className="font-mono text-[9.5px] uppercase tracking-[0.14em] text-passive">{title}</span>
			</div>
			<div className="flex flex-col gap-2.5 font-mono text-[12px]">{children}</div>
		</div>
	);
}

// One key/value row with a hover-revealed copy button. Copyable rows expose the
// full value (path/repo/id) which is otherwise truncated for layout.
export function ReadonlyRow({ label, value, copyable = false }: { label: string; value: string; copyable?: boolean }) {
	const [copied, setCopied] = useState(false);
	const copy = () => {
		void navigator.clipboard?.writeText(value);
		setCopied(true);
		window.setTimeout(() => setCopied(false), 1200);
	};
	return (
		<div className="group grid grid-cols-[52px_minmax(0,1fr)_auto] items-center gap-3">
			<span className="text-passive">{label}</span>
			<span className="min-w-0 truncate text-foreground">{value}</span>
			{copyable ? (
				<button
					type="button"
					aria-label={`Copy ${label}`}
					onClick={copy}
					className="text-passive opacity-0 transition-opacity hover:text-foreground focus-visible:opacity-100 group-hover:opacity-100"
				>
					{copied ? <Check className="h-3 w-3" aria-hidden="true" /> : <Copy className="h-3 w-3" aria-hidden="true" />}
				</button>
			) : (
				<span />
			)}
		</div>
	);
}
