import { useEffect, useMemo, useState } from "react";
import { APPEARANCE_AXES, castFromLook, type AppearanceAxis, type AxisId, type Look } from "../../../companion/cast";
import { castFor, isAxisChosen, resolveLook } from "../../../companion/look-store";
import {
	clearStoredLookChoice,
	pruneStoredLooks,
	storeLookChoice,
	useLookOverrides,
} from "../../../companion/look-store-live";
import { PROCS_INK } from "../../../companion/palette";
import { Procs } from "../../../companion/Procs";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";

// The Pet library: pick the colour and the hat one session wears.
//
// It overrides a DEFAULT, never fills a blank. Every session already has a look the
// moment it exists - a stable hash of its ref, one dimension per axis - and that is
// what makes a Proc recognisable across restarts. This is the escape hatch for when
// the hash puts two teal Procs on one desk, not a step anybody has to take.
//
// Nothing here names "colour" or "hat". It walks `APPEARANCE_AXES`, so the character
// TYPES the human wants next arrive as a row in that registry rather than as another
// hand-written section in this file.

/**
 * The plainest scene there is: no ground prop, no held prop, nothing emitted - just
 * the figure and its cord.
 *
 * Which is exactly right for a look picker, because the figure and the cord are the
 * part this screen CANNOT change. A Proc is a running process with a power lead;
 * colours and hats are variety on that. Drawing it holding a laptop would put a
 * status into a picture that is not about status.
 */
const PREVIEW_STATUS = "unknown";

/** Dark to light, the range a desktop wallpaper actually spans. Mirrors CompanionPreview. */
const WALLPAPER_TONES = ["#12141c", "#3d4457", "#6f727b", "#b6b2a8", "#f2efe8"];

const HERO_SIZE = 124;
const SWATCH_SIZE = 50;
const THUMB_SIZE = 32;

type LibrarySession = { id: string; title: string };
type LibraryGroup = { name: string; sessions: LibrarySession[] };

export function PetLibrary() {
	const query = useWorkspaceQuery();
	const looks = useLookOverrides();
	const request = useUiStore((state) => state.petLibraryRequest);
	const clearRequest = useUiStore((state) => state.requestPetLibrary);
	const [picked, setPicked] = useState<string | null>(null);

	// Terminated sessions have no Proc on the band, so there is nothing to dress.
	const groups = useMemo<LibraryGroup[]>(
		() =>
			(query.data ?? [])
				.map((project) => ({
					name: project.name,
					sessions: project.sessions
						.filter((session) => !session.isTerminated)
						.map((session) => ({ id: session.id, title: session.title })),
				}))
				.filter((group) => group.sessions.length > 0),
		[query.data],
	);
	const all = useMemo(() => groups.flatMap((group) => group.sessions), [groups]);
	const selected = all.find((session) => session.id === picked) ?? all[0] ?? null;

	// Forget the sessions that are gone.
	//
	// ⚠ Here, and NOT in the overlay. This is the authoritative list - every session
	// of every project, terminated ones included, because a terminated session still
	// exists. The overlay shows at most MAX_PETS, so pruning against what IT can see
	// would delete the saved look of a session that is merely off the band.
	//
	// Guarded on `isSuccess`: an in-flight or failed fetch is not evidence that
	// anybody's session is gone.
	const projects = query.data;
	useEffect(() => {
		if (!query.isSuccess || !projects) return;
		pruneStoredLooks(projects.flatMap((project) => project.sessions.map((session) => session.id)));
	}, [query.isSuccess, projects]);

	// A right-click on a Proc asked for this session. Honoured once and then
	// forgotten, or the next visit to Settings would jump somewhere unasked.
	useEffect(() => {
		if (!request) return;
		if (all.some((session) => session.id === request)) setPicked(request);
		clearRequest(null);
	}, [request, all, clearRequest]);

	if (!selected) {
		return (
			<p className="text-[12px] leading-5 text-muted-foreground">
				No sessions to dress yet. Every session is given a character the moment it starts, and this is where you can
				change the one it got.
			</p>
		);
	}

	const choose = (axisId: AxisId, optionId: string) => {
		storeLookChoice(selected.id, axisId, optionId);
		aoBridge.companion.looksChanged();
	};
	const reset = (axisId: AxisId) => {
		clearStoredLookChoice(selected.id, axisId);
		aoBridge.companion.looksChanged();
	};

	const look = resolveLook(selected.id, looks);
	const cast = castFromLook(look);

	return (
		<div className="flex flex-col gap-3">
			<p className="text-[12px] leading-5 text-muted-foreground">
				Every session is given a colour and a hat the moment it starts, so you never have to come here. Change one when
				two of them come out too alike to tell apart.
			</p>

			<div className="grid grid-cols-[196px_minmax(0,1fr)] gap-3">
				<nav aria-label="Sessions" className="flex max-h-[380px] flex-col gap-2 overflow-y-auto pr-1">
					{groups.map((group) => (
						<div key={group.name} className="flex flex-col gap-0.5">
							{/* muted, not `passive`: the project a session belongs to is what tells two
							    same-named workers apart, and `passive` measures 2.8:1 on the light
							    theme, which is under the 4.5 an 11px label needs. */}
							<p className="truncate px-1.5 text-[11px] font-medium text-muted-foreground">{group.name}</p>
							{group.sessions.map((session) => (
								<SessionRow
									key={session.id}
									session={session}
									active={session.id === selected.id}
									onSelect={() => setPicked(session.id)}
								/>
							))}
						</div>
					))}
				</nav>

				<div className="flex min-w-0 flex-col gap-3">
					{/* On a WALLPAPER strip, not an app surface: the pets live on a desktop,
					    and the ink rim exists so they survive any tone behind them. A flat
					    panel would flatter the art and hide the failure mode it guards. */}
					<div
						className="flex items-end justify-center overflow-hidden rounded-lg border border-border pt-2"
						style={{ background: `linear-gradient(100deg, ${WALLPAPER_TONES.join(", ")})` }}
						aria-hidden="true"
					>
						<Procs cast={cast} status={PREVIEW_STATUS} facing="front" walking={false} size={HERO_SIZE} />
					</div>
					{/* The caption is the accessible name for the picture above it, which is
					    why the picture itself is hidden: "Teal bucket hat, unknown" is what
					    the sprite would announce, and "unknown" is a session status this
					    screen is not about. */}
					<p className="text-center text-[12px] text-muted-foreground" data-pet-preview={cast.id}>
						<span className="text-foreground">{selected.title}</span> wears the {cast.name.toLowerCase()}
					</p>

					{APPEARANCE_AXES.map((axis) => (
						<AxisPicker
							key={axis.id}
							axis={axis}
							look={look}
							chosen={isAxisChosen(looks, selected.id, axis.id)}
							onChoose={(optionId) => choose(axis.id, optionId)}
							onReset={() => reset(axis.id)}
						/>
					))}
				</div>
			</div>
		</div>
	);
}

