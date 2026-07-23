import { useState } from "react";
import type { SessionStatus } from "../renderer/types/workspace";
import { startRally, tick, type World } from "./behaviour";
import type { CompanionActivity } from "./feed";
import type { ManualFeed } from "./dev-feed";
import { STATUS_LABELS } from "./preview";
import { appendActivity, everyStatus, makeActivity } from "./dev-roster";
import { ALL_COMPANION_STATUSES } from "./scene";
import { SPECIES, type SpeciesId } from "./species";

// The playground, mounted on `companion.html` in dev only (see main.tsx).
//
// The overlay's whole point is that it lives on a desktop, and a desktop cannot be
// put in a test: every state is reached by waiting for a real session to get there.
// This drives the SAME feed interface, the SAME decay ladder and the SAME engine —
// nothing here is a second implementation — so any state is one click away and can
// actually be LOOKED at, which is how every render-only bug in this feature so far
// has been found.

// Frames in the shape the SSE really delivers them, so the panel exercises the
// decay ladder rather than setting a string on a bubble.
const FRAMES: Array<{ label: string; frame: (sessionId: string) => Record<string, unknown> }> = [
	{
		label: "Bash (model's own words)",
		frame: (sessionId) => ({
			sessionId,
			kind: "tool_start",
			at: new Date().toISOString(),
			tool: "Bash",
			text: "Running the test suite",
			ttlMs: 20_000,
			coarse: "working",
			coarseTtlMs: 600_000,
		}),
	},
	{
		label: "Read a file",
		frame: (sessionId) => ({
			sessionId,
			kind: "tool_end",
			at: new Date().toISOString(),
			tool: "Read",
			target: "hooks.go",
			ttlMs: 8_000,
			coarse: "working",
			coarseTtlMs: 600_000,
		}),
	},
	{
		label: "A tool that failed",
		frame: (sessionId) => ({
			sessionId,
			kind: "tool_failed",
			at: new Date().toISOString(),
			tool: "Edit",
			target: "main.ts",
			ttlMs: 12_000,
			coarse: "working",
			coarseTtlMs: 600_000,
		}),
	},
	{
		label: "A message to the orchestrator",
		frame: (sessionId) => ({
			sessionId,
			kind: "message",
			at: new Date().toISOString(),
			text: "P1 is fixed and CI is green",
			ttlMs: 25_000,
			coarse: "working",
			coarseTtlMs: 600_000,
		}),
	},
	{
		label: "No tool — we don't know what happened",
		frame: (sessionId) => ({
			sessionId,
			kind: "tool_start",
			at: new Date().toISOString(),
			ttlMs: 15_000,
			coarse: "working",
			coarseTtlMs: 600_000,
		}),
	},
	{
		label: "Genuinely waiting for you",
		frame: (sessionId) => ({
			sessionId,
			kind: "activity",
			at: new Date().toISOString(),
			ttlMs: 0,
			coarse: "waiting",
			coarseTtlMs: 0,
		}),
	},
	{
		label: "Short TTL — watch it decay",
		frame: (sessionId) => ({
			sessionId,
			kind: "tool_start",
			at: new Date().toISOString(),
			tool: "Grep",
			target: "TODO",
			ttlMs: 4_000,
			coarse: "working",
			coarseTtlMs: 12_000,
		}),
	},
];

export type DevPanelProps = {
	feed: ManualFeed;
	setWorld: React.Dispatch<React.SetStateAction<World>> | null;
	reducedMotion: boolean;
	onReducedMotion: (value: boolean) => void;
	/** Which creature the whole band is drawn as. `mixed` deals all six round. */
	species: SpeciesId | "mixed";
	onSpecies: (species: SpeciesId | "mixed") => void;
	/** Opens the contact sheet: every creature, every state, every wallpaper tone. */
	onConceptSheet: () => void;
};

