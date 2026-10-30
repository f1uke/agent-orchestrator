import {
	Bot,
	Check,
	ChevronRight,
	FilePlus,
	FileText,
	ListChecks,
	Loader2,
	type LucideIcon,
	MessageSquare,
	Pencil,
	Search,
	Shield,
	SquareTerminal,
	Wrench,
	X,
} from "lucide-react";
import { useState } from "react";
import type { Pending, ToolCallItem } from "../lib/chat-fold";
import { isObject } from "../lib/chat-event";
import {
	type Settled,
	type ToolBody,
	type ToolView,
	describeSettled,
	describeTool,
	describeUpdates,
	parsePlan,
	parseQuestions,
} from "../lib/chat-tools";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";
import { DiffRows } from "./DiffRows";
import { NoteMarkdown } from "./NoteMarkdown";
import { Button } from "./ui/button";
import { SimpleTooltip } from "./ui/tooltip";

const FAMILY_ICON: Record<ToolView["family"], LucideIcon> = {
	bash: SquareTerminal,
	edit: Pencil,
	write: FilePlus,
	read: FileText,
	search: Search,
	agent: Bot,
	plan: ListChecks,
	question: MessageSquare,
	other: Wrench,
};

export const READ_ONLY_HINT = "Answering arrives in the next version";

function Status({ view }: { view: ToolView }) {
	const { status } = view;
	switch (status.tone) {
		case "running":
			return (
				<span className="chat-tool__status">
					<Loader2 className="chat-icon animate-spin" aria-hidden="true" />
					running
				</span>
			);
		case "diff":
			return (
				<span className="chat-tool__status">
					<span className="chat-tool__add">+{status.added}</span>
					{status.newFile ? <span>new file</span> : <span className="chat-tool__del">−{status.removed}</span>}
				</span>
			);
		case "ok":
			return (
				<span className="chat-tool__status chat-tool__status--ok">
					<Check className="chat-icon" aria-hidden="true" />
					{status.text}
				</span>
			);
		case "error":
			return (
				<span className="chat-tool__status chat-tool__status--error">
					<X className="chat-icon" aria-hidden="true" />
					{status.text}
				</span>
			);
	}
}

function outputTone(line: string, failed: boolean): string | undefined {
	if (!failed) return undefined;
	if (/^(--- )?FAIL\b|^panic:|^error\b/i.test(line)) return "chat-term__line--fail";
	if (/^ok\s/.test(line)) return "chat-term__line--pass";
	return undefined;
}

function ToolBodyView({ body, theme }: { body: ToolBody; theme: Theme }) {
	switch (body.kind) {
		case "bash":
			return (
				<div className="chat-term">
					<span className="chat-term__cmd">$ {body.command}</span>
					{body.output
						? body.output.split("\n").map((line, i) => (
								<span className={outputTone(line, body.failed)} key={i}>
									{`\n${line}`}
								</span>
							))
						: null}
				</div>
			);
		case "diff":
			return (
				<div className="diff">
					<DiffRows lines={body.lines} size="wide" />
					{body.hidden > 0 ? <div className="chat-tool__more">… {body.hidden} more lines</div> : null}
				</div>
			);
		case "output":
			return <div className="chat-term">{body.text || "(no output)"}</div>;
		case "plan":
			return (
				<div className="chat-plan-body note-prose chat-md">
					<NoteMarkdown source={body.plan} theme={theme} />
				</div>
			);
		case "generic":
			return (
				<div className="chat-term">
					<span className="chat-term__label">Input</span>
					{body.input}
					{body.output ? (
						<>
							<span className="chat-term__label">Result</span>
							{body.output}
						</>
					) : null}
				</div>
			);
	}
}

export function ToolCard({
	call,
	subagentTools,
	open,
	onToggle,
	theme,
}: {
	call: ToolCallItem;
	subagentTools: number;
	open: boolean | undefined;
	onToggle: (open: boolean) => void;
	theme: Theme;
}) {
	const view = describeTool(call, subagentTools);
	const isOpen = open ?? view.defaultOpen;
	const Icon = FAMILY_ICON[view.family];
	return (
		<div
			className={cn("chat-tool", view.compact && "chat-tool--compact", isOpen && "chat-tool--open")}
			data-tool={call.name}
		>
			<button aria-expanded={isOpen} className="chat-tool__head" onClick={() => onToggle(!isOpen)} type="button">
				<ChevronRight className="chat-chev" aria-hidden="true" />
				<Icon className="chat-icon" aria-hidden="true" />
				<span className="chat-tool__name">{view.title}</span>
				<span className="chat-tool__arg" title={view.arg}>
					{view.arg}
				</span>
				<Status view={view} />
			</button>
			{isOpen ? (
				<div className="chat-tool__body">
					<ToolBodyView body={view.body} theme={theme} />
				</div>
			) : null}
		</div>
	);
}

const SETTLED_ICON: Record<Settled["tone"], LucideIcon> = { ok: Check, no: X, muted: X };

export function ResolvedLine({ call }: { call: ToolCallItem }) {
	if (!call.permission || call.permission.status === "pending") return null;
	const settled = describeSettled(call, call.permission);
	const Icon = SETTLED_ICON[settled.tone];
	return (
		<div className={cn("chat-resolved", `chat-resolved--${settled.tone}`)} data-resolved={call.permission.status}>
			<Icon className="chat-icon" aria-hidden="true" />
			<span>
				{settled.text}
				{settled.code ? (
					<>
						{" "}
						<code>{settled.code}</code>
					</>
				) : null}
			</span>
		</div>
	);
}

