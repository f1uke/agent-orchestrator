import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { Hand, House, Layers, Loader2, RefreshCw } from "lucide-react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { simDevicesQueryKey, useSimDevices, type SimDevice } from "../hooks/useSimDevices";
import { usePageActive, useSimulatorStream } from "../hooks/useSimulatorStream";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";

/**
 * The Simulator tab: a booted iOS Simulator's screen, live, beside the session
 * driving it.
 *
 * Three things here are deliberate and load-bearing.
 *
 * Nothing runs unless this tab is being looked at. `active` is the tab being
 * shown AND the page being visible AND the window having focus; it gates both
 * the frame socket and the device list. Closing the socket is what stops the
 * capture process on the daemon side, so a hidden tab costs exactly nothing.
 *
 * The device is never guessed. With two simulators booted the daemon reports no
 * default and says why, and this panel asks rather than picking - the same
 * refusal `ao sim shot` makes, for the same reason.
 *
 * Driving is off until it has been earned. It requires this session to hold the
 * device lease, it is off again on every mount, and every gesture goes to the
 * daemon route that takes the same gesture hold `ao sim tap` takes. There is no
 * path from this panel to a device that skips arbitration.
 */

/**
 * emptyReason says what is actually true when there is no screen to show.
 *
 * The distinction matters: "no simulator is booted" is a claim about the
 * machine, and while the window is unfocused nothing has been asked, so making
 * that claim would state something AO never checked - the same mistake the
 * lease column made before it learned to say why a device reads as unknown.
 */
function emptyReason(watching: boolean, looked: boolean, bootedCount: number): string {
	if (!watching) return "Nothing is being captured while this window is not focused. Focus it to start watching.";
	if (!looked) return "Looking for booted simulators…";
	if (bootedCount === 0) {
		return "No simulator is booted. AO never boots, shuts down or erases one - start it from Xcode or Simulator.app.";
	}
	return "Choose which booted simulator to watch.";
}

/** A drag shorter than this is a tap, not a swipe. */
const SWIPE_THRESHOLD_PX = 8;

type GestureBody =
	| { kind: "tap"; x: number; y: number }
	| { kind: "swipe"; x: number; y: number; toX: number; toY: number; durationMs: number }
	| { kind: "type"; text: string }
	| { kind: "button"; name: string };

