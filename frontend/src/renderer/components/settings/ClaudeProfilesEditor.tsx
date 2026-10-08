import { Plus, X } from "lucide-react";
import type { ClaudeProfile, ClaudeProfileInput } from "../../lib/claude-profiles";
import { Button } from "../ui/button";
import { Input } from "../ui/input";

const NAME_CLASS = "h-8 w-[150px] shrink-0 text-[13px]";
const FILE_CLASS = "h-8 min-w-0 flex-1 font-mono text-[12.5px]";

export function ClaudeProfilesEditor({
	builtins,
	profiles,
	error,
	onChange,
}: {
	builtins: ClaudeProfile[];
	profiles: ClaudeProfileInput[];
	error?: string;
	onChange: (profiles: ClaudeProfileInput[]) => void;
}) {
	const update = (index: number, patch: Partial<ClaudeProfileInput>) =>
		onChange(profiles.map((p, i) => (i === index ? { ...p, ...patch } : p)));

	return (
		<div className="flex max-w-[620px] flex-col gap-2.5">
			<ul aria-label="Built-in Claude profiles" className="flex flex-col gap-1">
				{builtins.map((p) => (
					<li key={p.name} className="flex h-8 min-w-0 items-center gap-2 px-2.5 text-[12.5px]">
						<span className="w-[150px] shrink-0 truncate text-foreground">{p.name}</span>
						<span className="min-w-0 flex-1 truncate font-mono text-muted-foreground" title={p.settingsFile}>
							{p.settingsFile || "No settings file - Claude's own login"}
						</span>
						<span className="inline-flex shrink-0 items-center rounded-full border border-border-strong px-2 py-[1px] font-mono text-[10px] uppercase tracking-[0.05em] text-passive">
							Built-in
						</span>
					</li>
				))}
			</ul>

			{profiles.length > 0 && (
				<ul aria-label="Your Claude profiles" className="flex flex-col gap-1.5">
					{profiles.map((p, i) => (
						<li key={i} className="flex min-w-0 items-center gap-2">
							<Input
								aria-label={`Profile ${i + 1} name`}
								className={NAME_CLASS}
								placeholder="Name"
								spellCheck={false}
								value={p.name}
								onChange={(e) => update(i, { name: e.target.value })}
							/>
							<Input
								aria-label={`Profile ${i + 1} settings file`}
								className={FILE_CLASS}
								placeholder="~/.claude/settings-<name>.json"
								spellCheck={false}
								value={p.settingsFile}
								onChange={(e) => update(i, { settingsFile: e.target.value })}
							/>
							<Button
								type="button"
								variant="ghost"
								size="icon-sm"
								aria-label={`Remove profile ${p.name.trim() || i + 1}`}
								onClick={() => onChange(profiles.filter((_, j) => j !== i))}
							>
								<X className="h-3.5 w-3.5" aria-hidden="true" />
							</Button>
						</li>
					))}
				</ul>
			)}

			<div>
				<Button
					type="button"
					variant="ghost"
					size="sm"
					onClick={() => onChange([...profiles, { name: "", settingsFile: "" }])}
				>
					<Plus className="h-3.5 w-3.5" aria-hidden="true" />
					Add profile
				</Button>
			</div>

			{error ? (
				<p role="alert" className="max-w-[62ch] text-[11.5px] leading-[1.5] [overflow-wrap:anywhere] text-error">
					{error}
				</p>
			) : null}
		</div>
	);
}
