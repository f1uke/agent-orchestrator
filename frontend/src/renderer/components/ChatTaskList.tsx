import { Check, Circle } from "lucide-react";
import type { Todo } from "../lib/chat-inspect";

function StatusIcon({ status }: { status: Todo["status"] }) {
	if (status === "completed") return <Check aria-hidden="true" className="chat-tasks__icon chat-tasks__icon--done" />;
	if (status === "in_progress") {
		return (
			<svg aria-hidden="true" className="chat-tasks__icon chat-tasks__icon--active" viewBox="0 0 16 16">
				<circle cx="8" cy="8" fill="none" r="6" stroke="currentColor" strokeWidth="1.5" />
				<path d="M8 2a6 6 0 0 0 0 12z" fill="currentColor" />
			</svg>
		);
	}
	return <Circle aria-hidden="true" className="chat-tasks__icon" />;
}

export function ChatTaskList({ todos }: { todos: readonly Todo[] }) {
	const done = todos.filter((todo) => todo.status === "completed").length;
	return (
		<div className="chat-tasks" data-tasks>
			<div className="chat-tasks__head">
				Tasks · {done} of {todos.length}
			</div>
			<ul aria-label="Tasks" className="chat-tasks__list" role="list">
				{todos.map((todo, i) => (
					<li className="chat-tasks__item" data-status={todo.status} key={`${i}:${todo.content}`} role="listitem">
						<StatusIcon status={todo.status} />
						<span>{todo.status === "in_progress" && todo.activeForm !== "" ? todo.activeForm : todo.content}</span>
					</li>
				))}
			</ul>
		</div>
	);
}
