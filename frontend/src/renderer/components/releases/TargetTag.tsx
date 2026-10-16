import type { TargetChip } from "../../lib/releases/llb";

export function TargetTag({ chip, className = "" }: { chip: TargetChip; className?: string }) {
	const kind = chip.epic ? " epic" : chip.tone ? "" : " none";
	return (
		<span
			className={`tgt${kind}${className ? ` ${className}` : ""}`}
			data-st={chip.tone ?? undefined}
			data-testid="llb-target"
			title={chip.title}
		>
			{chip.text}
		</span>
	);
}