function SessionRow({ session, active, onSelect }: { session: LibrarySession; active: boolean; onSelect: () => void }) {
	const looks = useLookOverrides();
	return (
		<button
			type="button"
			aria-current={active}
			onClick={onSelect}
			className={cn(
				"relative flex items-center gap-1 rounded-md py-0.5 pl-2 pr-1.5 text-left text-[12.5px] transition-colors",
				active
					? "bg-secondary text-foreground before:absolute before:inset-y-1.5 before:left-0 before:w-0.5 before:rounded-full before:bg-accent before:content-['']"
					: "text-muted-foreground hover:bg-interactive-hover hover:text-foreground",
			)}
		>
			<span className="shrink-0" aria-hidden="true">
				<Procs
					cast={castFor(session.id, looks)}
					status={PREVIEW_STATUS}
					facing="front"
					walking={false}
					size={THUMB_SIZE}
				/>
			</span>
			<span className="min-w-0 flex-1 truncate">{session.title}</span>
		</button>
	);
}

/**
 * One axis' worth of choices, each drawn as the Proc it would actually produce.
 *
 * A colour tile wears the session's CURRENT hat and a hat tile its current colour,
 * so every tile is the real answer to "what would I get" rather than an abstract
 * chip. That is the whole reason to have a gallery instead of a list of names.
 */
function AxisPicker({
	axis,
	look,
	chosen,
	onChoose,
	onReset,
}: {
	axis: AppearanceAxis;
	look: Look;
	chosen: boolean;
	onChoose: (optionId: string) => void;
	onReset: () => void;
}) {
	return (
		<section role="group" aria-label={axis.name} className="flex flex-col gap-1.5">
			<div className="flex items-baseline justify-between gap-2">
				<h4 className="text-[12.5px] font-medium text-foreground">{axis.name}</h4>
				{chosen ? (
					<button
						type="button"
						onClick={onReset}
						className="shrink-0 rounded px-1 text-[11px] text-muted-foreground underline-offset-2 hover:text-foreground hover:underline"
					>
						Back to the default
					</button>
				) : (
					<span className="shrink-0 text-[11px] text-muted-foreground">The one it was given</span>
				)}
			</div>
			<p className="text-[11.5px] leading-4 text-muted-foreground">{axis.hint}</p>
			{/* auto-fill, so more colours or more hats simply wrap rather than
			    overflowing a column count written down here. */}
			<div className="grid gap-1.5 [grid-template-columns:repeat(auto-fill,minmax(66px,1fr))]">
				{axis.options.map((option) => {
					const active = look[axis.id] === option.id;
					return (
						<button
							key={option.id}
							type="button"
							aria-pressed={active}
							aria-label={`${option.name}${active ? " (in use)" : ""}`}
							onClick={() => onChoose(option.id)}
							className={cn(
								"flex flex-col items-center gap-0.5 rounded-md border py-1.5 transition-colors",
								active
									? "border-accent bg-secondary text-foreground"
									: "border-border text-muted-foreground hover:border-passive hover:bg-interactive-hover",
							)}
						>
							{/* The tile gets its own dark plate rather than the app surface. These
							    are colours chosen to survive a WALLPAPER, and on a light theme's
							    near-white card the pale ones wash out completely. */}
							<span
								className="flex items-end justify-center rounded px-1 pb-0.5 pt-1"
								style={{ background: PROCS_INK }}
								aria-hidden="true"
							>
								<Procs
									cast={castFromLook({ ...look, [axis.id]: option.id })}
									status={PREVIEW_STATUS}
									facing="front"
									walking={false}
									size={SWATCH_SIZE}
								/>
							</span>
							{/* The name stays on EVERY tile, in use or not: "In use" alone would
							    hide the one thing someone came here to read, which is what the
							    look they are wearing is called. The second line is reserved on
							    all of them so picking one does not resize the row. */}
							<span className="flex flex-col items-center gap-px leading-3">
								<span className="text-[10.5px] first-letter:uppercase">{option.name}</span>
								{/* Foreground, not the accent: at 9.5px the blue measures 4.4:1 on the
									    selected tile's own fill in the light theme, which is under the
									    4.5 a word this small has to clear. The accent is already
									    carrying the selection, in the border. */}
								<span className={cn("text-[9.5px] font-semibold", active ? "text-foreground" : "invisible")}>
									In use
								</span>
							</span>
						</button>
					);
				})}
			</div>
		</section>
	);
}
