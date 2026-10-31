import { useEffect, useMemo, useState } from "react";
import { type CodeView, highlightView } from "../lib/chat-code";
import type { CodeToken } from "../lib/note/highlight";
import { cn } from "../lib/utils";
import type { Theme } from "../stores/ui-store";

const SIGN = { add: "+", del: "−", context: "" } as const;

/**
 * A file's code under a header with its name. Plain text shows first and the shiki colours replace it
 * once the grammar lands, so opening a card never waits on a grammar chunk.
 */
export function CodeBox({
	view,
	mode,
	theme,
	named = true,
}: {
	view: CodeView;
	mode: "write" | "edit" | "read";
	theme: Theme;
	/** False where the file's name is already on screen, as in the Document tab's header. */
	named?: boolean;
}) {
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
			{named ? (
				<div className="chat-codebox__head">
					<span className="chat-codebox__title">{name}</span>
				</div>
			) : null}
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
