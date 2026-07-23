import { useEffect, useMemo, useState } from "react";
import { castForSession, withSpecies, type CastMember } from "../../../companion/cast";
import { isSpeciesChosen, resolveSpecies } from "../../../companion/look-store";
import {
	clearStoredProjectSpecies,
	pruneStoredProjectLooks,
	storeProjectSpecies,
	useProjectLooks,
} from "../../../companion/look-store-live";
import { SPECIES, speciesById, type SpeciesId } from "../../../companion/species";
import { PROCS_INK } from "../../../companion/palette";
import { Procs } from "../../../companion/Procs";
import { useWorkspaceQuery } from "../../hooks/useWorkspaceQuery";
import { aoBridge } from "../../lib/bridge";
import { cn } from "../../lib/utils";
import { useUiStore } from "../../stores/ui-store";

// The Pet library: which CREATURE each project is.
//
// That is the whole screen, and the smallness is the design. A pet has three things about
// it — its creature, its colour and what it is wearing — and only the first is a question
// a person can answer better than a hash can. The creature says WHICH PROJECT, which is
// knowledge the machine does not have; the other two only have to be DIFFERENT from each
// other, which is exactly what a hash of the session ref is for.
//
// So colour and accessory are automatic, per session, stable across restarts, and there is
// no control for them anywhere. An earlier build let a human dress one session by hand; it
// was removed, store and all, because a second source of truth for a pet's colour costs
// every reader a branch and buys a choice nobody wanted to make.
//
// This still OVERRIDES a default rather than filling a blank: every project is already
// some animal the moment it exists (the hash of its name). This is the escape hatch for
// when two projects come out as the same one — and, with six creatures, the answer to the
// seventh project.

/**
 * The plainest scene there is: no ground prop, no held prop, nothing emitted - just the
 * figure and its cord.
 *
 * Which is exactly right for a look picker, because the figure and the cord are the part
 * this screen CANNOT change. Drawing a pet holding a laptop would put a status into a
 * picture that is not about status.
 */
const PREVIEW_STATUS = "unknown";

/** Dark to light, the range a desktop wallpaper actually spans. Mirrors CompanionPreview. */
const WALLPAPER_TONES = ["#12141c", "#3d4457", "#6f727b", "#b6b2a8", "#f2efe8"];

/**
 * How many of a project's pets stand on the strip.
 *
 * A cap rather than all of them, because the strip is an ARGUMENT — same animal, different
 * colours — and four make that argument as well as twenty do. Anything over the cap is
 * COUNTED in the caption rather than dropped silently.
 *
 * ⚠ FOUR, measured rather than guessed. The detail column is ~438px here and a Proc's drawn
 * frame is about 1.15× the size it is asked for, so six at any readable size overflow and
 * wrap — which lands a single orphaned pet on a second row under five. Four at 88 is ~405px
 * and fits with room to spare.
 */
const STRIP_MAX = 4;

const SWATCH_SIZE = 50;
const THUMB_SIZE = 32;

/** Big when there are few, smaller when the strip is full, so it is always ONE row. */
function stripSize(count: number): number {
	if (count <= 2) return 112;
	if (count === 3) return 96;
	return 88;
}

type LibrarySession = { id: string; title: string };
type LibraryProject = { name: string; sessions: LibrarySession[] };

