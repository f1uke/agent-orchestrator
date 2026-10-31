import { ChevronDown, ListTodo } from "lucide-react";
import { useState } from "react";
import type { Todo, TodoStatus } from "../lib/chat-inspect";
import { cn } from "../lib/utils";

const COUNT_LABEL: Array<[TodoStatus, string]> = [
	["completed", "completed"],
	["in_progress", "in progress"],
	["pending", "pending"],
];

export function ChatTodoBar({ todos }: { todos: readonly Todo[] | undefined }) {
	const [open, setOpen] = useState(false);
	if (!todos?.length) return null;
	const counts = COUNT_LABEL.flatMap(([status, label]) => {
		const n = todos.filter((t) => t.status === status).length;
		return n > 0 ? [`${n} ${label}`] : [];
	});
	return (
		<div className="chat-todos" data-open={open || undefined}>
			<button aria-expanded={open} className="chat-todos__bar" onClick={() => setOpen(!open)} type="button">
				<ListTodo aria-hidden="true" className="chat-icon" />
				<span className="chat-todos__title">To-dos</span>
				<span className="chat-todos__counts">{counts.join(" · ")}</span>
				<ChevronDown aria-hidden="true" className={cn("chat-todos__chev", open && "chat-todos__chev--open")} />
			</button>
			{open ? (
				<ul aria-label="To-dos" className="chat-todos__list">
					{todos.map((t, i) => (
						<li
							className="chat-todos__item"
							data-status={t.status}
							key={`${i}:${t.content}`}
							title={t.status === "in_progress" ? t.activeForm : undefined}
						>
							<span aria-hidden="true" className="chat-todos__mark" />
							{t.content}
						</li>
					))}
				</ul>
			) : null}
		</div>
	);
}
