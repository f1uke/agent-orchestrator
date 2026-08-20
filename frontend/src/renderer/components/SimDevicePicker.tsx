import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, ChevronDown, Loader2, Power } from "lucide-react";
import type { SimDevice } from "../hooks/useSimDevices";
import type { SimPowerRequest } from "../hooks/useSimPower";
import { cn } from "../lib/utils";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";

/**
 * The device picker: every simulator this machine has, which are booted, and
 * the two buttons that change that.
 *
 * 🗝 Why this replaced a Select. The tab used to list only BOOTED devices, so
 * with nothing running it was a dead end - the human had to leave the app, open
 * Simulator.app or run simctl by hand, and come back. The list itself never
 * needed to change to fix that: `GET /sim/devices` has always returned every
 * installed device with its state, and the panel was throwing the shut-down
 * ones away. What needed a home was the pair of actions, and a Select item is a
 * bad host for a button, a spinner, an elapsed counter and a failure reason.
 *
 * ⚠ The one layout rule this file exists to keep. The rendered device must not
 * change size or shift as this control's contents grow. That is why the trigger
 * is a FIXED width rather than a fitted one, and why everything else lives in
 * PopoverContent - which renders in a portal, floating over the layout. A list
 * that grows, a confirmation that appears, a two-line failure reason: none of
 * them can push the screen by a pixel, because none of them is in the screen's
 * layout at all.
 *
 * ⚠ The memory guard is not decoration. A booted simulator costs roughly 4 GB,
 * and this machine has hit a true OOM from three at once - tooling started
 * failing with `fork failed: resource temporarily unavailable`. So the count of
 * booted devices is on the header at all times, not only at the moment of
 * danger, and booting an ADDITIONAL device asks first. Nothing here ever shuts
 * a device down on the human's behalf: it warns, and they decide.
 */

/** What one booted simulator costs, near enough for a decision. */
const DEVICE_MEMORY = "~4 GB";

/**
 * The boot timeout, in the words the control uses. It has to match
 * simpower.BootTimeout: this is what the human is told they are waiting for,
 * and a control that promises a different deadline from the one the daemon
 * keeps is worse than one that promises nothing.
 */
const BOOT_TIMEOUT_LABEL = "2:00";

type PowerInfo = NonNullable<SimDevice["power"]>;

export function SimDevicePicker({
	chosen,
	devices,
	loading,
	onChoose,
	onPower,
	sessionId,
}: {
	chosen: string | null;
	devices: SimDevice[];
	loading: boolean;
	onChoose: (udid: string) => void;
	onPower: (request: SimPowerRequest) => void;
	sessionId: string;
}) {
	const [open, setOpen] = useState(false);
	// Which row is mid-confirmation, if any. Held here rather than per row so
	// opening one confirmation closes another: two live "are you sure"s in one
	// list is how the wrong one gets answered.
	const [confirming, setConfirming] = useState<string | null>(null);

	const booted = useMemo(() => devices.filter((d) => d.state === "Booted"), [devices]);
	const off = useMemo(() => devices.filter((d) => d.state !== "Booted"), [devices]);
	const current = devices.find((d) => d.udid === chosen) ?? null;

	// Closing the popover abandons any half-asked question rather than leaving
	// it armed for the next time it is opened.
	useEffect(() => {
		if (!open) setConfirming(null);
	}, [open]);

	return (
		<Popover onOpenChange={setOpen} open={open}>
			<PopoverTrigger asChild>
				<button
					aria-label="Simulator to watch"
					// A FIXED width, not a fitted one. The trigger sits in the toolbar
					// above the device, and a pill that changed width with the chosen
					// device's name would move the screen underneath it.
					className="flex h-7 w-[190px] shrink-0 items-center gap-1 rounded-md px-2 text-[12px] font-medium text-foreground transition-colors hover:bg-overlay"
					type="button"
				>
					{current ? (
						// Two spans, not one string: the RUNTIME is what tells two
						// devices of the same model apart, so it is the half that
						// survives a narrow rail. Truncating the pair as one string
						// dropped it first and left "iPhone 17 Pro Max · iO…".
						<>
							<span className="min-w-0 flex-1 truncate text-left">{current.name}</span>
							<span className="shrink-0 text-muted-foreground">{current.runtime}</span>
						</>
					) : (
						<span className="min-w-0 flex-1 truncate text-left text-muted-foreground">
							{loading ? "Looking for simulators…" : "Choose a simulator"}
						</span>
					)}
					<ChevronDown aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
				</button>
			</PopoverTrigger>

			<PopoverContent align="start" className="w-[320px] p-0">
				<BootedCount count={booted.length} />

				<div className="max-h-[320px] overflow-y-auto p-1">
					{devices.length === 0 ? (
						<p className="px-2 py-3 text-[11px] leading-snug text-muted-foreground">
							{loading ? "Looking for simulators…" : "This machine has no iOS Simulators installed."}
						</p>
					) : null}

					<Group label="Booted" show={booted.length > 0}>
						{booted.map((device) => (
							<DeviceRow
								bootedCount={booted.length}
								confirming={confirming === device.udid}
								device={device}
								key={device.udid}
								onChoose={() => {
									onChoose(device.udid);
									setOpen(false);
								}}
								onConfirm={setConfirming}
								onPower={onPower}
								sessionId={sessionId}
								watching={device.udid === chosen}
							/>
						))}
					</Group>

					<Group label="Shut down" show={off.length > 0}>
						{off.map((device) => (
							<DeviceRow
								bootedCount={booted.length}
								confirming={confirming === device.udid}
								device={device}
								key={device.udid}
								onChoose={() => {}}
								onConfirm={setConfirming}
								onPower={onPower}
								sessionId={sessionId}
								watching={false}
							/>
						))}
					</Group>
				</div>
			</PopoverContent>
		</Popover>
	);
}