export function PetLibrary() {
	const query = useWorkspaceQuery();
	const projectLooks = useProjectLooks();
	const request = useUiStore((state) => state.petLibraryRequest);
	const clearRequest = useUiStore((state) => state.requestPetLibrary);
	const [picked, setPicked] = useState<string | null>(null);

	// Terminated sessions have no pet on the band, so there is nothing of theirs to draw.
	// A project all of whose sessions have ended therefore drops off this list — but NOT
	// out of the store; see the pruning note below.
	const projects = useMemo<LibraryProject[]>(
		() =>
			(query.data ?? [])
				.map((project) => ({
					name: project.name,
					sessions: project.sessions
						.filter((session) => !session.isTerminated)
						.map((session) => ({ id: session.id, title: session.title })),
				}))
				.filter((project) => project.sessions.length > 0),
		[query.data],
	);
	const selected = projects.find((project) => project.name === picked) ?? projects[0] ?? null;

	// Forget the projects that are gone.
	//
	// ⚠ Against EVERY project the workspace knows, including those whose sessions have all
	// ended — a project with nothing running is still a project, and its creature has to
	// survive the quiet spell. Pruning against the list rendered above would delete it the
	// moment its last worker stopped.
	//
	// Guarded on `isSuccess`: an in-flight or failed fetch is not evidence that anybody's
	// project is gone.
	const workspace = query.data;
	useEffect(() => {
		if (!query.isSuccess || !workspace) return;
		pruneStoredProjectLooks(workspace.map((project) => project.name));
	}, [query.isSuccess, workspace]);

	// A right-click on a pet asked for this SESSION, and the creature is chosen per
	// project — so the app resolves one to the other against the list it already holds,
	// which is the only place the two are authoritatively related. Honoured once and then
	// forgotten, or the next visit to Settings would jump somewhere unasked.
	useEffect(() => {
		if (!request) return;
		const owner = projects.find((project) => project.sessions.some((session) => session.id === request));
		if (owner) setPicked(owner.name);
		clearRequest(null);
	}, [request, projects, clearRequest]);

	if (!selected) {
		return (
			<p className="text-[12px] leading-5 text-muted-foreground">
				No projects with anything running yet. Every project is given a creature the moment it exists, and this is where
				you can change the one it got.
			</p>
		);
	}

	const species = resolveSpecies(selected.name, projectLooks);
	const chooseCreature = (next: SpeciesId) => {
		storeProjectSpecies(selected.name, next);
		aoBridge.companion.looksChanged();
	};
	const resetCreature = () => {
		clearStoredProjectSpecies(selected.name);
		aoBridge.companion.looksChanged();
	};

	// The tiles wear the FIRST session's colour and accessory slot, so a tile is the real
	// answer to "what would I get" rather than a picture of somebody else's pet.
	const sample = withSpecies(castForSession(selected.sessions[0].id), species);

	return (
		<div className="flex flex-col gap-3">
			<p className="text-[12px] leading-5 text-muted-foreground">
				Every project is given a creature the moment it exists, so you never have to come here. Change it when two
				projects come out as the same animal. Each session's own colour is picked for it and is never the same question.
			</p>

			<div className="grid grid-cols-[196px_minmax(0,1fr)] gap-3">
				<nav aria-label="Projects" className="flex max-h-[380px] flex-col gap-0.5 overflow-y-auto pr-1">
					{projects.map((project) => (
						<ProjectRow
							key={project.name}
							project={project}
							species={resolveSpecies(project.name, projectLooks)}
							active={project.name === selected.name}
							onSelect={() => setPicked(project.name)}
						/>
					))}
				</nav>

				<div className="flex min-w-0 flex-col gap-3">
					<ProjectPets project={selected} species={species} />

					<CreaturePicker
						project={selected.name}
						species={species}
						cast={sample}
						chosen={isSpeciesChosen(projectLooks, selected.name)}
						onChoose={chooseCreature}
						onReset={resetCreature}
					/>
				</div>
			</div>
		</div>
	);
}

/**
 * The project's live sessions, standing together as they really look.
 *
 * The picture IS the explanation: one animal, one colour each. It is what makes "every
 * session on this project becomes a ghost" a thing you can see rather than a sentence you
 * have to trust, and it is drawn from the same two functions the desktop draws from.
 *
 * ⚠ On a WALLPAPER strip, not an app surface: the pets live on a desktop, and the ink rim
 * exists so they survive any tone behind them. A flat panel would flatter the art and hide
 * the failure mode it guards.
 */
