import { useState } from "react";
import { Bubble } from "../../../companion/Bubble";
import { PREVIEW_BUBBLES, previewRoster } from "../../../companion/preview";
import { PROCS_INK, PROP_COLOURS } from "../../../companion/palette";
import { Procs } from "../../../companion/Procs";
import { Button } from "../ui/button";

// The Settings gallery: what every state looks like, and what the bubble does as a
// claim ages.
//
// The overlay is the one surface a user cannot browse. It sits along the bottom of
// the screen showing whatever the sessions happen to be doing, so most of the
// fifteen states are never seen at once and none of them is captioned. This is
// where someone learns that a bed means idle and a `?` sign means it wants them.
//
// The Procs are drawn on a deliberately BANDED backdrop rather than an app surface,
// because that is the honest preview: they live on a wallpaper, and the whole point
// of the ink rim is that they survive any tone behind them. Showing them on a flat
// panel would flatter the art and hide exactly the failure mode the rim exists for.

/** Dark → light, the range a desktop wallpaper actually spans. */
const WALLPAPER_TONES = ["#12141c", "#3d4457", "#6f727b", "#b6b2a8", "#f2efe8"];

const PET_SIZE = 92;

export function CompanionPreview() {
	const [open, setOpen] = useState(false);

	if (!open) {
		return (
			<Button type="button" variant="outline" onClick={() => setOpen(true)}>
				Show me what they look like
			</Button>
		);
	}

	return (
		<div className="flex flex-col gap-4">
			<div className="flex items-center justify-between">
				<p className="text-[12px] leading-5 text-muted-foreground">
					Every state a session can be in, and the character each one wears. The strip behind them runs from a dark
					wallpaper to a light one, because that is what they have to stay readable against.
				</p>
				<Button type="button" variant="ghost" onClick={() => setOpen(false)}>
					Hide
				</Button>
			</div>

			<div
				className="overflow-hidden rounded-lg border border-border"
				style={{ background: `linear-gradient(100deg, ${WALLPAPER_TONES.join(", ")})` }}
			>
				<div className="flex flex-wrap gap-x-1 gap-y-4 p-3">
					{previewRoster().map((entry) => (
						<figure key={entry.status} data-preview-state={entry.status} className="w-[196px] shrink-0">
							{/* The bubble sits in its own row above the Proc rather than floating
							    over it: overlapping absolutely-positioned bubbles ran across the
							    neighbouring cells and over the row above. */}
							<div className="flex h-[46px] items-end justify-center px-2">
								{entry.bubble ? (
									<Bubble text={entry.bubble.text} tone={entry.bubble.tone} decay={entry.bubble.decay} />
								) : null}
							</div>
							<div className="flex h-[116px] items-end justify-center">
								<Procs cast={entry.cast} status={entry.status} facing="front" walking={false} size={PET_SIZE} />
							</div>
							{/* The caption is chrome on a WALLPAPER, so it gets the same treatment
							    the pets and the bubble get: its own self-contained fill plus ink,
							    not light text and a drop shadow. White captions were unreadable
							    over the bright end of the strip. */}
							<figcaption className="mt-1 flex flex-col items-center gap-0.5">
								<span
									className="rounded px-1.5 py-0.5 text-[11px] font-medium"
									style={{ background: PROCS_INK, color: PROP_COLOURS.paper }}
								>
									{entry.label}
								</span>
								<span
									className="rounded px-1 font-mono text-[10px]"
									style={{ background: PROCS_INK, color: PROP_COLOURS.quiet }}
								>
									{entry.status}
								</span>
							</figcaption>
						</figure>
					))}
				</div>
			</div>

			<div>
				<p className="text-[12px] leading-5 text-muted-foreground">
					A Proc only speaks when its session gives it something true to say, and what it says fades as it ages — so it
					can never still be claiming, ten minutes later, to be doing something it has finished.
				</p>
				<div
					className="mt-2 flex flex-wrap items-start gap-6 rounded-lg border border-border p-4"
					style={{ background: "#6f727b" }}
				>
					{PREVIEW_BUBBLES.map((sample) => (
						<div key={sample.caption} data-preview-decay={sample.decay} className="flex flex-col items-start gap-3">
							<Bubble text={sample.text} tone={sample.tone} decay={sample.decay} />
							<span
								className="rounded px-1.5 py-0.5 text-[11px]"
								style={{ background: PROCS_INK, color: PROP_COLOURS.paper }}
							>
								{sample.caption}
							</span>
						</div>
					))}
				</div>
			</div>
		</div>
	);
}