export function SimulatorPanel({
	sessionId,
	/** Whether the Simulator tab is the one on screen. */
	isActive,
}: {
	sessionId: string;
	isActive: boolean;
}) {
	const pageActive = usePageActive();
	const watching = isActive && pageActive;

	const devices = useSimDevices(watching);
	const [chosen, setChosen] = useState<string | null>(null);
	// Driving is never sticky. A remembered "on" would let a click land on a
	// device the human forgot they had left drivable.
	const [driving, setDriving] = useState(false);
	const [typed, setTyped] = useState("");
	const [problem, setProblem] = useState("");
	const canvasRef = useRef<HTMLCanvasElement | null>(null);
	const queryClient = useQueryClient();

	const booted = useMemo(() => (devices.data?.devices ?? []).filter((d) => d.state === "Booted"), [devices.data]);
	const defaultUdid = devices.data?.defaultUdid ?? null;

	// Preselect only what the daemon was willing to resolve. With several booted
	// it hands back null, and null is what the picker shows.
	useEffect(() => {
		if (chosen && booted.some((d) => d.udid === chosen)) return;
		setChosen(defaultUdid && booted.some((d) => d.udid === defaultUdid) ? defaultUdid : null);
	}, [chosen, booted, defaultUdid]);

	const device = booted.find((d) => d.udid === chosen) ?? null;
	const lease = device?.lease;
	const heldByThisSession = lease?.state === "held" && lease.holder === sessionId;
	const heldByOther = lease?.state === "held" && lease.holder !== sessionId;

	// Switching device or losing the lease has to switch driving back off, not
	// leave a toggle that is on for a device it no longer applies to.
	useEffect(() => {
		if (!heldByThisSession) setDriving(false);
	}, [heldByThisSession, chosen]);

	// `active` is the only gate, and it is passed whole rather than also being
	// folded into the udid: two guards for one rule means a mutation can break
	// one of them and every test still passes, which is exactly what happened
	// the first time this was written.
	const stream = useSimulatorStream({ udid: chosen, active: watching, canvasRef });

	const refreshDevices = useCallback(() => {
		void queryClient.invalidateQueries({ queryKey: simDevicesQueryKey });
	}, [queryClient]);

	const claim = useMutation({
		mutationFn: async (udid: string) => {
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/sim-leases", {
				params: { path: { sessionId } },
				body: { udid },
			});
			if (error) throw error;
		},
		onSuccess: refreshDevices,
		onError: (error) => setProblem(apiErrorMessage(error, "Could not claim the simulator")),
	});

	const release = useMutation({
		mutationFn: async (udid: string) => {
			const { error } = await apiClient.DELETE("/api/v1/sessions/{sessionId}/sim-leases/{udid}", {
				params: { path: { sessionId, udid } },
			});
			if (error) throw error;
		},
		onSuccess: refreshDevices,
		onError: (error) => setProblem(apiErrorMessage(error, "Could not release the simulator")),
	});

	const gesture = useMutation({
		mutationFn: async (body: GestureBody) => {
			if (!chosen) throw new Error("No simulator is selected");
			const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture", {
				params: { path: { sessionId, udid: chosen } },
				body,
			});
			if (error) throw error;
		},
		onMutate: () => setProblem(""),
		onError: (error) => setProblem(apiErrorMessage(error, "The gesture did not reach the device")),
		onSettled: refreshDevices,
	});

	// The device has no queue: two overlapping gestures merge into one touch.
	// Refusing a second here means the human sees "wait" rather than a 409.
	const busy = gesture.isPending;
	const canDrive = driving && heldByThisSession && stream.state === "live";

	const pressed = useRef<{ x: number; y: number; at: number } | null>(null);

	const pointFor = (event: React.PointerEvent<HTMLCanvasElement>) => {
		const rect = event.currentTarget.getBoundingClientRect();
		if (rect.width === 0 || rect.height === 0) return null;
		// Normalized 0..1 is what the HID layer takes and what `ao sim ax` reports
		// per element, so nothing in between needs the device's pixel size.
		return {
			x: Math.min(1, Math.max(0, (event.clientX - rect.left) / rect.width)),
			y: Math.min(1, Math.max(0, (event.clientY - rect.top) / rect.height)),
			clientX: event.clientX,
			clientY: event.clientY,
		};
	};

	const onPointerDown = (event: React.PointerEvent<HTMLCanvasElement>) => {
		if (!canDrive || busy) return;
		const point = pointFor(event);
		if (!point) return;
		event.currentTarget.setPointerCapture(event.pointerId);
		pressed.current = { x: point.x, y: point.y, at: Date.now() };
	};

	const onPointerUp = (event: React.PointerEvent<HTMLCanvasElement>) => {
		const start = pressed.current;
		pressed.current = null;
		if (!canDrive || busy || !start) return;
		const point = pointFor(event);
		if (!point) return;
		const rect = event.currentTarget.getBoundingClientRect();
		const movedPx = Math.hypot((point.x - start.x) * rect.width, (point.y - start.y) * rect.height);
		if (movedPx < SWIPE_THRESHOLD_PX) {
			gesture.mutate({ kind: "tap", x: start.x, y: start.y });
			return;
		}
		gesture.mutate({
			kind: "swipe",
			x: start.x,
			y: start.y,
			toX: point.x,
			toY: point.y,
			// The real drag time, so a slow drag scrolls and a flick flicks.
			durationMs: Math.min(3_000, Math.max(80, Date.now() - start.at)),
		});
	};

	const aspect = stream.size ? `${stream.size.width} / ${stream.size.height}` : "9 / 19.5";

	return (
		<div className="flex h-full min-h-0 flex-col gap-3 p-3">
			<DevicePicker
				booted={booted}
				chosen={chosen}
				loading={devices.isPending && watching}
				onChoose={setChosen}
				onRefresh={refreshDevices}
				reason={devices.data?.defaultReason ?? ""}
			/>

			<Freshness
				chosen={Boolean(chosen)}
				lastFrameAt={stream.lastFrameAt}
				message={stream.message}
				state={stream.state}
				watching={watching}
			/>

			<div className="flex min-h-0 flex-1 items-center justify-center overflow-hidden rounded-lg border border-border bg-black/40">
				{chosen ? (
					<canvas
						ref={canvasRef}
						aria-label="Live simulator screen"
						data-testid="sim-canvas"
						className={cn(
							"h-full max-h-full w-auto max-w-full object-contain",
							canDrive ? "cursor-crosshair" : "cursor-default",
						)}
						style={{ aspectRatio: aspect }}
						onPointerDown={onPointerDown}
						onPointerUp={onPointerUp}
					/>
				) : (
					<p className="px-6 text-center text-[12px] text-muted-foreground">
						{emptyReason(watching, devices.isSuccess, booted.length)}
					</p>
				)}
			</div>

			<LeaseLine
				busy={claim.isPending || release.isPending}
				device={device}
				heldByThisSession={heldByThisSession}
				heldByOther={Boolean(heldByOther)}
				onClaim={() => device && claim.mutate(device.udid)}
				onRelease={() => device && release.mutate(device.udid)}
				sessionId={sessionId}
			/>

			{heldByThisSession ? (
				<div className="flex flex-col gap-2">
					<label className="flex items-center gap-2 text-[12px] text-foreground">
						<input
							type="checkbox"
							className="h-4 w-4 accent-accent"
							checked={driving}
							onChange={(e) => setDriving(e.target.checked)}
						/>
						<Hand aria-hidden className="h-3.5 w-3.5 text-passive" />
						Drive this device as @{sessionId}
					</label>
					{driving ? (
						<>
							<p className="text-[11px] text-passive">
								Click to tap, drag to swipe. Every gesture takes the same gesture hold <code>ao sim tap</code> takes, so
								it waits for nobody and is refused if this session's agent is mid-gesture.
							</p>
							<div className="flex flex-wrap items-center gap-2">
								<Button
									disabled={busy}
									onClick={() => gesture.mutate({ kind: "button", name: "home" })}
									size="sm"
									variant="outline"
								>
									<House aria-hidden className="h-3.5 w-3.5" /> Home
								</Button>
								<Button
									disabled={busy}
									onClick={() => gesture.mutate({ kind: "button", name: "app-switcher" })}
									size="sm"
									variant="outline"
								>
									<Layers aria-hidden className="h-3.5 w-3.5" /> App switcher
								</Button>
							</div>
							<form
								className="flex items-center gap-2"
								onSubmit={(e) => {
									e.preventDefault();
									if (!typed || busy) return;
									gesture.mutate({ kind: "type", text: typed });
									setTyped("");
								}}
							>
								<input
									aria-label="Text to type into the focused field"
									className="min-w-0 flex-1 rounded-md border border-border bg-raised px-2 py-1 text-[12px] text-foreground"
									onChange={(e) => setTyped(e.target.value)}
									placeholder="Type into the focused field…"
									value={typed}
								/>
								<Button disabled={!typed || busy} size="sm" type="submit" variant="outline">
									Send
								</Button>
							</form>
							<p className="text-[11px] text-passive">
								Keys are sent as US-keyboard presses; the simulator's own input source decides what characters appear.
								Read the field back rather than assuming.
							</p>
						</>
					) : null}
				</div>
			) : null}

			{problem ? <p className="text-[11px] text-error">{problem}</p> : null}
			{busy ? (
				<p className="flex items-center gap-1.5 text-[11px] text-passive">
					<Loader2 aria-hidden className="h-3 w-3 animate-spin" /> Sending the gesture…
				</p>
			) : null}
		</div>
	);
}

