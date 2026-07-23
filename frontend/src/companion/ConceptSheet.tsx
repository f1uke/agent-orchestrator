import { useState } from "react";
import type { SessionStatus } from "../renderer/types/workspace";
import { Bubble } from "./Bubble";
import { accessoriesFor, composeCast, HATS, palettesFor, type CastMember } from "./cast";
import { NameTag } from "./NameTag";
import { Procs } from "./Procs";
import { STATUS_LABELS } from "./preview";
import { ALL_COMPANION_STATUSES } from "./scene";
import { SPECIES, type SpeciesId } from "./species";

// The concept sheet: every creature, across states, across the wallpaper range.
//
// It exists for one reason — this art cannot be reviewed by reading it. Every
// render-only bug in this feature so far has been found by LOOKING at a contact
// sheet: the laptop that measured fine and read as a grey blob, the fifteen Procs
// that showed six colours and six hats but zero colours worn with two hats, the dust
// that came off a Proc's ears. A new BODY gets the same treatment, and more of it,
// because a body is not a parameter that can be checked by arithmetic.
//
// Mounted only on `companion.html` in a dev browser, behind the same both-conditions
// guard as the rest of the lab. Nothing here ships to a desktop.

/** The three a person has to be able to tell apart at a glance. */
const STATES: SessionStatus[] = ["idle", "working", "needs_input"];

/**
 * The wallpaper range, as tones rather than pictures. Contrast depends only on
 * relative luminance, so a light, a mid and a dark grey — plus one busy photo-ish
 * gradient — is the whole axis, not a sample of it.
 */
const TONES = [
	{ id: "light", label: "light wallpaper", background: "#f2f0f5" },
	{ id: "mid", label: "mid wallpaper", background: "#8d8a97" },
	{ id: "dark", label: "dark wallpaper", background: "#1b1a22" },
	{
		id: "busy",
		label: "busy wallpaper",
		background: "linear-gradient(115deg, #2b1c3f 0%, #d8c7a2 38%, #16394a 62%, #f4f1ea 100%)",
	},
];

export function ConceptSheet({ onClose }: { onClose: () => void }) {
	const [tone, setTone] = useState(TONES[0]);
	const [hatIndex, setHatIndex] = useState(0);
	const [walking, setWalking] = useState(true);
	const [small, setSmall] = useState(false);

	return (
		<div style={PAGE}>
			<header style={BAR}>
				<strong>Creature concepts</strong>
				<span style={MUTED}>six silhouettes, one set of machinery</span>
				<span style={{ flex: 1 }} />
				{TONES.map((entry) => (
					<button
						key={entry.id}
						type="button"
						style={{ ...BUTTON, ...(entry.id === tone.id ? BUTTON_ON : null) }}
						onClick={() => setTone(entry)}
					>
						{entry.label}
					</button>
				))}
				<button type="button" style={BUTTON} onClick={() => setHatIndex((n) => (n + 1) % 4)}>
					accessory {hatIndex + 1} of each
				</button>
				<button
					type="button"
					style={{ ...BUTTON, ...(walking ? BUTTON_ON : null) }}
					onClick={() => setWalking((w) => !w)}
				>
					moving
				</button>
				<button type="button" style={{ ...BUTTON, ...(small ? BUTTON_ON : null) }} onClick={() => setSmall((v) => !v)}>
					actual size
				</button>
				<button type="button" style={BUTTON} onClick={onClose}>
					close
				</button>
			</header>

			<div style={{ ...SHEET, background: tone.background }}>
				{SPECIES.map((species, index) => (
					<section key={species.id} style={ROW}>
						<div style={CARD}>
							<h2 style={TITLE}>{species.name}</h2>
							<p style={IDENTITY}>{species.identity}</p>
							<p style={{ ...IDENTITY, opacity: 0.72 }}>
								Reports the link with <b>{species.tell}</b> · gets about by <b>{species.locomotion}</b>
							</p>
							<p style={{ ...IDENTITY, opacity: 0.72 }}>
								Wears:{" "}
								{accessoriesFor(species.id)
									.map((worn) => worn.name)
									.join(" · ")}
							</p>
						</div>
						{STATES.map((status) => (
							<figure key={status} style={CELL}>
								<Stand
									cast={lookFor(species.id, index, hatIndex)}
									status={status}
									walking={walking && status !== "idle"}
									bubble={BUBBLES[status]}
									size={small ? 74 : 128}
								/>
								<figcaption style={CAPTION}>{STATUS_LABELS[status]}</figcaption>
							</figure>
						))}
					</section>
				))}
			</div>

			<section style={{ ...SHEET, background: "#221d2e", display: "block" }}>
				<h2 style={{ ...TITLE, marginBottom: 6 }}>All fifteen states, per creature</h2>
				{SPECIES.filter((entry) => entry.id !== "proc").map((species, index) => (
					<div key={species.id} style={{ ...ROW, flexWrap: "wrap", alignItems: "flex-end" }}>
						{ALL_COMPANION_STATUSES.map((status) => (
							<figure key={status} style={{ ...CELL, minWidth: 132 }}>
								<Stand cast={lookFor(species.id, index + 1, hatIndex)} status={status} walking={false} size={110} />
								<figcaption style={CAPTION}>{status.replace(/_/g, " ")}</figcaption>
							</figure>
						))}
					</div>
				))}
			</section>
		</div>
	);
}