function DisabledActions({ children }: { children: React.ReactNode }) {
	return (
		<SimpleTooltip label={READ_ONLY_HINT}>
			<span className="chat-ask__buttons" data-testid="chat-request-actions" tabIndex={0}>
				{children}
			</span>
		</SimpleTooltip>
	);
}

function PermissionCard({ request, theme }: { request: Pending; theme: Theme }) {
	const input = isObject(request.input) ? request.input : {};
	const preview = describeTool({
		kind: "tool_call",
		id: request.toolUseId,
		name: request.toolName,
		parentToolUseId: "",
		input: request.input,
	});
	const command = typeof input.command === "string" ? input.command : preview.arg;
	const { rules, mode } = describeUpdates(request.suggestions);
	return (
		<div className="chat-ask" data-pending="permission">
			<div className="chat-ask__bar">
				<Shield className="chat-icon" aria-hidden="true" />
				Permission needed · {request.toolName}
				<span className="chat-ask__key">waiting for an answer</span>
			</div>
			<div className="chat-ask__inner">
				{command ? <div className="chat-ask__cmd">{command}</div> : null}
				{typeof input.description === "string" && input.description !== "" ? (
					<div className="chat-ask__why">{input.description}</div>
				) : null}
				{preview.body.kind === "diff" ? (
					<div className="chat-ask__diff">
						<ToolBodyView body={preview.body} theme={theme} />
					</div>
				) : null}
				{preview.body.kind === "generic" ? <div className="chat-ask__cmd">{preview.body.input}</div> : null}
				<div className="chat-ask__actions">
					<DisabledActions>
						<Button disabled size="sm">
							Allow once
						</Button>
						{rules.length || mode ? (
							<Button disabled size="sm" variant="outline">
								Always allow
							</Button>
						) : null}
						<Button className="chat-danger" disabled size="sm" variant="outline">
							Deny
						</Button>
					</DisabledActions>
					{rules.length ? (
						<span className="chat-ask__rule">
							Always adds <code>{rules.join(", ")}</code>
						</span>
					) : mode ? (
						<span className="chat-ask__rule">
							Always switches to <code>{mode}</code> mode
						</span>
					) : null}
				</div>
			</div>
		</div>
	);
}

function QuestionCard({ request }: { request: Pending }) {
	const questions = parseQuestions(request.input);
	const [tab, setTab] = useState(0);
	const current = questions[Math.min(tab, questions.length - 1)];
	return (
		<div className="chat-ask" data-pending="question">
			<div className="chat-ask__bar">
				<MessageSquare className="chat-icon" aria-hidden="true" />
				Claude is asking · {questions.length === 1 ? "1 question" : `${questions.length} questions`}
				<span className="chat-ask__key">AskUserQuestion</span>
			</div>
			<div className="chat-ask__inner">
				{questions.length > 1 ? (
					<div className="chat-qtabs" role="tablist">
						{questions.map((q, i) => (
							<button
								aria-selected={i === tab}
								className="chat-qtab"
								key={`${i}:${q.header}`}
								onClick={() => setTab(i)}
								role="tab"
								type="button"
							>
								{q.header || `Question ${i + 1}`}
							</button>
						))}
					</div>
				) : null}
				{current ? (
					<>
						<div className="chat-question">{current.question}</div>
						<div className="chat-opts">
							{current.options.map((o) => (
								<div className={cn("chat-opt", current.multiSelect && "chat-opt--multi")} key={o.label}>
									<span aria-hidden="true" className="chat-opt__mark" />
									<span>
										<span className="chat-opt__label">{o.label}</span>
										<span className="chat-opt__desc">{o.description}</span>
									</span>
								</div>
							))}
						</div>
					</>
				) : null}
				<div className="chat-ask__actions">
					<input aria-label="Other answer" className="chat-other" disabled placeholder="Other: type your own answer" />
					<DisabledActions>
						<Button disabled size="sm">
							Submit answers
						</Button>
					</DisabledActions>
				</div>
			</div>
		</div>
	);
}

function PlanCard({ request, theme }: { request: Pending; theme: Theme }) {
	const { plan, path } = parsePlan(request.input);
	return (
		<div className="chat-ask chat-ask--plan" data-pending="plan">
			<div className="chat-ask__bar">
				<ListChecks className="chat-icon" aria-hidden="true" />
				Plan ready for review
				<span className="chat-ask__key">{path ? `ExitPlanMode · ${path}` : "ExitPlanMode"}</span>
			</div>
			<div className="chat-plan-body note-prose chat-md">
				<NoteMarkdown source={plan} theme={theme} />
			</div>
			<div className="chat-ask__foot chat-ask__actions">
				<DisabledActions>
					<Button disabled size="sm">
						Approve · auto-accept edits
					</Button>
					<Button disabled size="sm" variant="outline">
						Approve · ask before edits
					</Button>
					<Button disabled size="sm" variant="ghost">
						Keep planning…
					</Button>
				</DisabledActions>
				<span className="chat-ask__waiting">waiting for an answer</span>
			</div>
		</div>
	);
}

export function PendingRequestCard({ request, theme }: { request: Pending; theme: Theme }) {
	switch (request.kind) {
		case "question":
			return <QuestionCard request={request} />;
		case "plan":
			return <PlanCard request={request} theme={theme} />;
		case "permission":
			return <PermissionCard request={request} theme={theme} />;
	}
}