function DevicePicker({
	booted,
	chosen,
	loading,
	onChoose,
	onRefresh,
	reason,
}: {
	booted: SimDevice[];
	chosen: string | null;
	loading: boolean;
	onChoose: (udid: string) => void;
	onRefresh: () => void;
	reason: string;
}) {
	return (
		<div className="flex flex-col gap-1.5">
			<div className="flex items-center gap-2">
				<Select disabled={booted.length === 0} onValueChange={onChoose} value={chosen ?? ""}>
					<SelectTrigger aria-label="Simulator to watch" className="h-8 flex-1 text-[12px]" size="sm">
						<SelectValue placeholder={loading ? "Looking for simulators…" : "Choose a booted simulator"} />
					</SelectTrigger>
					<SelectContent>
						{booted.map((device) => (
							<SelectItem key={device.udid} value={device.udid}>
								{device.name} · {device.runtime}
							</SelectItem>
						))}
					</SelectContent>
				</Select>
				<Button aria-label="Refresh simulators" onClick={onRefresh} size="sm" variant="ghost">
					<RefreshCw aria-hidden className="h-3.5 w-3.5" />
				</Button>
			</div>
			{/* With several booted there is no default, and saying why beats a
			    silently pre-picked device the human did not choose. */}
			{!chosen && booted.length > 1 && reason ? <p className="text-[11px] text-passive">{reason}.</p> : null}
		</div>
	);
}

