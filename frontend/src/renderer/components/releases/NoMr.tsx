import { CircleSlash } from "lucide-react";
import { useMarkNoMr } from "../../hooks/useReleaseBoard";
import { canMarkNoMr, noMrOfCard } from "../../lib/releases/noMr";
import { markedText, noMrOf } from "../../lib/releases/sources";
import type { Board } from "../../lib/releases/types";
import { SourceTag } from "./Sources";

/** Same verb everywhere: the card row, the card sheet and the G2 alert row. */
export function NoMrButton({ board, k, variant }: { board: Board; k: string; variant: "row" | "link" }) {
	const mutate = useMarkNoMr();
	const marked = noMrOfCard(board, k) !== null;
	if (!canMarkNoMr(board, k)) return null;
	const label = marked ? `Undo No MR needed on ${k}` : `Mark ${k} No MR needed`;
	const toggle = () => mutate.mutate({ key: k, on: !marked });
	if (variant === "link")
		return (
			<button
				type="button"
				data-testid="no-mr-toggle"
				data-on={marked || undefined}
				aria-label={label}
				onClick={(e) => {
					e.stopPropagation();
					toggle();
				}}
			>
				{marked ? "Undo No MR needed" : "No MR needed"}
			</button>
		);
	return (
		<button
			type="button"
			className="nomrb"
			data-testid="no-mr-toggle"
			data-on={marked || undefined}
			aria-label={label}
			title={marked ? "Undo No MR needed" : "No MR needed: this card ships without code"}
			onClick={(e) => {
				e.stopPropagation();
				toggle();
			}}
			onKeyDown={(e) => e.stopPropagation()}
		>
			<CircleSlash aria-hidden="true" />
		</button>
	);
}

export function NoMrLine({ board, k }: { board: Board; k: string }) {
	const mark = noMrOfCard(board, k);
	if (!mark && !canMarkNoMr(board, k)) return null;
	return (
		<>
			<span>Code</span>
			<span className="nomrline" data-testid="card-no-mr">
				{mark ? (
					<>
						<span className="c" data-st="passive">
							No MR needed
						</span>
						<span className="passive">{markedText(mark)}</span>
						<SourceTag p={noMrOf(k, mark)} wide />
					</>
				) : (
					<span className="passive">no MR in any repo</span>
				)}
				<NoMrButton board={board} k={k} variant="link" />
			</span>
		</>
	);
}
