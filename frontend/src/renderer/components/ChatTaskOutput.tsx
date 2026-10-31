import { createContext, type ReactNode, useContext, useEffect, useLayoutEffect, useRef, useState } from "react";
import type { TaskOutput } from "../lib/chat-api";

type ReadOutput = (taskId: string) => Promise<TaskOutput | null>;

const ReadContext = createContext<ReadOutput>(async () => null);

export function TaskOutputProvider({ read, children }: { read: ReadOutput; children: ReactNode }) {
	return <ReadContext.Provider value={read}>{children}</ReadContext.Provider>;
}

const POLL_MS = 2000;
const TAIL_BYTES = 64 * 1024;
const FOLLOW_PX = 8;

type Shown = { phase: "loading" } | { phase: "missing" } | { phase: "shown"; output: TaskOutput };

const kb = (bytes: number) => `${Math.max(1, Math.round(bytes / 1024))} KB`;

export function TaskOutputView({ taskId, running }: { taskId: string; running: boolean }) {
	const read = useContext(ReadContext);
	const [shown, setShown] = useState<Shown>({ phase: "loading" });
	const term = useRef<HTMLDivElement>(null);
	const follow = useRef(true);

	useEffect(() => {
		let live = true;
		const load = () =>
			void read(taskId).then((output) => {
				if (live) setShown(output ? { phase: "shown", output } : { phase: "missing" });
			});
		load();
		const timer = running ? setInterval(load, POLL_MS) : undefined;
		return () => {
			live = false;
			clearInterval(timer);
		};
	}, [read, taskId, running]);

	const text = shown.phase === "shown" ? shown.output.text : "";
	useLayoutEffect(() => {
		const el = term.current;
		if (el && follow.current) el.scrollTop = el.scrollHeight;
	}, [text]);

	if (shown.phase === "loading") return <div className="chat-task-output__note">Loading output…</div>;
	if (shown.phase === "missing") return <div className="chat-task-output__note">Output is not available</div>;
	if (shown.output.text === "") return <div className="chat-task-output__note">No output yet</div>;
	return (
		<>
			{shown.output.truncated ? (
				<div className="chat-task-output__note">
					Showing the last {kb(TAIL_BYTES)} of {kb(shown.output.size)}
				</div>
			) : null}
			<div
				className="chat-term"
				data-task-output={taskId}
				onScroll={(e) => {
					const el = e.currentTarget;
					follow.current = el.scrollHeight - el.scrollTop - el.clientHeight <= FOLLOW_PX;
				}}
				ref={term}
			>
				{shown.output.text}
			</div>
		</>
	);
}
