import { ChevronLeft, ChevronRight, Maximize2, Minimize2, Square, Trash2 } from "lucide-react";
import { useCallback, useMemo, useState } from "react";
import { useChatActions } from "../hooks/useChatActions";
import type { ChatSessionState } from "../hooks/useChatSession";
import type { ChatApi } from "../lib/chat-api";
import { ChatFolder, type Transcript } from "../lib/chat-fold";
import { chatRows } from "../lib/chat-rows";
import { emptyOverlay } from "../lib/chat-stream";
import { subagentTranscript, type TaskEntry, taskList, taskTarget } from "../lib/chat-tasks";
import { TASK_STATE_LABEL } from "../lib/chat-tools";
import { cn } from "../lib/utils";
import { type Theme } from "../stores/ui-store";
import { useSessionTasksUi, useTasksUi } from "../stores/tasks-ui";
import { ReadOnlyRows } from "./ChatPane";
import { ChatTasksProvider } from "./ChatTasks";
import { TaskOutputProvider, TaskOutputView } from "./ChatTaskOutput";
import { Button } from "./ui/button";

const PROMPT_COLLAPSED_LINES = 6;
const PROMPT_COLLAPSED_CHARS = 360;

const longPrompt = (prompt: string) =>
	prompt.split("\n").length > PROMPT_COLLAPSED_LINES || prompt.length > PROMPT_COLLAPSED_CHARS;

type StopState = { phase: "stopping" } | { phase: "failed"; message: string };

/** What the stop buttons need: a send, and what became of each press. A press is over once its job has ended. */
function useStops(stop: (taskId: string) => Promise<{ ok: true } | { ok: false; error: { message: string } }>) {
	const [stops, setStops] = useState<Readonly<Record<string, StopState>>>({});
	const press = useCallback(
		(taskId: string) => {
			setStops((prev) => ({ ...prev, [taskId]: { phase: "stopping" } }));
			void stop(taskId).then((result) => {
				if (result.ok) return;
				setStops((prev) => ({ ...prev, [taskId]: { phase: "failed", message: result.error.message } }));
			});
		},
		[stop],
	);
	return { stops, press };
}

type StopProps = { entry: TaskEntry; state: StopState | undefined; onStop: (taskId: string) => void };

function StopControl({ entry, state, onStop }: StopProps) {
	const taskId = entry.task?.id;
	if (!entry.running || taskId === undefined) return null;
	if (state?.phase === "stopping") {
		return (
			<span className="tasks-panel__stopping" role="status">
				Stopping…
			</span>
		);
	}
	return (
		<button
			aria-label={`Stop ${entry.title}`}
			className="tasks-panel__stop"
			onClick={() => onStop(taskId)}
			title="Stop"
			type="button"
		>
			<Square aria-hidden="true" className="tasks-panel__stop-icon" />
		</button>
	);
}

function StopError({ state }: { state: StopState | undefined }) {
	if (state?.phase !== "failed") return null;
	return (
		<div className="tasks-panel__error" role="alert">
			{state.message}
		</div>
	);
}

function TaskRow({ entry, state, onOpen, onStop }: StopProps & { onOpen: (ref: string) => void }) {
	return (
		<li className="tasks-panel__item" data-running={entry.running ? "" : undefined}>
			<div className="tasks-panel__row">
				<button className="tasks-panel__open" onClick={() => onOpen(entry.ref)} type="button">
					<span className="tasks-panel__title">{entry.title}</span>
					{entry.running ? null : <span className="tasks-panel__state">{TASK_STATE_LABEL[entry.state]}</span>}
					<ChevronRight aria-hidden="true" className="tasks-panel__chev" />
				</button>
				<StopControl entry={entry} onStop={onStop} state={state} />
			</div>
			<StopError state={state} />
		</li>
	);
}

function ExpandButton({ expanded, onToggle }: { expanded: boolean; onToggle: () => void }) {
	const Icon = expanded ? Minimize2 : Maximize2;
	return (
		<Button
			aria-label={expanded ? "Restore the panel width" : "Widen the panel"}
			aria-pressed={expanded}
			onClick={onToggle}
			size="icon-sm"
			variant="ghost"
		>
			<Icon aria-hidden="true" className="tasks-panel__icon" />
		</Button>
	);
}

function PromptCard({ prompt }: { prompt: string }) {
	const [open, setOpen] = useState(false);
	if (prompt.trim() === "") return null;
	const folds = longPrompt(prompt);
	return (
		<div className="tasks-panel__prompt" data-open={open ? "" : undefined}>
			<div className={cn("tasks-panel__prompt-text", folds && !open && "tasks-panel__prompt-text--folded")}>
				{prompt}
			</div>
			{folds ? (
				<button className="tasks-panel__more" onClick={() => setOpen(!open)} type="button">
					{open ? "Show less" : "Show more"}
				</button>
			) : null}
		</div>
	);
}

function modelName(transcript: Transcript, id: string): string {
	return transcript.models.find((m) => m.resolvedModel === id || m.value === id)?.displayName || id;
}