/**
 * How many devices are up, always - not a warning that appears once it is too
 * late to be useful. The cost per device is stated beside the count because the
 * count alone means nothing to somebody who has not just run out of memory.
 */
function BootedCount({ count }: { count: number }) {
	return (
		<div
			className="flex items-center justify-between gap-2 border-b border-border px-3 py-2 text-[11px]"
			data-testid="sim-booted-count"
		>
			<span className={cn("font-medium", count >= 2 ? "text-warning" : "text-foreground")}>
				{count === 0 ? "Nothing booted" : `${count} booted`}
			</span>
			<span className="text-muted-foreground">{DEVICE_MEMORY} of memory each</span>
		</div>
	);
}

function Group({ children, label, show }: { children: React.ReactNode; label: string; show: boolean }) {
	if (!show) return null;
	return (
		<div className="py-1">
			<p className="px-2 pb-1 font-mono text-[10px] uppercase tracking-wide text-passive">{label}</p>
			{children}
		</div>
	);
}

/**
 * One simulator: what it is, whether it is up, and the single thing that can be
 * done to it from here.
 */
function DeviceRow({
	bootedCount,
	confirming,
	device,
	onChoose,
	onConfirm,
	onPower,
	sessionId,
	watching,
}: {
	bootedCount: number;
	confirming: boolean;
	device: SimDevice;
	onChoose: () => void;
	onConfirm: (udid: string | null) => void;
	onPower: (request: SimPowerRequest) => void;
	sessionId: string;
	watching: boolean;
}) {
	const booted = device.state === "Booted";
	const power = device.power ?? null;
	const running = power?.state === "running";
	const holder = device.lease?.state === "held" ? (device.lease.holder ?? "") : "";
	const heldByOther = Boolean(holder) && holder !== sessionId;

	// Boot the first device with one press; ask before adding to a machine that
	// is already carrying one.
	const needsMemoryWarning = !booted && bootedCount > 0;

	const act = () => {
		if (booted) {
			onPower({ udid: device.udid, state: "shutdown", confirmHolder: heldByOther ? holder : undefined });
		} else {
			onPower({ udid: device.udid, state: "booted" });
		}
		onConfirm(null);
	};

	return (
		<div className={cn("rounded-md px-2 py-1.5", watching && "bg-overlay")} data-testid={`sim-device-${device.udid}`}>
			<div className="flex items-center gap-2">
				<button
					// A booted device is chosen by pressing it; a shut-down one has
					// nothing to look at yet, so its whole row leads to its Boot button
					// rather than pretending to be selectable.
					aria-label={booted ? `Watch ${device.name}` : `${device.name}, shut down`}
					className="flex min-w-0 flex-1 items-center gap-2 text-left disabled:cursor-default"
					disabled={!booted}
					onClick={onChoose}
					type="button"
				>
					<span
						aria-hidden
						className={cn("size-1.5 shrink-0 rounded-full", booted ? "bg-success" : "bg-passive")}
					/>
					<span className="min-w-0 flex-1">
						<span className="block truncate text-[12px] text-foreground">{device.name}</span>
						<span className="block truncate text-[11px] text-muted-foreground">
							{device.runtime}
							{watching ? " · watching" : ""}
							{heldByOther ? ` · leased by @${holder}` : ""}
						</span>
					</span>
				</button>

				{running ? (
					<InFlight power={power} />
				) : confirming ? null : (
					<button
						className={cn(
							"shrink-0 rounded-full border border-border px-2.5 py-1 text-[11px] font-medium transition-colors",
							"text-muted-foreground hover:bg-overlay hover:text-foreground",
						)}
						onClick={() => (booted || needsMemoryWarning ? onConfirm(device.udid) : act())}
						type="button"
					>
						{booted ? "Shut down" : "Boot"}
					</button>
				)}
			</div>

			{confirming && !running ? (
				<Confirm
					booted={booted}
					bootedCount={bootedCount}
					holder={heldByOther ? holder : ""}
					name={device.name}
					onCancel={() => onConfirm(null)}
					onConfirm={act}
				/>
			) : null}

			{power?.state === "failed" ? <Failure reason={power.reason ?? "It did not work, and said nothing."} /> : null}
		</div>
	);
}

