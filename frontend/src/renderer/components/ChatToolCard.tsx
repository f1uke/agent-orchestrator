import {
	Bot,
	Check,
	ChevronRight,
	FilePlus,
	FileText,
	ListChecks,
	ListTodo,
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
import { useEffect, useMemo, useState } from "react";
import {
	type AnswerBody,
	allowOnce,
	alwaysAllow,
	answerQuestions,
	approvePlan,
	deny,
	keepPlanning,
	type PermissionMode,
	type QuestionChoice,
	questionAnswer,
	replyInChat,
} from "../lib/chat-answers";
import type { BackgroundTask, Pending, ToolCallItem } from "../lib/chat-fold";
import { TaskOutputView } from "./ChatTaskOutput";
import { isObject } from "../lib/chat-event";
import {
	type Question,
	type Settled,
	type ToolBody,
	type ToolView,
	describeSettled,
	describeSuggestions,
	describeTool,
	parsePlan,
	parseQuestions,
} from "../lib/chat-tools";
import { type CodeView, highlightView } from "../lib/chat-code";
import type { CodeToken } from "../lib/note/highlight";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";
import { ChatImages, READ_THUMB } from "./ChatImages";
import { CopyButton } from "./CopyButton";
import { NoteMarkdown } from "./NoteMarkdown";
import { RefImages } from "./RefImages";
import { Button } from "./ui/button";

const FAMILY_ICON: Record<ToolView["family"], LucideIcon> = {
	bash: SquareTerminal,
	edit: Pencil,
	write: FilePlus,
	read: FileText,
	search: Search,
	agent: Bot,
	todo: ListTodo,
	plan: ListChecks,
	question: MessageSquare,
	other: Wrench,
};

function Status({ view }: { view: ToolView }) {
	const { status } = view;
	switch (status.tone) {
		case "running":
			return (
				<span className="chat-tool__status">
					<Loader2 className="chat-icon animate-spin" aria-hidden="true" />
					{status.text ?? "running"}
				</span>
			);
		case "idle":
			return <span className="chat-tool__status">{status.text}</span>;
		case "diff":
			return (
				<span className="chat-tool__status">
					<span className="chat-tool__add">+{status.added}</span>
					{status.removed > 0 ? <span className="chat-tool__del">−{status.removed}</span> : null}
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

const SIGN = { add: "+", del: "−", context: "" } as const;

/**
 * A file's code under a header with its name. Plain text shows first and the shiki colours replace it
 * once the grammar lands, so opening a card never waits on a grammar chunk.
 */
function CodeBox({ view, mode, theme }: { view: CodeView; mode: "write" | "edit" | "read"; theme: Theme }) {
	const [tokens, setTokens] = useState<CodeToken[][] | undefined>(undefined);
	const source = useMemo(() => view.lines.map((l) => `${l.kind[0]}${l.text}`).join("\n"), [view.lines]);
	useEffect(() => {
		let live = true;
		setTokens(undefined);
		void highlightView(view, theme).then((result) => {
			if (live) setTokens(result);
		});
		return () => {
			live = false;
		};
		// `source` stands for the view: describeTool builds a new object on every render.
	}, [source, view.language, theme]);

	const name = view.path.split("/").filter(Boolean).at(-1) ?? view.path;
	return (
		<div className={cn("chat-codebox", mode === "write" && "chat-codebox--new")}>
			<div className="chat-codebox__head">
				<span className="chat-codebox__title">{name}</span>
			</div>
			<div className="chat-codebox__body">
				{view.lines.map((line, i) => (
					<div
						className={cn("chat-codebox__row", line.kind !== "context" && `chat-codebox__row--${line.kind}`)}
						key={i}
					>
						<span aria-hidden="true" className="chat-codebox__num">
							{line.num}
						</span>
						{mode === "edit" ? (
							<span aria-hidden="true" className="chat-codebox__sign">
								{SIGN[line.kind]}
							</span>
						) : null}
						<span className="chat-codebox__text">
							{tokens?.[i]?.length
								? tokens[i].map((token, t) => (
										<span key={t} style={{ color: token.color }}>
											{token.text}
										</span>
									))
								: line.text || " "}
						</span>
					</div>
				))}
			</div>
			{view.hidden > 0 ? <div className="chat-tool__more">… {view.hidden} more lines</div> : null}
		</div>
	);
}

function ToolBodyView({ body, theme }: { body: ToolBody; theme: Theme }) {
	switch (body.kind) {
		case "bash":
			return (
				<div className="chat-bash">
					<div className="chat-code">
						<span aria-hidden="true" className="chat-code__prompt">
							$
						</span>
						<span className="chat-code__text">{body.command}</span>
						<CopyButton className="chat-code__copy" value={body.command} what="command" />
					</div>
					{body.exitCode ? <div className="chat-tool__exit">Exit code {body.exitCode}</div> : null}
					{body.task ? (
						<TaskOutputView running={body.task.running} taskId={body.task.id} />
					) : body.output ? (
						<div className="chat-term">
							{body.output.split("\n").map((line, i) => (
								<span className={outputTone(line, body.failed)} key={i}>
									{i > 0 ? `\n${line}` : line}
								</span>
							))}
						</div>
					) : null}
				</div>
			);
		case "code":
			return <CodeBox mode={body.mode} theme={theme} view={body.view} />;
		case "output":
			return <div className="chat-term">{body.text || "(no output)"}</div>;
		case "image": {
			const file = body.path.split("/").pop() || "image";
			return (
				<div className="chat-tool__images">
					<ChatImages
						images={body.images}
						name={(i) => (body.images.length > 1 ? `${file} (${i + 1})` : file)}
						thumb={READ_THUMB}
					/>
				</div>
			);
		}
		case "plan":
			return (
				<div className="chat-plan-body">
					<div className="note-prose chat-md">
						<NoteMarkdown source={body.plan} theme={theme} />
					</div>
					<RefImages source={body.plan} />
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
	task,
	root,
	open,
	onToggle,
	theme,
}: {
	call: ToolCallItem;
	subagentTools: number;
	task?: BackgroundTask;
	root: string;
	open: boolean | undefined;
	onToggle: (open: boolean) => void;
	theme: Theme;
}) {
	const view = describeTool(call, { root, subagentTools, task });
	const isOpen = open ?? false;
	const Icon = FAMILY_ICON[view.family];
	return (
		<div
			className={cn(
				"chat-tool",
				view.compact && "chat-tool--compact",
				view.body.kind === "code" && "chat-tool--file",
				isOpen && "chat-tool--open",
			)}
			data-tool={call.name}
		>
			<button aria-expanded={isOpen} className="chat-tool__head" onClick={() => onToggle(!isOpen)} type="button">
				<ChevronRight className="chat-chev" aria-hidden="true" />
				<Icon className="chat-icon" aria-hidden="true" />
				<span className="chat-tool__name">{view.title}</span>
				<span className="chat-tool__arg" title={view.path || view.arg}>
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

export type RequestState = { phase: "sending" } | { phase: "failed"; message: string };

export type AnswerHandler = (body: AnswerBody, then?: PermissionMode) => void;

type CardProps = {
	request: Pending;
	theme: Theme;
	state: RequestState | undefined;
	disabled: boolean;
	onAnswer: AnswerHandler;
};

function Kbd({ children }: { children: React.ReactNode }) {
	return (
		<span aria-hidden="true" className="chat-kbd">
			{children}
		</span>
	);
}

function CardStatus({ state, idle }: { state: RequestState | undefined; idle: string }) {
	if (state?.phase === "sending") {
		return (
			<span className="chat-ask__key" role="status">
				<Loader2 className="chat-icon animate-spin" aria-hidden="true" />
				Sending…
			</span>
		);
	}
	return <span className="chat-ask__key">{idle}</span>;
}

function CardError({ state }: { state: RequestState | undefined }) {
	if (state?.phase !== "failed") return null;
	return (
		<div className="chat-ask__error" role="alert">
			<X className="chat-icon" aria-hidden="true" />
			{state.message}
		</div>
	);
}

function PermissionCard({ request, theme, state, disabled, onAnswer }: CardProps) {
	const [reason, setReason] = useState("");
	const input = isObject(request.input) ? request.input : {};
	const preview = describeTool({
		kind: "tool_call",
		id: request.toolUseId,
		name: request.toolName,
		parentToolUseId: "",
		input: request.input,
		startedTs: "",
	});
	const command = typeof input.command === "string" ? input.command : preview.arg;
	const suggestions = describeSuggestions(request.suggestions);
	const locked = disabled || state?.phase === "sending";

	const onKeyDown = (e: React.KeyboardEvent<HTMLDivElement>) => {
		if (locked) return;
		if (e.key === "Escape") {
			e.preventDefault();
			onAnswer(deny(request, reason));
		} else if (e.key === "Enter" && e.target === e.currentTarget && !request.defaultToNo) {
			e.preventDefault();
			onAnswer((e.metaKey || e.ctrlKey) && suggestions.length ? alwaysAllow(request) : allowOnce(request));
		}
	};

	return (
		<div
			aria-label={`Permission needed: ${request.toolName}`}
			className="chat-ask"
			data-pending="permission"
			onKeyDown={onKeyDown}
			role="group"
			tabIndex={0}
		>
			<div className="chat-ask__bar">
				<Shield className="chat-icon" aria-hidden="true" />
				Permission needed · {request.toolName}
				<CardStatus idle="waiting for an answer" state={state} />
			</div>
			<div className="chat-ask__inner">
				{command ? <div className="chat-ask__cmd">{command}</div> : null}
				{typeof input.description === "string" && input.description !== "" ? (
					<div className="chat-ask__why">{input.description}</div>
				) : null}
				{preview.body.kind === "code" ? (
					<div className="chat-ask__diff">
						<ToolBodyView body={preview.body} theme={theme} />
					</div>
				) : null}
				{preview.body.kind === "generic" ? <div className="chat-ask__cmd">{preview.body.input}</div> : null}
				<div className="chat-ask__actions">
					<Button disabled={locked} onClick={() => onAnswer(allowOnce(request))} size="sm">
						Allow once
						{request.defaultToNo ? null : <Kbd>↵</Kbd>}
					</Button>
					{suggestions.length ? (
						<Button disabled={locked} onClick={() => onAnswer(alwaysAllow(request))} size="sm" variant="outline">
							Always allow
							{request.defaultToNo ? null : <Kbd>⌘↵</Kbd>}
						</Button>
					) : null}
					<Button
						className="chat-danger"
						disabled={locked}
						onClick={() => onAnswer(deny(request, reason))}
						size="sm"
						variant="outline"
					>
						Deny
						<Kbd>esc</Kbd>
					</Button>
					{suggestions.length ? (
						<span className="chat-ask__rule" data-testid="chat-always-rule">
							Always{" "}
							{suggestions.map((s, i) => (
								<span key={i}>
									{i > 0 ? "; " : null}
									{s.verb} <code>{s.code}</code>
									{s.after}
									{s.file ? (
										<>
											{" "}
											to <code>{s.file}</code>
										</>
									) : null}
								</span>
							))}
						</span>
					) : null}
				</div>
				<input
					aria-label="Reason for denying"
					className="chat-other"
					disabled={locked}
					onChange={(e) => setReason(e.target.value)}
					onKeyDown={(e) => {
						if (e.key !== "Enter" || reason.trim() === "" || locked) return;
						e.preventDefault();
						onAnswer(deny(request, reason));
					}}
					placeholder="Reason, if you deny (optional). Claude reads it."
					value={reason}
				/>
				<CardError state={state} />
			</div>
		</div>
	);
}

function OptionButton({
	option,
	multi,
	checked,
	disabled,
	onPick,
	onFocusPreview,
}: {
	option: Question["options"][number];
	multi: boolean;
	checked: boolean;
	disabled: boolean;
	onPick: () => void;
	onFocusPreview: () => void;
}) {
	return (
		<button
			aria-checked={checked}
			className={cn("chat-opt", multi && "chat-opt--multi", checked && "chat-opt--on")}
			disabled={disabled}
			onClick={onPick}
			onFocus={onFocusPreview}
			onMouseEnter={onFocusPreview}
			role={multi ? "checkbox" : "radio"}
			type="button"
		>
			<span aria-hidden="true" className="chat-opt__mark" />
			<span>
				<span className="chat-opt__label">{option.label}</span>
				<span className="chat-opt__desc">{option.description}</span>
			</span>
		</button>
	);
}

const questionText = (q: Question) =>
	[q.question, ...q.options.flatMap((o) => [o.label, o.description, o.preview])].join("\n");

const EMPTY_CHOICE: QuestionChoice = { picked: [], other: "" };

function QuestionCard({ request, state, disabled, onAnswer }: CardProps) {
	const questions = parseQuestions(request.input);
	const [tab, setTab] = useState(0);
	const [choices, setChoices] = useState<QuestionChoice[]>(() => questions.map(() => EMPTY_CHOICE));
	const [peek, setPeek] = useState<string | undefined>(undefined);
	const index = Math.min(tab, questions.length - 1);
	const current = questions[index];
	const choice = choices[index] ?? EMPTY_CHOICE;
	const locked = disabled || state?.phase === "sending";
	const complete = questions.length > 0 && questions.every((q, i) => questionAnswer(q, choices[i]) !== "");

	const update = (next: QuestionChoice) => setChoices((prev) => prev.map((c, i) => (i === index ? next : c)));
	const pick = (label: string) => {
		if (!current.multiSelect) return update({ picked: [label], other: "" });
		const picked = choice.picked.includes(label)
			? choice.picked.filter((l) => l !== label)
			: current.options.map((o) => o.label).filter((l) => l === label || choice.picked.includes(l));
		update({ ...choice, picked });
	};
	const setOther = (other: string) => update(current.multiSelect ? { ...choice, other } : { picked: [], other });
	const showTab = (i: number) => {
		setTab(i);
		setPeek(undefined);
	};

	const withPreview = current?.options.filter((o) => o.preview !== "") ?? [];
	const shown =
		current?.options.find((o) => o.label === peek && o.preview !== "") ??
		current?.options.find((o) => o.label === choice.picked[0] && o.preview !== "") ??
		withPreview[0];

	return (
		<div aria-label="Claude is asking" className="chat-ask" data-pending="question" role="group">
			<div className="chat-ask__bar">
				<MessageSquare className="chat-icon" aria-hidden="true" />
				Claude is asking · {questions.length === 1 ? "1 question" : `${questions.length} questions`}
				<CardStatus idle="AskUserQuestion" state={state} />
			</div>
			<div className="chat-ask__inner">
				{questions.length > 1 ? (
					<div className="chat-qtabs" role="tablist">
						{questions.map((q, i) => (
							<button
								aria-selected={i === index}
								className={cn("chat-qtab", questionAnswer(q, choices[i]) !== "" && "chat-qtab--done")}
								key={`${i}:${q.header}`}
								onClick={() => showTab(i)}
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
						<div className="chat-question">
							{current.question}
							{current.multiSelect ? <span className="chat-question__hint"> (choose any)</span> : null}
						</div>
						<div className={cn("chat-qwrap", shown && "chat-qwrap--preview")}>
							<div
								aria-label={current.question}
								className="chat-opts"
								role={current.multiSelect ? "group" : "radiogroup"}
							>
								{current.options.map((o) => (
									<OptionButton
										checked={choice.picked.includes(o.label)}
										disabled={locked}
										key={o.label}
										multi={current.multiSelect}
										onFocusPreview={() => setPeek(o.label)}
										onPick={() => pick(o.label)}
										option={o}
									/>
								))}
							</div>
							{shown ? (
								<div className="chat-preview" data-testid="chat-option-preview">
									<span className="chat-preview__label">Preview · {shown.label}</span>
									{shown.preview}
								</div>
							) : null}
						</div>
						<RefImages source={questionText(current)} />
					</>
				) : null}
				<div className="chat-ask__actions">
					<input
						aria-label="Other answer"
						className="chat-other"
						disabled={locked}
						onChange={(e) => setOther(e.target.value)}
						placeholder="Other: type your own answer"
						value={choice.other}
					/>
					<Button
						disabled={locked || !complete}
						onClick={() => onAnswer(answerQuestions(request, questions, choices))}
						size="sm"
						title={complete ? undefined : "Answer every question first"}
					>
						Submit answers
					</Button>
					<Button disabled={locked} onClick={() => onAnswer(replyInChat(request))} size="sm" variant="ghost">
						Reply in chat instead
					</Button>
				</div>
				<CardError state={state} />
			</div>
		</div>
	);
}

function PlanCard({ request, theme, state, disabled, onAnswer }: CardProps) {
	const { plan, path } = parsePlan(request.input);
	const [feedback, setFeedback] = useState<string | undefined>(undefined);
	const locked = disabled || state?.phase === "sending";
	const approve = (mode: "acceptEdits" | "default") => {
		const { answer, mode: then } = approvePlan(request, mode);
		onAnswer(answer, then);
	};
	return (
		<div aria-label="Plan ready for review" className="chat-ask chat-ask--plan" data-pending="plan" role="group">
			<div className="chat-ask__bar">
				<ListChecks className="chat-icon" aria-hidden="true" />
				Plan ready for review
				<span className="chat-ask__key">{path ? `ExitPlanMode · ${path}` : "ExitPlanMode"}</span>
			</div>
			<div className="chat-plan-body">
				<div className="note-prose chat-md">
					<NoteMarkdown source={plan} theme={theme} />
				</div>
				<RefImages source={plan} />
			</div>
			<div className="chat-ask__foot">
				<div className="chat-ask__actions">
					<Button disabled={locked} onClick={() => approve("acceptEdits")} size="sm">
						Approve · auto-accept edits
					</Button>
					<Button disabled={locked} onClick={() => approve("default")} size="sm" variant="outline">
						Approve · ask before edits
					</Button>
					<Button
						aria-expanded={feedback !== undefined}
						disabled={locked}
						onClick={() => setFeedback((f) => (f === undefined ? "" : undefined))}
						size="sm"
						variant="ghost"
					>
						Keep planning…
					</Button>
					<CardStatus idle="waiting for an answer" state={state} />
				</div>
				{feedback !== undefined ? (
					<div className="chat-plan-feedback">
						<textarea
							aria-label="Plan feedback"
							autoFocus
							className="chat-plan-feedback__input"
							disabled={locked}
							onChange={(e) => setFeedback(e.target.value)}
							placeholder="What should change? Claude revises the plan and asks again."
							value={feedback}
						/>
						<Button
							disabled={locked || feedback.trim() === ""}
							onClick={() => onAnswer(keepPlanning(request, feedback))}
							size="sm"
							variant="outline"
						>
							Send feedback
						</Button>
					</div>
				) : null}
				<CardError state={state} />
			</div>
		</div>
	);
}

export function PendingRequestCard(props: CardProps) {
	switch (props.request.kind) {
		case "question":
			return <QuestionCard {...props} />;
		case "plan":
			return <PlanCard {...props} />;
		case "permission":
			return <PermissionCard {...props} />;
	}
}