export function DevPanel({
	feed,
	setWorld,
	reducedMotion,
	onReducedMotion,
	species,
	onSpecies,
	onConceptSheet,
}: DevPanelProps) {
	const [roster, setRosterState] = useState<CompanionActivity[]>(() => feed.roster());
	const [selected, setSelected] = useState<string | null>(roster[0]?.sessionId ?? null);
	const [applyToAll, setApplyToAll] = useState(false);
	const [say, setSay] = useState("");
	const [open, setOpen] = useState(true);

	const commit = (next: CompanionActivity[]) => {
		setRosterState(next);
		feed.setRoster(next);
		if (!next.some((entry) => entry.sessionId === selected)) setSelected(next[0]?.sessionId ?? null);
	};

	const targets = applyToAll ? roster.map((entry) => entry.sessionId) : selected ? [selected] : [];

	const setStatus = (status: SessionStatus) =>
		commit(roster.map((entry) => (targets.includes(entry.sessionId) ? { ...entry, status } : entry)));

	const pushFrame = (frame: Record<string, unknown>) => {
		feed.push(frame as never);
	};

	if (!open) {
		return (
			<div style={{ ...SHELL, padding: "6px 10px" }}>
				<button type="button" style={BUTTON} onClick={() => setOpen(true)}>
					Procs lab ▸
				</button>
			</div>
		);
	}

	return (
		<div style={SHELL}>
			<header style={HEADER}>
				<strong>Procs lab</strong>
				<button type="button" style={BUTTON} onClick={() => setOpen(false)}>
					hide
				</button>
			</header>

			<Section title={`Cast (${roster.length})`}>
				<div style={{ display: "grid", gap: 2, maxHeight: 132, overflowY: "auto" }}>
					{roster.map((entry) => (
						<button
							key={entry.sessionId}
							type="button"
							onClick={() => setSelected(entry.sessionId)}
							style={{ ...ROW, ...(entry.sessionId === selected ? ROW_ON : null) }}
						>
							<span>{entry.name}</span>
							<span style={MUTED}>{STATUS_LABELS[entry.status]}</span>
						</button>
					))}
				</div>
				<div style={ROW_OF_BUTTONS}>
					<button type="button" style={BUTTON} onClick={() => commit(appendActivity(roster))}>
						+ session
					</button>
					<button type="button" style={BUTTON} onClick={() => commit(roster.slice(0, -1))}>
						− session
					</button>
					<button type="button" style={BUTTON} onClick={() => commit(everyStatus())}>
						all 15 states
					</button>
					<button type="button" style={BUTTON} onClick={() => commit([makeActivity(0, "working", new Set())])}>
						just one
					</button>
					<button
						type="button"
						style={BUTTON}
						onClick={() =>
							commit(
								roster.map((entry) =>
									targets.includes(entry.sessionId)
										? { ...entry, kind: entry.kind === "orchestrator" ? "worker" : "orchestrator" }
										: entry,
								),
							)
						}
					>
						orchestrator?
					</button>
				</div>
				<label style={CHECK}>
					<input type="checkbox" checked={applyToAll} onChange={(e) => setApplyToAll(e.target.checked)} />
					apply to the whole cast
				</label>
			</Section>

			<Section title="Creature (per PROJECT)">
				<p style={{ ...MUTED, margin: 0 }}>
					`by project` is what the real thing does: the creature comes from the project name, so every session on a
					project is the same animal. The rest force one body, to look at it across every state.
				</p>
				<div style={ROW_OF_BUTTONS}>
					{[
						{ id: "mixed" as const, name: "by project" },
						...SPECIES.map((entry) => ({ id: entry.id as SpeciesId | "mixed", name: entry.name })),
					].map((entry) => (
						<button
							key={entry.id}
							type="button"
							style={{ ...BUTTON, ...(entry.id === species ? ROW_ON : null) }}
							onClick={() => onSpecies(entry.id)}
						>
							{entry.name}
						</button>
					))}
				</div>
				<div style={ROW_OF_BUTTONS}>
					<button type="button" style={BUTTON} onClick={onConceptSheet}>
						concept sheet ▸
					</button>
				</div>
			</Section>

			<Section title="State (drives the scene + how it moves)">
				<div style={{ display: "grid", gridTemplateColumns: "1fr 1fr", gap: 2 }}>
					{ALL_COMPANION_STATUSES.map((status) => (
						<button key={status} type="button" style={BUTTON} onClick={() => setStatus(status)}>
							{STATUS_LABELS[status]}
						</button>
					))}
				</div>
			</Section>

			<Section title="What it says (real frames, real decay)">
				<div style={{ display: "grid", gap: 2 }}>
					{FRAMES.map((entry) => (
						<button
							key={entry.label}
							type="button"
							style={BUTTON}
							onClick={() => targets.forEach((id) => pushFrame(entry.frame(id)))}
						>
							{entry.label}
						</button>
					))}
				</div>
				<div style={{ display: "flex", gap: 4, marginTop: 4 }}>
					<input
						value={say}
						placeholder="say anything…"
						onChange={(event) => setSay(event.target.value)}
						style={INPUT}
					/>
					<button
						type="button"
						style={BUTTON}
						onClick={() =>
							targets.forEach((id) =>
								pushFrame({
									sessionId: id,
									kind: "tool_start",
									at: new Date().toISOString(),
									tool: "Bash",
									text: say,
									ttlMs: 30_000,
									coarse: "working",
									coarseTtlMs: 600_000,
								}),
							)
						}
					>
						say
					</button>
				</div>
				<div style={ROW_OF_BUTTONS}>
					<button type="button" style={BUTTON} onClick={() => targets.forEach((id) => feed.hush(id))}>
						silence it
					</button>
				</div>
			</Section>

			<Section title="Two of them talking (ao send)">
				<p style={{ ...MUTED, margin: 0 }}>
					Sends a real `message` frame from the coordinator to the selected Proc. They run to each other, hop, say their
					piece and go home.
				</p>
				<div style={ROW_OF_BUTTONS}>
					<button
						type="button"
						style={BUTTON}
						onClick={() => {
							const speaker = roster.find((entry) => entry.kind === "orchestrator") ?? roster[0];
							const listener = roster.find(
								(entry) => entry.sessionId !== speaker?.sessionId && targets.includes(entry.sessionId),
							);
							if (!speaker || !listener) return;
							pushFrame({
								sessionId: listener.sessionId,
								kind: "message",
								at: new Date().toISOString(),
								// The stamp `ao send` puts on every message body. It is what
								// names the sender, so the pairing is read rather than invented.
								text: `[from @${speaker.sessionId}] ${say.trim() || "P1 is fixed and CI is green"}`,
								ttlMs: 12_000,
							});
						}}
					>
						coordinator → this one
					</button>
				</div>
			</Section>

			<Section title="Roll-call (shake the coordinator)">
				<p style={{ ...MUTED, margin: 0 }}>
					The gesture itself is press-and-shake on the crowned Proc — this button is the same call, without the wrist.
					Everything on the coordinator&apos;s project runs in, stands round it, and goes home.
				</p>
				<div style={ROW_OF_BUTTONS}>
					<button
						type="button"
						style={BUTTON}
						onClick={() =>
							setWorld?.((current) => {
								const lead = current.pets.find((pet) => pet.kind === "orchestrator");
								return lead ? startRally(current, lead.id, Date.now()) : current;
							})
						}
					>
						rally
					</button>
				</div>
			</Section>

			<Section title="Motion">
				<div style={ROW_OF_BUTTONS}>
					<button
						type="button"
						style={BUTTON}
						onClick={() =>
							setWorld?.((current) => ({
								...current,
								pets: current.pets.map((pet) => ({ ...pet, restUntil: 0 })),
							}))
						}
					>
						stroll now
					</button>
					<button
						type="button"
						style={BUTTON}
						onClick={() =>
							setWorld?.((current) => ({
								...current,
								pets: current.pets.map((pet, index) => ({
									...pet,
									// Spread across the WHOLE band, so a big cast is laid out to be
									// looked at rather than piled into the left-hand third.
									x:
										current.band.minX +
										(current.pets.length < 2
											? 0
											: (index * (current.band.maxX - current.band.minX)) / (current.pets.length - 1)),
									motion: { kind: "standing" },
								})),
							}))
						}
					>
						scatter
					</button>
					<button
						type="button"
						style={BUTTON}
						onClick={() => setWorld?.((current) => tick(current, Date.now(), Math.random))}
					>
						step once
					</button>
				</div>
				<label style={CHECK}>
					<input type="checkbox" checked={reducedMotion} onChange={(e) => onReducedMotion(e.target.checked)} />
					prefers-reduced-motion
				</label>
			</Section>

			<p style={{ ...MUTED, margin: "6px 0 0" }}>Drag a Proc to throw it. Hover one for a second to open its card.</p>
		</div>
	);
}

