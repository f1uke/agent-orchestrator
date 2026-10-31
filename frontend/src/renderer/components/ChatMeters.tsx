import type { ContextBreakdown, ContextCategory } from "../lib/chat-context";
import { type ContextUsage, formatTokens } from "../lib/chat-inspect";
import type { SubscriptionUsage, UsageWindow } from "../lib/chat-usage";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";

const CATEGORY_COLOR: Record<string, string> = {
	system_prompt: "var(--fg-passive)",
	system_tools: "var(--purple)",
	mcp_tools: "var(--orange)",
	memory_files: "var(--green)",
	custom_agents: "var(--red)",
	skills: "var(--amber)",
	messages: "var(--accent)",
};
const SPARE = ["var(--orange)", "var(--green)", "var(--red)", "var(--amber)"];

function colorOf(category: ContextCategory, spareIndex: number): string {
	return CATEGORY_COLOR[category.id] ?? SPARE[spareIndex % SPARE.length];
}

function Ring({ percent }: { percent: number }) {
	const r = 6;
	const length = 2 * Math.PI * r;
	const filled = (Math.min(100, Math.max(0, percent)) / 100) * length;
	return (
		<svg aria-hidden="true" className="chat-meter__ring" viewBox="0 0 16 16">
			<circle cx="8" cy="8" r={r} />
			<circle cx="8" cy="8" r={r} data-fill="" strokeDasharray={`${filled} ${length}`} />
		</svg>
	);
}

type ContextMeterProps = { breakdown?: ContextBreakdown; fallback?: ContextUsage };

export function ContextMeter({ breakdown, fallback }: ContextMeterProps) {
	if (breakdown) return <ExactContext breakdown={breakdown} />;
	if (fallback) return <EstimatedContext fallback={fallback} />;
	return null;
}

function ContextPopover({ label, percent, children }: { label: string; percent: number; children: React.ReactNode }) {
	return (
		<Popover>
			<PopoverTrigger aria-label={`Context window: ${label}`} className="chat-meter" type="button">
				<Ring percent={percent} />
				{label}
			</PopoverTrigger>
			<PopoverContent aria-label="Context window" align="end" className="chat-meter-pop" side="top">
				{children}
			</PopoverContent>
		</Popover>
	);
}

function ExactContext({ breakdown }: { breakdown: ContextBreakdown }) {
	const used = breakdown.categories.filter((c) => c.kind === "used");
	const rest = breakdown.categories.filter((c) => c.kind !== "used");
	const colors = new Map(used.map((c, i) => [c.id, colorOf(c, i)]));
	const percent = `${breakdown.percentage}%`;
	return (
		<ContextPopover label={percent} percent={breakdown.percentage}>
			<header className="chat-meter-pop__head">
				<span>
					<b>{percent}</b> of context used
				</span>
				<span className="chat-meter-pop__total">
					{formatTokens(breakdown.totalTokens)} / {formatTokens(breakdown.maxTokens)}
				</span>
			</header>
			<div className="chat-meter-pop__bar">
				{used.map((c) => (
					<span
						data-segment={c.id}
						key={c.id}
						style={{ width: `${(c.tokens / breakdown.maxTokens) * 100}%`, background: colors.get(c.id) }}
					/>
				))}
			</div>
			<ul className="chat-meter-pop__rows">
				{[...used, ...rest].map((c) => (
					<li data-muted={c.kind === "used" ? undefined : ""} key={c.id}>
						<span aria-hidden="true" className="chat-meter-pop__swatch" style={{ background: colors.get(c.id) }} />
						<span className="chat-meter-pop__name">{c.name}</span>
						<span className="chat-meter-pop__tokens">{formatTokens(c.tokens)}</span>
					</li>
				))}
			</ul>
		</ContextPopover>
	);
}

function EstimatedContext({ fallback }: { fallback: ContextUsage }) {
	const { tokens, window } = fallback;
	const percent = window ? Math.min(100, Math.round((tokens / window) * 100)) : undefined;
	const label = percent === undefined ? `~${formatTokens(tokens)}` : `~${percent}%`;
	return (
		<ContextPopover label={label} percent={percent ?? 0}>
			<header className="chat-meter-pop__head">
				<span>
					{percent === undefined ? (
						"Context used"
					) : (
						<>
							<b>~{percent}%</b> of context used
						</>
					)}
				</span>
				<span className="chat-meter-pop__total">
					~{formatTokens(tokens)}
					{window ? ` / ${formatTokens(window)}` : ""}
				</span>
			</header>
			<p className="chat-meter-pop__note">estimated from the last response</p>
		</ContextPopover>
	);
}

const percentOf = (w: UsageWindow) => Math.min(100, Math.max(0, Math.round(w.utilization * 100)));
const pct = (w: UsageWindow) => `${percentOf(w)}%`;

const STATUS_LABEL: Record<string, string> = {
	allowed: "Within the limit",
	allowed_warning: "Near the limit",
	rejected: "Limit reached",
};

function resetTime(iso: string): string {
	return new Date(iso).toLocaleString(undefined, { weekday: "short", hour: "2-digit", minute: "2-digit" });
}

function updatedAgo(iso: string, now = Date.now()): string {
	const minutes = Math.floor((now - Date.parse(iso)) / 60_000);
	if (Number.isNaN(minutes)) return "";
	if (minutes < 1) return "updated just now";
	if (minutes < 60) return `updated ${minutes} min ago`;
	const hours = Math.floor(minutes / 60);
	return `updated ${hours} h ago`;
}

export function UsageMeter({ usage }: { usage?: SubscriptionUsage }) {
	if (!usage || (!usage.fiveHour && !usage.sevenDay)) return null;
	const windows: Array<[string, string, UsageWindow]> = [];
	if (usage.fiveHour) windows.push(["5h", "5-hour window", usage.fiveHour]);
	if (usage.sevenDay) windows.push(["week", "Weekly window", usage.sevenDay]);
	return (
		<Popover>
			<PopoverTrigger aria-label="Subscription usage" className="chat-meter" type="button">
				{windows.map(([short, , w]) => `${short} ${pct(w)}`).join(" · ")}
			</PopoverTrigger>
			<PopoverContent aria-label="Subscription usage" align="end" className="chat-meter-pop" side="top">
				<header className="chat-meter-pop__head">
					<span>Subscription usage</span>
				</header>
				<ul className="chat-meter-pop__rows">
					{windows.map(([short, name, w]) => (
						<li className="chat-meter-pop__window" data-status={w.status} key={short}>
							<span className="chat-meter-pop__name">{name}</span>
							<span className="chat-meter-pop__tokens">{pct(w)}</span>
							<div
								aria-label={name}
								aria-valuemax={100}
								aria-valuemin={0}
								aria-valuenow={percentOf(w)}
								className="chat-meter-pop__bar chat-meter-pop__usage-bar"
								role="meter"
							>
								<span style={{ width: pct(w) }} />
							</div>
							<span className="chat-meter-pop__sub">resets {resetTime(w.resetsAt)}</span>
							<span className="chat-meter-pop__status">{STATUS_LABEL[w.status] ?? w.status}</span>
						</li>
					))}
				</ul>
				<p className="chat-meter-pop__note">{updatedAgo(usage.updatedAt)}</p>
			</PopoverContent>
		</Popover>
	);
}