function ProjectPets({ project, species }: { project: LibraryProject; species: SpeciesId }) {
	const shown = project.sessions.slice(0, STRIP_MAX);
	const hidden = project.sessions.length - shown.length;
	const size = stripSize(shown.length);
	const creature = speciesById(species);
	return (
		<div className="flex flex-col gap-2">
			<div
				data-pet-strip={project.name}
				className="flex flex-nowrap items-end justify-center gap-1 overflow-hidden rounded-lg border border-border pt-2"
				style={{ background: `linear-gradient(100deg, ${WALLPAPER_TONES.join(", ")})` }}
				aria-hidden="true"
			>
				{shown.map((session) => (
					<Procs
						key={session.id}
						cast={withSpecies(castForSession(session.id), species)}
						status={PREVIEW_STATUS}
						facing="front"
						walking={false}
						size={size}
					/>
				))}
			</div>
			{/* The caption is the accessible name for the picture above it, which is why the
			    picture itself is hidden: a sprite would announce "Teal Ghost, bow, unknown"
			    six times over, and "unknown" is a session status this screen is not about. */}
			<p className="text-center text-[12px] text-muted-foreground" data-pet-preview={species}>
				{project.sessions.length === 1 ? (
					<>
						<span className="text-foreground">{project.sessions[0].title}</span> is a {creature.name.toLowerCase()}
					</>
				) : hidden > 0 ? (
					<>
						{/* "4 of the 9" rather than "all 9 (5 not shown)": naming the cap up front is
						    both shorter and honest, and it stops the sentence wrapping a two-word
						    parenthesis onto a line of its own. */}
						{shown.length} of the {project.sessions.length} sessions on{" "}
						<span className="text-foreground">{project.name}</span> — all {creature.name.toLowerCase()}s, each in its
						own colour
					</>
				) : (
					<>
						All {project.sessions.length} sessions on <span className="text-foreground">{project.name}</span> are{" "}
						{creature.name.toLowerCase()}s, each in its own colour
					</>
				)}
			</p>
		</div>
	);
}

function ProjectRow({
	project,
	species,
	active,
	onSelect,
}: {
	project: LibraryProject;
	species: SpeciesId;
	active: boolean;
	onSelect: () => void;
}) {
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
				{/* The row's own pet is the project's FIRST session, so the thumbnail is a real
				    one off the band rather than a stand-in nobody owns. */}
				<Procs
					cast={withSpecies(castForSession(project.sessions[0].id), species)}
					status={PREVIEW_STATUS}
					facing="front"
					walking={false}
					size={THUMB_SIZE}
				/>
			</span>
			<span className="min-w-0 flex-1 truncate">{project.name}</span>
			<span className="shrink-0 text-[10.5px] text-passive tabular-nums">{project.sessions.length}</span>
		</button>
	);
}

/**
 * Which CREATURE a project is drawn as. The only control on this screen.
 *
 * ⚠ Chosen per PROJECT. Every session on a project is the same animal, which is what took
 * the coloured mark off the name chip: the band groups itself by shape and there is nothing
 * left to decode. Six creatures tell six projects apart on their own; a seventh collides,
 * and this is the answer to that.
 *
 * Every tile wears the project's own colour and accessory slot, so it is the real answer to
 * "what would I get" rather than a picture of somebody else's pet.
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
			{/* muted, not `passive`: `passive` measures 2.8:1 on the light theme, which is
			    under the 4.5 an 11.5px line needs. */}
			<p className="text-[11.5px] leading-4 text-muted-foreground">
				Which animal <span className="text-foreground">{project}</span> is. Every session on it is the same one, so a
				glance at the band says which project a pet belongs to.
			</p>
			{/* auto-fill, so a seventh creature simply wraps rather than overflowing a column
			    count written down here. */}
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
							{/* The tile gets its own dark plate rather than the app surface. These are
							    colours chosen to survive a WALLPAPER, and on a light theme's near-white
							    card the pale ones wash out completely. */}
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
							{/* The name stays on EVERY tile, in use or not: "In use" alone would hide
							    the one thing someone came here to read. The second line is reserved on
							    all of them so picking one does not resize the row. */}
							<span className="flex flex-col items-center gap-px leading-3">
								<span className="text-[10.5px]">{entry.name}</span>
								{/* Foreground, not the accent: at 9.5px the blue measures 4.4:1 on the
								    selected tile's own fill in the light theme, under the 4.5 a word
								    this small has to clear. The accent already carries the selection,
								    in the border. */}
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