function Section({ title, children }: { title: string; children: React.ReactNode }) {
	return (
		<section style={{ display: "grid", gap: 4, marginTop: 10 }}>
			<h2 style={{ font: "600 10px/1.4 ui-sans-serif, system-ui", margin: 0, letterSpacing: "0.04em", opacity: 0.65 }}>
				{title.toUpperCase()}
			</h2>
			{children}
		</section>
	);
}

const SHELL: React.CSSProperties = {
	position: "fixed",
	top: 12,
	left: 12,
	width: 268,
	maxHeight: "calc(100vh - 24px)",
	overflowY: "auto",
	// The page itself is click-through; the panel is the one thing on it that is not.
	pointerEvents: "auto",
	background: "#16131f",
	color: "#f3f0f8",
	border: "1px solid #3a3448",
	borderRadius: 10,
	padding: 10,
	font: "500 11px/1.45 ui-sans-serif, system-ui, sans-serif",
	boxShadow: "0 10px 30px rgba(0,0,0,0.35)",
	zIndex: 9999,
};

const HEADER: React.CSSProperties = { display: "flex", justifyContent: "space-between", alignItems: "center" };
const MUTED: React.CSSProperties = { opacity: 0.6, font: "500 10px/1.4 ui-sans-serif, system-ui, sans-serif" };
const ROW_OF_BUTTONS: React.CSSProperties = { display: "flex", flexWrap: "wrap", gap: 2, marginTop: 4 };
const CHECK: React.CSSProperties = { display: "flex", gap: 6, alignItems: "center", marginTop: 6, cursor: "pointer" };

const BUTTON: React.CSSProperties = {
	background: "#241f31",
	color: "inherit",
	border: "1px solid #3a3448",
	borderRadius: 6,
	padding: "3px 7px",
	font: "inherit",
	cursor: "pointer",
	textAlign: "left",
};

const ROW: React.CSSProperties = { ...BUTTON, display: "flex", justifyContent: "space-between", gap: 6 };
const ROW_ON: React.CSSProperties = { background: "#3c2f5c", borderColor: "#7c5cf0" };
const INPUT: React.CSSProperties = { ...BUTTON, flex: 1, minWidth: 0, cursor: "text" };
