import { useState } from "react";
import type { SessionStatus } from "../renderer/types/workspace";
import { Bubble } from "./Bubble";
import { composeCast, HATS, PALETTES, type CastMember } from "./cast";
import { NameTag } from "./NameTag";
import { PROP_COLOURS, worstSeparation } from "./palette";
import { Procs } from "./Procs";
import { STATUS_LABELS } from "./preview";
import { ALL_COMPANION_STATUSES } from "./scene";
import { IRIS_BY_PALETTE, SPECIES, type SpeciesId } from "./species";

// The concept sheet: every character, across states, across the wallpaper range.
//
// It exists for one reason — this art cannot be reviewed by reading it. Every
// render-only bug in this feature so far has been found by LOOKING at a contact
// sheet: the laptop that measured fine and read as a grey blob, the fifteen Procs
// that showed six colours and six hats but zero colours worn with two hats, the
// dust that came off a Proc's ears. So a new character type gets the same
// treatment before it is offered to anybody.
//
// Mounted only on `companion.html` in a dev browser, behind the same both-conditions
// guard as the rest of the lab. Nothing here ships to a desktop.

/** The three the human has to be able to tell apart at a glance, plus the incumbent. */
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

	return (
		<div style={PAGE}>
			<header style={BAR}>
				<strong>Anime character concepts</strong>
				<span style={MUTED}>3 originals, on the one rig, across the wallpaper range</span>
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
				<button type="button" style={BUTTON} onClick={() => setHatIndex((n) => (n + 1) % HATS.length)}>
					hat: {HATS[hatIndex].name}
				</button>
				<button
					type="button"
					style={{ ...BUTTON, ...(walking ? BUTTON_ON : null) }}
					onClick={() => setWalking((w) => !w)}
				>
					walking
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
							<p style={{ ...IDENTITY, opacity: 0.7 }}>Says what the link is doing with: {species.tell}.</p>
						</div>
						{STATES.map((status) => (
							<figure key={status} style={CELL}>
								<Stand
									cast={lookFor(species.id, index, hatIndex)}
									status={status}
									walking={walking && status !== "idle"}
									bubble={BUBBLES[status]}
								/>
								<figcaption style={CAPTION}>{STATUS_LABELS[status]}</figcaption>
							</figure>
						))}
					</section>
				))}
			</div>

			<section style={{ ...SHEET, background: "#221d2e", display: "block" }}>
				<h2 style={{ ...TITLE, marginBottom: 6 }}>The whole state vocabulary, per character</h2>
				{SPECIES.filter((entry) => entry.id !== "proc").map((species, index) => (
					<div key={species.id} style={{ ...ROW, flexWrap: "wrap", alignItems: "flex-end" }}>
						{ALL_COMPANION_STATUSES.map((status) => (
							<figure key={status} style={{ ...CELL, minWidth: 132 }}>
								<Stand cast={lookFor(species.id, index + 1, hatIndex)} status={status} walking={false} />
								<figcaption style={CAPTION}>{status.replace(/_/g, " ")}</figcaption>
							</figure>
						))}
					</div>
				))}
			</section>

			<section style={{ ...SHEET, background: "#191622", display: "block" }}>
				<h2 style={{ ...TITLE, marginBottom: 6 }}>Measured, not eyeballed</h2>
				<table style={TABLE}>
					<tbody>
						<tr>
							<th style={TH}>Kitsu ear lining on any wallpaper</th>
							<td style={TD}>{worstSeparation(PROP_COLOURS.inner).toFixed(2)}:1</td>
						</tr>
						<tr>
							<th style={TH}>Sprite wing plate on any wallpaper</th>
							<td style={TD}>{worstSeparation(PROP_COLOURS.linen).toFixed(2)}:1</td>
						</tr>
						<tr>
							<th style={TH}>Unit lamp, lit, on any wallpaper</th>
							<td style={TD}>{worstSeparation(PROP_COLOURS.spark).toFixed(2)}:1</td>
						</tr>
						{PALETTES.map((palette) => (
							<tr key={palette.id}>
								<th style={TH}>{palette.name} iris against the eye's ink</th>
								<td style={TD}>
									<span style={{ color: IRIS_BY_PALETTE[palette.id] }}>■</span> {IRIS_BY_PALETTE[palette.id]}
								</td>
							</tr>
						))}
					</tbody>
				</table>
			</section>
		</div>
	);
}

/** A demo look: this species, a colour per row, and whichever hat the sheet is showing. */
function lookFor(species: SpeciesId, index: number, hatIndex: number): CastMember {
	return composeCast(PALETTES[(index * 2 + 1) % PALETTES.length], HATS[hatIndex], species);
}

const BUBBLES: Partial<Record<SessionStatus, { text: string; tone?: "alert" }>> = {
	working: { text: "Running the test suite" },
	needs_input: { text: "Waiting for you", tone: "alert" },
};

/** One character standing on the sheet's floor, with its name chip and its bubble. */
function Stand({
	cast,
	status,
	walking,
	bubble,
}: {
	cast: CastMember;
	status: SessionStatus;
	walking: boolean;
	bubble?: { text: string; tone?: "alert" };
}) {
	return (
		<div style={STAND}>
			{bubble ? (
				<div style={{ marginBottom: 2 }}>
					<Bubble text={bubble.text} tone={bubble.tone} decay="fresh" />
				</div>
			) : null}
			{/* The same class the overlay puts on a Proc, so reduced motion reaches this
			    art through exactly the rule that governs it on a real desktop. Laid out
			    in flow here rather than absolutely, which is the one thing about a
			    contact sheet that is not like a desktop. */}
			<div className="companion-proc" style={{ position: "static" }}>
				<Procs cast={cast} status={status} facing="front" walking={walking} size={128} />
			</div>
			<NameTag name="login rate limit" project="demo-app" />
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
const IDENTITY: React.CSSProperties = { margin: "4px 0 0", opacity: 0.86 };
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
const TABLE: React.CSSProperties = { borderCollapse: "collapse", font: "500 11px/1.6 ui-monospace, monospace" };
const TH: React.CSSProperties = { textAlign: "left", padding: "2px 14px 2px 0", fontWeight: 500, opacity: 0.75 };
const TD: React.CSSProperties = { padding: "2px 0" };
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