function Detail({
	entry,
	transcript,
	root,
	theme,
	onOpenTask,
}: {
	entry: TaskEntry;
	transcript: Transcript;
	root: string;
	theme: Theme;
	onOpenTask: (ref: string) => void;
}) {
	const sub = useMemo(
		() => (entry.kind === "agent" ? subagentTranscript(transcript, entry.ref) : undefined),
		[entry.kind, entry.ref, transcript],
	);
	const rows = useMemo(() => (sub ? chatRows(sub, emptyOverlay) : []), [sub]);
	if (entry.kind === "bash") {
		return (
			<div className="tasks-panel__detail">
				{entry.command ? <pre className="chat-term tasks-panel__command">$ {entry.command}</pre> : null}
				{entry.task ? (
					<TaskOutputView running={entry.running} taskId={entry.task.id} />
				) : (
					<div className="chat-task-output__note">Output is not available</div>
				)}
			</div>
		);
	}
	if (!sub) return <div className="chat-task-output__note tasks-panel__detail">Nothing to show for this task.</div>;
	return (
		<div className="tasks-panel__detail">
			<PromptCard prompt={entry.prompt} />
			{sub.model ? <div className="tasks-panel__model">Model {modelName(transcript, sub.model)}</div> : null}
			<ChatTasksProvider onOpen={(ref) => ref !== null && onOpenTask(ref)}>
				<ReadOnlyRows rows={rows} root={root} theme={theme} />
			</ChatTasksProvider>
			{rows.length === 0 ? (
				<div className="chat-task-output__note">
					{entry.running ? "Nothing yet" : "No messages were logged for this task."}
				</div>
			) : null}
		</div>
	);
}

/**
 * The inspector's Tasks tab: the session's background jobs, newest first, and a read-only view of one. Both read
 * the transcript the chat already folded, so a job is counted here exactly as the status line and the Waiting on
 * row count it.
 */
export function TasksPanel({
	sessionId,
	chat,
	root,
	theme,
	chatApi,
}: {
	sessionId: string;
	chat: ChatSessionState | undefined;
	root: string;
	theme: Theme;
	chatApi?: ChatApi;
}) {
	const transcript = useMemo(() => chat?.transcript ?? new ChatFolder().transcript(), [chat?.transcript]);
	const actions = useChatActions(sessionId, chat?.connection ?? "connecting", chatApi);
	const { stops, press } = useStops(actions.stopTask);
	const ui = useSessionTasksUi(sessionId);
	const focus = useTasksUi((state) => state.focus);
	const setExpanded = useTasksUi((state) => state.setExpanded);
	const clear = useTasksUi((state) => state.clear);
	const list = useMemo(() => taskList(transcript), [transcript]);
	const finished = useMemo(() => list.finished.filter((e) => !ui.cleared.includes(e.ref)), [list.finished, ui.cleared]);
	const open = useCallback((ref: string | null) => focus(sessionId, ref), [focus, sessionId]);
	const toggleExpanded = () => setExpanded(sessionId, !ui.expanded);

	const target = ui.focus === null ? undefined : taskTarget(transcript, ui.focus);

	return (
		<TaskOutputProvider read={actions.taskOutput}>
			<div className="tasks-panel" data-testid="tasks-panel" role="tabpanel">
				{ui.focus === null ? (
					<>
						<div className="tasks-panel__head">
							<h2 className="tasks-panel__heading">Background tasks</h2>
							<Button
								aria-label="Clear finished tasks"
								disabled={finished.length === 0}
								onClick={() =>
									clear(
										sessionId,
										finished.map((e) => e.ref),
									)
								}
								size="icon-sm"
								variant="ghost"
							>
								<Trash2 aria-hidden="true" className="tasks-panel__icon" />
							</Button>
							<ExpandButton expanded={ui.expanded} onToggle={toggleExpanded} />
						</div>
						<div className="tasks-panel__body">
							{list.running.length === 0 && finished.length === 0 ? (
								<p className="inspector-empty">No background tasks in this session.</p>
							) : null}
							{list.running.length > 0 ? (
								<section aria-labelledby="tasks-running">
									<h3 className="tasks-panel__section" id="tasks-running">
										Running {list.running.length}
									</h3>
									<ul className="tasks-panel__list">
										{list.running.map((entry) => (
											<TaskRow
												entry={entry}
												key={entry.ref}
												onOpen={open}
												onStop={press}
												state={entry.task ? stops[entry.task.id] : undefined}
											/>
										))}
									</ul>
								</section>
							) : null}
							{finished.length > 0 ? (
								<section aria-labelledby="tasks-finished">
									<h3 className="tasks-panel__section" id="tasks-finished">
										Finished {finished.length}
									</h3>
									<ul className="tasks-panel__list">
										{finished.map((entry) => (
											<TaskRow entry={entry} key={entry.ref} onOpen={open} onStop={press} state={undefined} />
										))}
									</ul>
								</section>
							) : null}
						</div>
					</>
				) : (
					<>
						<div className="tasks-panel__head">
							<Button aria-label="Back to the task list" onClick={() => open(null)} size="icon-sm" variant="ghost">
								<ChevronLeft aria-hidden="true" className="tasks-panel__icon" />
							</Button>
							<h2 className="tasks-panel__heading" title={target?.title}>
								{target?.title ?? "Task not found"}
							</h2>
							{target?.running ? <span aria-label="Running" className="tasks-panel__live" role="status" /> : null}
							{target ? (
								<StopControl entry={target} onStop={press} state={target.task ? stops[target.task.id] : undefined} />
							) : null}
							<ExpandButton expanded={ui.expanded} onToggle={toggleExpanded} />
						</div>
						<div className="tasks-panel__body">
							{target ? (
								<>
									<StopError state={target.task ? stops[target.task.id] : undefined} />
									<Detail entry={target} onOpenTask={open} root={root} theme={theme} transcript={transcript} />
								</>
							) : (
								<p className="inspector-empty">This task is no longer in the session log.</p>
							)}
						</div>
					</>
				)}
			</div>
		</TaskOutputProvider>
	);
}