// Measured, not eyeballed: `text-passive` is the repo's de-emphasised helper
// tone and comes out at 3.61:1 on dark and 2.74:1 on light - below WCAG AA for
// body text. That is the right weight for the guidance paragraphs below, and
// the wrong weight for the one line that says whether what you are looking at
// is live. `text-muted-foreground` measures 7.57:1 and 5.60:1.
function Freshness({
	state,
	message,
	lastFrameAt,
	watching,
	chosen,
}: {
	state: string;
	message: string;
	lastFrameAt: number | null;
	watching: boolean;
	chosen: boolean;
}) {
	// The age of the newest frame is the honest freshness signal, and it keeps
	// counting up while the screen is still - which is what "nothing changed"
	// looks like when repeats are not forwarded.
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (state !== "live") return;
		const timer = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(timer);
	}, [state]);

	const age = lastFrameAt ? Math.max(0, Math.round((now - lastFrameAt) / 1000)) : null;
	const tone =
		state === "live"
			? "bg-success"
			: state === "connecting"
				? "bg-warning"
				: state === "ended"
					? "bg-error"
					: "bg-passive";

	let label: string;
	if (!watching) label = "Paused - this window is not focused, so nothing is being captured";
	else if (!chosen) label = "Choose a simulator above to start watching";
	else if (state === "connecting") label = "Connecting to the device…";
	else if (state === "ended") label = message || "The stream ended";
	else if (state === "live") label = age === null ? "Live" : age < 2 ? "Live" : `Live - screen unchanged for ${age}s`;
	else label = "Not watching";

	return (
		<p className="flex items-center gap-2 text-[11px] text-muted-foreground" data-testid="sim-freshness">
			<span aria-hidden className={cn("h-1.5 w-1.5 shrink-0 rounded-full", tone)} />
			{label}
		</p>
	);
}

function LeaseLine({
	busy,
	device,
	heldByThisSession,
	heldByOther,
	onClaim,
	onRelease,
	sessionId,
}: {
	busy: boolean;
	device: SimDevice | null;
	heldByThisSession: boolean;
	heldByOther: boolean;
	onClaim: () => void;
	onRelease: () => void;
	sessionId: string;
}) {
	if (!device) return null;
	const lease = device.lease;

	return (
		<div className="flex flex-wrap items-center gap-2 text-[11px]">
			{heldByThisSession ? (
				<>
					<span className="text-success">Leased by this session (@{sessionId})</span>
					<Button disabled={busy} onClick={onRelease} size="sm" variant="ghost">
						Release
					</Button>
				</>
			) : heldByOther ? (
				<span className="text-warning">
					Leased by @{lease?.holder}. Watching is always allowed; driving is not, until they release it.
				</span>
			) : (
				<>
					{/* Never "free": AO knows its own leases and nothing else. */}
					<span className="text-passive">Lease: unknown - {lease?.reason}</span>
					<Button disabled={busy} onClick={onClaim} size="sm" variant="outline">
						Claim to drive
					</Button>
				</>
			)}
		</div>
	);
}
