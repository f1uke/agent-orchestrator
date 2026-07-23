import { useEffect, useMemo, useState } from "react";
import {
	accessoriesFor,
	APPEARANCE_AXES,
	castFromLook,
	HATS,
	paletteOf,
	palettesFor,
	storedIdFor,
	withSpecies,
	type AppearanceAxis,
	type AxisId,
	type CastMember,
} from "../../../companion/cast";
import { composeCast } from "../../../companion/cast";
import { castFor, isAxisChosen, isSpeciesChosen, resolveLook, resolveSpecies } from "../../../companion/look-store";
import {
	clearStoredLookChoice,
	clearStoredProjectSpecies,
	pruneStoredLooks,
	storeLookChoice,
	storeProjectSpecies,
	useLookOverrides,
	useProjectLooks,
} from "../../../companion/look-store-live";
import { SPECIES, type SpeciesId } from "../../../companion/species";
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

type LibrarySession = { id: string; title: string; project: string };
type LibraryGroup = { name: string; sessions: LibrarySession[] };

export function PetLibrary() {
	const query = useWorkspaceQuery();
	const looks = useLookOverrides();
	const projectLooks = useProjectLooks();
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
						.map((session) => ({ id: session.id, title: session.title, project: project.name })),
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
		pruneStoredLooks(
			projects.flatMap((project) => project.sessions.map((session) => session.id)),
			projects.map((project) => project.name),
		);
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

	// Two questions, two keys: the CREATURE is the project's, the colour and the
	// accessory are this session's. The picker has to ask both, in that order, because
	// the creature decides what the other two axes even offer.
	const species = resolveSpecies(selected.project, projectLooks);
	const look = resolveLook(selected.id, looks);
	const cast = withSpecies(castFromLook(look), species);

	const chooseCreature = (next: SpeciesId) => {
		storeProjectSpecies(selected.project, next);
		aoBridge.companion.looksChanged();
	};
	const resetCreature = () => {
		clearStoredProjectSpecies(selected.project);
		aoBridge.companion.looksChanged();
	};

	return (
		<div className="flex flex-col gap-3">
			<p className="text-[12px] leading-5 text-muted-foreground">
				Every project is given a creature and every session a colour, the moment they exist — so you never have to come
				here. Change the creature when two projects come out as the same animal, and a colour when two sessions on one
				project come out too alike to tell apart.
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

					<CreaturePicker
						project={selected.project}
						species={species}
						cast={cast}
						chosen={isSpeciesChosen(projectLooks, selected.project)}
						onChoose={chooseCreature}
						onReset={resetCreature}
					/>

					{APPEARANCE_AXES.map((axis) => (
						<AxisPicker
							key={axis.id}
							axis={axis}
							species={species}
							cast={cast}
							chosen={isAxisChosen(looks, selected.id, axis.id)}
							onChoose={(optionId) => choose(axis.id, storedIdFor(axis.id, species, optionId))}
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
	// ⚠ The creature too, not just the session's colour. Without it this list drew every
	// session as a Proc while the picker beside it and the band on the desktop drew the
	// project's animal — three views of one pet, disagreeing. Caught by opening the app.
	const projectLooks = useProjectLooks();
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
					cast={withSpecies(castFor(session.id, looks), resolveSpecies(session.project, projectLooks))}
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
 * Which CREATURE a project is drawn as.
 *
 * ⚠ Chosen per PROJECT, and it is the only control on this screen that is. Every
 * session on a project is the same animal, which is what took the coloured mark off
 * the name chip: the band groups itself by shape and there is nothing left to decode.
 * Six creatures tell six projects apart on their own; a seventh collides, and this is
 * the answer to that.
 *
 * Every tile wears the SESSION's current colour and accessory by slot, so it is the
 * real answer to "what would I get" rather than a picture of somebody else's pet.
 */
function CreaturePicker({
	project,
	species,
	cast,
	chosen,
	onChoose,
	onReset,
}: {
	project: string;
	species: SpeciesId;
	cast: CastMember;
	chosen: boolean;
	onChoose: (species: SpeciesId) => void;
	onReset: () => void;
}) {
	return (
		<section role="group" aria-label="Creature" className="flex flex-col gap-1.5">
			<div className="flex items-baseline justify-between gap-2">
				<h4 className="text-[12.5px] font-medium text-foreground">Creature</h4>
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
			<p className="text-[11.5px] leading-4 text-muted-foreground">
				Which animal <span className="text-foreground">{project}</span> is. Every session on it is the same one, so a
				glance at the band says which project a pet belongs to.
			</p>
			<div className="grid gap-1.5 [grid-template-columns:repeat(auto-fill,minmax(66px,1fr))]">
				{SPECIES.map((entry) => {
					const active = entry.id === species;
					return (
						<button
							key={entry.id}
							type="button"
							aria-pressed={active}
							aria-label={`${entry.name}${active ? " (in use)" : ""}`}
							onClick={() => onChoose(entry.id)}
							className={cn(
								"flex flex-col items-center gap-0.5 rounded-md border py-1.5 transition-colors",
								active
									? "border-accent bg-secondary text-foreground"
									: "border-border text-muted-foreground hover:border-passive hover:bg-interactive-hover",
							)}
						>
							<span
								className="flex items-end justify-center rounded px-1 pb-0.5 pt-1"
								style={{ background: PROCS_INK }}
								aria-hidden="true"
							>
								<Procs
									cast={withSpecies(cast, entry.id)}
									status={PREVIEW_STATUS}
									facing="front"
									walking={false}
									size={SWATCH_SIZE}
								/>
							</span>
							<span className="flex flex-col items-center gap-px leading-3">
								<span className="text-[10.5px]">{entry.name}</span>
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

/**
 * One axis' worth of choices, each drawn as the pet it would actually produce.
 *
 * A colour tile wears the session's CURRENT accessory and an accessory tile its current
 * colour, so every tile is the real answer to "what would I get" rather than an abstract
 * chip. That is the whole reason to have a gallery instead of a list of names.
 *
 * ⚠ The options come from the CREATURE, not from the axis' own list. The axis registry
 * carries the Proc's six as a default; offering those to a slime would be offering six
 * hats to a jelly cube.
 */
function AxisPicker({
	axis,
	species,
	cast,
	chosen,
	onChoose,
	onReset,
}: {
	axis: AppearanceAxis;
	species: SpeciesId;
	cast: CastMember;
	chosen: boolean;
	onChoose: (optionId: string) => void;
	onReset: () => void;
}) {
	const options = axis.id === "palette" ? palettesFor(species) : accessoriesFor(species);
	const worn = axis.id === "palette" ? cast.palette : cast.hatId;
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
				{options.map((option) => {
					const active = worn === option.id;
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
									cast={
										axis.id === "palette"
											? composeCast(paletteOf(species, option.id), HATS[0], species, cast.hatId)
											: composeCast(paletteOf(species, cast.palette), HATS[0], species, option.id)
									}
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
