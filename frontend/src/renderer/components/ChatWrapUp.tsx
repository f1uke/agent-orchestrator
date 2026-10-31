import type { WrapUp } from "../lib/chat-wrapup";
import { cn } from "../lib/utils";

export function ChatWrapUp({
	wrap,
	live,
	onPrompt,
}: {
	wrap: WrapUp;
	live: boolean;
	onPrompt: (text: string) => void;
}) {
	return (
		<div className="chat-wrap" data-wrapup>
			<p className="chat-wrap__recap">{wrap.recap}</p>
			{wrap.ask ? <p className="chat-wrap__ask">{wrap.ask}</p> : null}
			{wrap.replies.length > 0 ? (
				<div className="chat-wrap__replies">
					{wrap.replies.map((reply, i) => (
						<button
							aria-disabled={live ? undefined : true}
							className={cn("chat-wrap__reply", i === 0 && "chat-wrap__reply--primary")}
							key={reply}
							onClick={live ? () => onPrompt(reply) : undefined}
							type="button"
						>
							{reply}
						</button>
					))}
				</div>
			) : null}
		</div>
	);
}