/** A demo look: this creature, one of ITS OWN colours, and whichever hat the sheet shows. */
function lookFor(species: SpeciesId, index: number, hatIndex: number): CastMember {
	const colours = palettesFor(species);
	const worn = accessoriesFor(species);
	return composeCast(colours[(index * 2 + 1) % colours.length], HATS[0], species, worn[hatIndex % worn.length].id);
}

const BUBBLES: Partial<Record<SessionStatus, { text: string; tone?: "alert" }>> = {
	working: { text: "Running the test suite" },
	needs_input: { text: "Waiting for you", tone: "alert" },
};

/** One creature standing on the sheet's floor, with its name chip and its bubble. */
function Stand({
	cast,
	status,
	walking,
	bubble,
	size,
}: {
	cast: CastMember;
	status: SessionStatus;
	walking: boolean;
	bubble?: { text: string; tone?: "alert" };
	size: number;
}) {
	return (
		<div style={STAND}>
			{bubble ? (
				<div style={{ marginBottom: 2 }}>
					<Bubble text={bubble.text} tone={bubble.tone} decay="fresh" />
				</div>
			) : null}
			{/* The same class the overlay puts on a pet, so reduced motion reaches this art
			    through exactly the rule that governs it on a real desktop. Laid out in flow
			    here rather than absolutely, which is the one thing about a contact sheet
			    that is not like a desktop. */}
			<div className="companion-proc" style={{ position: "static" }}>
				<Procs cast={cast} status={status} facing="front" walking={walking} size={size} />
			</div>
			<NameTag name="login rate limit" />
		</div>
	);
}

const PAGE: React.CSSProperties = {
	position: "fixed",
	inset: 0,
	overflowY: "auto",
	pointerEvents: "auto",
	background: "#100d18",
	color: "#f3f0f8",
	font: "500 12px/1.5 ui-sans-serif, system-ui, sans-serif",
	zIndex: 10000,
};

const BAR: React.CSSProperties = {
	position: "sticky",
	top: 0,
	display: "flex",
	gap: 6,
	alignItems: "center",
	padding: "8px 12px",
	background: "#16131f",
	borderBottom: "1px solid #3a3448",
	zIndex: 2,
};

const SHEET: React.CSSProperties = { display: "grid", gap: 10, padding: 14 };
const ROW: React.CSSProperties = { display: "flex", gap: 14, alignItems: "flex-end" };
const CARD: React.CSSProperties = {
	width: 250,
	alignSelf: "center",
	background: "rgba(16,13,24,0.82)",
	border: "1px solid #3a3448",
	borderRadius: 10,
	padding: "8px 10px",
};
const TITLE: React.CSSProperties = { font: "700 15px/1.3 ui-sans-serif, system-ui", margin: 0 };
const IDENTITY: React.CSSProperties = { margin: "4px 0 0", opacity: 0.88 };
const CELL: React.CSSProperties = { margin: 0, display: "grid", justifyItems: "center", minWidth: 168 };
const STAND: React.CSSProperties = { display: "grid", justifyItems: "center" };
const CAPTION: React.CSSProperties = {
	marginTop: 6,
	padding: "2px 6px",
	borderRadius: 5,
	background: "rgba(16,13,24,0.78)",
	font: "600 10px/1.4 ui-sans-serif, system-ui",
	letterSpacing: "0.03em",
};
const MUTED: React.CSSProperties = { opacity: 0.6 };
const BUTTON: React.CSSProperties = {
	background: "#241f31",
	color: "inherit",
	border: "1px solid #3a3448",
	borderRadius: 6,
	padding: "3px 8px",
	font: "inherit",
	cursor: "pointer",
};
const BUTTON_ON: React.CSSProperties = { background: "#3c2f5c", borderColor: "#7c5cf0" };