/**
 * What a boot looks like while it is happening.
 *
 * The elapsed count is the real signal and the spinner is decoration, which is
 * why the count is text: under prefers-reduced-motion the spinner stops and the
 * control still visibly says it is working, and a screen reader gets the same
 * fact either way. The deadline is stated rather than implied - a wait with a
 * known end is a wait somebody can sit through.
 */
function InFlight({ power }: { power: PowerInfo }) {
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		const timer = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(timer);
	}, []);

	const started = Date.parse(power.startedAt);
	const seconds = Number.isNaN(started) ? 0 : Math.max(0, Math.round((now - started) / 1000));
	const elapsed = `${Math.floor(seconds / 60)}:${String(seconds % 60).padStart(2, "0")}`;

	return (
		<span
			aria-live="polite"
			className="flex shrink-0 items-center gap-1.5 text-[11px] text-muted-foreground"
			data-testid="sim-power-running"
			role="status"
		>
			<Loader2 aria-hidden className="size-3.5 animate-spin motion-reduce:animate-none" />
			<span className="font-mono tabular-nums">{elapsed}</span>
			<span className="sr-only">
				{power.op === "boot" ? "Booting" : "Shutting down"}, {elapsed} so far, giving up at {BOOT_TIMEOUT_LABEL}
			</span>
			<span aria-hidden className="text-passive">
				/ {BOOT_TIMEOUT_LABEL}
			</span>
		</span>
	);
}

/**
 * Why it did not work, in the machine's own words. It stays until the machine
 * makes it moot - a device that comes up later drops its own failure - because
 * the alternative is what this control replaced: a spinner that never resolves
 * and never explains.
 */
function Failure({ reason }: { reason: string }) {
	return (
		<p className="mt-1.5 flex gap-1.5 rounded-md bg-overlay px-2 py-1.5 text-[11px] leading-snug text-error">
			<AlertTriangle aria-hidden className="mt-0.5 size-3.5 shrink-0" />
			<span className="min-w-0">{reason}</span>
		</p>
	);
}

/**
 * The question asked before a device changes power state.
 *
 * Three different questions share this shape because they are the same
 * decision at different stakes: another 4 GB on a machine that has already
 * OOM'd, throwing away what is running on a device, and taking a device away
 * from a named session. The last one NAMES that session - and so does the
 * request, which the daemon refuses unless the name matches, so the naming is
 * a property of the protocol rather than of this component's politeness.
 */
function Confirm({
	booted,
	bootedCount,
	holder,
	name,
	onCancel,
	onConfirm,
}: {
	booted: boolean;
	bootedCount: number;
	holder: string;
	name: string;
	onCancel: () => void;
	onConfirm: () => void;
}) {
	let question: string;
	if (booted) {
		question = holder
			? `Shut ${name} down? @${holder} is leasing it, and loses the device and everything running on it.`
			: `Shut ${name} down? Everything running on it is lost.`;
	} else if (bootedCount >= 2) {
		question =
			`Boot ${name} as well? ${bootedCount} are already up. Three booted at once has run this machine out of ` +
			`memory before - tooling starts failing with "resource temporarily unavailable".`;
	} else {
		question = `Boot ${name} as well? One is already up, and each takes about 4 GB.`;
	}

	return (
		<div className="mt-1.5 rounded-md bg-overlay px-2 py-1.5" data-testid="sim-power-confirm">
			<p className={cn("text-[11px] leading-snug", bootedCount >= 2 && !booted ? "text-warning" : "text-foreground")}>
				{question}
			</p>
			<div className="mt-1.5 flex items-center gap-1.5">
				<button
					className="flex items-center gap-1 rounded-full border border-border bg-raised px-2.5 py-1 text-[11px] font-medium text-foreground transition-colors hover:bg-overlay"
					onClick={onConfirm}
					type="button"
				>
					<Power aria-hidden className="size-3" />
					{booted ? "Shut down" : "Boot anyway"}
				</button>
				<button
					className="rounded-full px-2.5 py-1 text-[11px] text-muted-foreground transition-colors hover:text-foreground"
					onClick={onCancel}
					type="button"
				>
					Cancel
				</button>
			</div>
		</div>
	);
}
