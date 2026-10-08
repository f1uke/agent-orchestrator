import { SUBSCRIPTION_PROFILE, sameProfile, type ClaudeProfile } from "../lib/claude-profiles";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

const INHERIT = "__inherit__";

// `value` "" is the empty choice: Subscription, or, given `inheritLabel`, whatever
// the project default is.
export function ClaudeProfileSelect({
	id,
	profiles,
	value,
	onChange,
	inheritLabel,
	className,
}: {
	id: string;
	profiles: ClaudeProfile[];
	value: string;
	onChange: (value: string) => void;
	inheritLabel?: string;
	className?: string;
}) {
	const names = profiles.map((p) => p.name);
	if (!names.some((n) => sameProfile(n, SUBSCRIPTION_PROFILE))) names.unshift(SUBSCRIPTION_PROFILE);
	if (value && !names.some((n) => sameProfile(n, value))) names.push(value);
	const empty = inheritLabel ? INHERIT : SUBSCRIPTION_PROFILE;
	const selected = value ? (names.find((n) => sameProfile(n, value)) ?? value) : empty;

	return (
		<Select
			value={selected}
			onValueChange={(v) => onChange(v === INHERIT || (!inheritLabel && v === SUBSCRIPTION_PROFILE) ? "" : v)}
		>
			<SelectTrigger id={id} className={className}>
				<SelectValue />
			</SelectTrigger>
			<SelectContent>
				{inheritLabel && <SelectItem value={INHERIT}>{inheritLabel}</SelectItem>}
				{names.map((name) => (
					<SelectItem key={name} value={name}>
						{name}
					</SelectItem>
				))}
			</SelectContent>
		</Select>
	);
}
