import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useMutation, useQueryClient } from "@tanstack/react-query";
import { House, Layers, MoreHorizontal, MousePointer2 } from "lucide-react";
import { apiClient, apiErrorMessage } from "../lib/api-client";
import { simDevicesQueryKey, useSimDevices, type SimDevice } from "../hooks/useSimDevices";
import { usePageActive, useSimulatorStream, type SimStreamStatus } from "../hooks/useSimulatorStream";
import { DragStream } from "../lib/drag-stream";
import { devicePoint, fitDevice } from "../lib/screen-fit";
import { cn } from "../lib/utils";
import {
	DropdownMenu,
	DropdownMenuContent,
	DropdownMenuItem,
	DropdownMenuSeparator,
	DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { SimpleTooltip, TooltipProvider } from "./ui/tooltip";

/**
 * The Device tab: a booted iOS Simulator's screen, live, beside the session
 * driving it.
 *
 * The screen is the panel. Everything else is one toolbar row that wraps to two
 * at the rail's narrowest, because a pane that spends half its height on a
 * device picker, a freshness line, a lease row, a checkbox and two paragraphs
 * of guidance is not a live view of anything - which is what the first version
 * of this panel was. The guidance did not disappear: the gesture caveat lives
 * on the control it applies to, and the lease's own words are one menu away
 * rather than permanently under the screen.
 *
 * There is no text field. `ao sim type` still exists and the daemon route still
 * takes a type gesture, but the panel does not offer one: it was a field, a
 * button and a paragraph of caveat spent on something a person watching a screen
 * does not reach for, and the human asked for it back as pane.
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
function emptyReason(watching: boolean, looked: boolean, bootedCount: number, defaultReason: string): string {
	if (!watching) return "Nothing is being captured while this window is not focused. Focus it to start watching.";
	if (!looked) return "Looking for booted simulators…";
	if (bootedCount === 0) {
		return "No simulator is booted. AO never boots, shuts down or erases one - start it from Xcode or Simulator.app.";
	}
	// With several booted there is no default, and the daemon's own words for
	// why beat a picker that quietly chose one the human did not.
	if (bootedCount > 1 && defaultReason) return `Choose which booted simulator to watch: ${defaultReason}.`;
	return "Choose which booted simulator to watch.";
}

/** A press that moves less than this is a tap, not a drag. */
const DRAG_THRESHOLD_PX = 8;

/**
 * What each session was last doing with a simulator, so coming back to a worker
 * puts you where you left off instead of asking again.
 *
 * The panel is keyed by session and remounts when you switch between them, so
 * without this every trip back meant picking the device again and opting in to
 * driving again.
 *
 * In session storage rather than a module variable or local storage, because
 * that is exactly the lifetime this wants: it survives switching workers and a
 * reload, and it is gone when the window is. A remembered "driving" that came
 * back days later would be a setting nobody chose.
 *
 * Remembering that driving was on does NOT re-grant it. The lease still does:
 * the effect below switches driving off the moment this session is not the
 * holder, so what comes back is only ever a device this session still owns.
 */
type Remembered = { udid: string | null; driving: boolean };

function rememberedKey(sessionId: string): string {
	return `ao.sim.lastUsed.${sessionId}`;
}

function recall(sessionId: string): Remembered | null {
	try {
		const raw = sessionStorage.getItem(rememberedKey(sessionId));
		if (!raw) return null;
		const parsed = JSON.parse(raw) as Partial<Remembered>;
		return { udid: typeof parsed.udid === "string" ? parsed.udid : null, driving: parsed.driving === true };
	} catch {
		// Storage a build or a policy took away is a reason to ask again, not to
		// fail to show a screen.
		return null;
	}
}

function remember(sessionId: string, value: Remembered): void {
	try {
		sessionStorage.setItem(rememberedKey(sessionId), JSON.stringify(value));
	} catch {
		// As above: forgetting is survivable.
	}
}

/** Beyond this long without a frame, a stream that says it is live is not. */
const STALE_AFTER_MS = 2_000;

// What this panel sends one-shot. A drag is not here: it is several requests
// under one hold, and lives in DragStream.
type GestureBody = { kind: "tap"; x: number; y: number } | { kind: "button"; name: string };

export function SimulatorPanel({
	sessionId,
	/** Whether the Device tab is the one on screen. */
	isActive,
}: {
	sessionId: string;
	isActive: boolean;
}) {
	const pageActive = usePageActive();
	const watching = isActive && pageActive;

	const devices = useSimDevices(watching);
	const [chosen, setChosen] = useState<string | null>(() => recall(sessionId)?.udid ?? null);
	const [driving, setDriving] = useState(() => recall(sessionId)?.driving ?? false);
	const [problem, setProblem] = useState("");
	const canvasRef = useRef<HTMLCanvasElement | null>(null);
	const queryClient = useQueryClient();

	const booted = useMemo(() => (devices.data?.devices ?? []).filter((d) => d.state === "Booted"), [devices.data]);
	const defaultUdid = devices.data?.defaultUdid ?? null;

	// Preselect only what the daemon was willing to resolve, or what this session
	// was last watching. With several booted the daemon hands back null, and null
	// is what the picker shows - remembering a choice the human made is not the
	// same as guessing one they did not.
	useEffect(() => {
		if (chosen && booted.some((d) => d.udid === chosen)) return;
		if (booted.length === 0 && !devices.isSuccess) return;
		setChosen(defaultUdid && booted.some((d) => d.udid === defaultUdid) ? defaultUdid : null);
	}, [chosen, booted, defaultUdid, devices.isSuccess]);

	const device = booted.find((d) => d.udid === chosen) ?? null;
	const lease = device?.lease;
	const heldByThisSession = lease?.state === "held" && lease.holder === sessionId;
	const heldByOther = lease?.state === "held" && lease.holder !== sessionId;

	// Losing the lease switches driving back off. Deliberately conditional on
	// the device being known: on a remount the lease state has not arrived yet,
	// and clearing on "not held" before anyone has looked would wipe what this
	// session was doing a moment ago rather than reflect anything true.
	useEffect(() => {
		if (!chosen) {
			setDriving(false);
			return;
		}
		if (device && !heldByThisSession) setDriving(false);
	}, [chosen, device, heldByThisSession]);

	// Remembered per session, not per mount.
	useEffect(() => {
		remember(sessionId, { udid: chosen, driving });
	}, [sessionId, chosen, driving]);

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
	// `driving` is the whole permission, not half of it. The effect above is
	// what keeps it true only while this session holds the lease, and re-testing
	// the lease here as well would be the same rule enforced twice - which is
	// how the last version of this panel got a mutation through: break one copy
	// and the other still refuses, so no test notices either is gone.
	//
	// The stream only has to be one that could still be showing the truth, not
	// one that is mid-frame. Requiring "live" meant every press in the few
	// hundred milliseconds after the socket was rebuilt was dropped - and the
	// socket is rebuilt whenever the window regains focus, which is precisely
	// when a human who just clicked into the app tries to drag. A stream that
	// has actually ended is a different thing: the picture will never update
	// again, so driving on it would be driving blind.
	const canDrive = driving && stream.state !== "ended" && stream.state !== "unsupported";

	const pressed = useRef<{ x: number; y: number; at: number } | null>(null);

	// A drag is streamed while the finger is still down rather than replayed
	// after it comes up. Sending one swipe on release - which is what this did -
	// means the screen starts moving after the human has stopped, which is the
	// lag they reported against serve-sim, where the content tracks the finger.
	const drag = useRef<DragStream | null>(null);
	if (!drag.current) {
		drag.current = new DragStream(
			async (step, point) => {
				if (!chosenRef.current) throw new Error("No simulator is selected");
				const { error } = await apiClient.POST("/api/v1/sessions/{sessionId}/sim-devices/{udid}/gesture", {
					params: { path: { sessionId, udid: chosenRef.current } },
					body: { kind: step, x: point.x, y: point.y },
				});
				if (error) throw error;
			},
			(error) => setProblem(apiErrorMessage(error, "The drag did not reach the device")),
		);
	}
	// The sender is built once and outlives any particular device, so it reads
	// the current one rather than closing over the one chosen when it was made.
	const chosenRef = useRef(chosen);
	chosenRef.current = chosen;

	// The canvas fills the pane and letterboxes the picture inside it, so a
	// pointer position is only a device coordinate after it has been mapped
	// through that letterbox - and a press in the bars is not on the device.
	const pointFor = (event: React.PointerEvent<HTMLCanvasElement>) => {
		const rect = event.currentTarget.getBoundingClientRect();
		const frame = stream.size;
		if (!frame) return null;
		return devicePoint({ width: rect.width, height: rect.height }, frame, {
			x: event.clientX - rect.left,
			y: event.clientY - rect.top,
		});
	};

	/** Pixels moved since the press, which is what tells a tap from a drag. */
	const movedPx = (event: React.PointerEvent<HTMLCanvasElement>, point: { x: number; y: number }) => {
		const start = pressed.current;
		if (!start) return 0;
		const rect = event.currentTarget.getBoundingClientRect();
		return Math.hypot((point.x - start.x) * rect.width, (point.y - start.y) * rect.height);
	};

	// Deliberately not gated on `busy`. Refusing the press while some other
	// gesture is still in flight made a drag vanish with no sign it had been
	// asked for - which is what "sometimes I have to do it twice" was. The
	// device's own arbitration is what may refuse this, and when it does it says
	// so; silence here said nothing.
	const onPointerDown = (event: React.PointerEvent<HTMLCanvasElement>) => {
		if (!canDrive) return;
		const point = pointFor(event);
		if (!point) return;
		event.currentTarget.setPointerCapture(event.pointerId);
		// Nothing is sent yet: a press that never moves is a tap, and a tap holds
		// the finger down for a measured moment that a drag's begin does not.
		pressed.current = { x: point.x, y: point.y, at: Date.now() };
	};

	// A pointer capture the browser takes back - a window switch, a gesture the
	// OS claimed - ends the touch here too. Without it this side believes a
	// finger is still down and ignores every drag after it.
	const onLostPointerCapture = () => {
		pressed.current = null;
		const held = drag.current;
		if (held?.isDragging) held.end({ x: 0.5, y: 0.5 });
	};

	const onPointerMove = (event: React.PointerEvent<HTMLCanvasElement>) => {
		const start = pressed.current;
		if (!start || !canDrive) return;
		const point = pointFor(event);
		if (!point) return;
		if (drag.current?.isDragging) {
			drag.current.move(point);
			return;
		}
		// The touch goes down where the press was, not where the pointer has got
		// to, so the drag starts from the point the human actually aimed at.
		if (movedPx(event, point) >= DRAG_THRESHOLD_PX) {
			drag.current?.begin({ x: start.x, y: start.y });
			drag.current?.move(point);
		}
	};

	const onPointerUp = (event: React.PointerEvent<HTMLCanvasElement>) => {
		const start = pressed.current;
		pressed.current = null;
		const point = pointFor(event);
		if (drag.current?.isDragging) {
			drag.current.end(point ?? { x: start?.x ?? 0.5, y: start?.y ?? 0.5 });
			return;
		}
		if (!canDrive || busy || !start || !point) return;
		gesture.mutate({ kind: "tap", x: start.x, y: start.y });
	};

	// A pointer the browser took away (a window switch, a gesture the OS
	// claimed) has to end the touch too, or the finger stays down until the
	// daemon's watchdog lifts it.
	const onPointerCancel = () => {
		pressed.current = null;
		const held = drag.current;
		if (held?.isDragging) held.end({ x: 0.5, y: 0.5 });
	};

	// Leaving the pane, or losing the lease, must not leave a finger down.
	useEffect(() => {
		if (canDrive) return;
		const held = drag.current;
		if (held?.isDragging) held.cancel();
	}, [canDrive]);

	const stageRef = useRef<HTMLDivElement | null>(null);
	const stage = useStageSize(stageRef);
	const drawn = fitDevice(stage, stream.size, device?.frame ?? null);

	return (
		<TooltipProvider delayDuration={200}>
			{/* Device centred, chrome floating clear of it. The ground is the app's
			    own, not pure black: a black panel inside a near-black app reads as a
			    hole rather than a surface, and it forced every label on it to use
			    fixed colours that could not follow the theme. */}
			<div className="relative flex h-full min-h-0 flex-col items-center gap-2 overflow-hidden bg-background py-2">
				<DevicePill
					booted={booted}
					chosen={chosen}
					loading={devices.isPending && watching}
					onChoose={setChosen}
					status={stream}
					watching={watching}
				/>

				<div
					className="relative flex min-h-0 w-full flex-1 items-center justify-center"
					data-testid="sim-stage"
					ref={stageRef}
				>
					{chosen ? (
						<div
							className={cn(
								"relative max-h-full max-w-full",
								drawn && drawn.bezel > 0 ? "bg-neutral-900 shadow-[0_18px_50px_-20px_rgba(0,0,0,0.8)]" : "",
							)}
							style={
								drawn
									? {
											width: drawn.screen.width + drawn.bezel * 2,
											height: drawn.screen.height + drawn.bezel * 2,
											padding: drawn.bezel,
											borderRadius: drawn.outerRadius,
										}
									: undefined
							}
						>
							<canvas
								ref={canvasRef}
								aria-label="Live simulator screen"
								data-testid="sim-canvas"
								// The box is sized to the picture, so `object-contain` has
								// nothing to letterbox in the ordinary case - it is the safety
								// net for the frame before the stage has been measured, and
								// `pointFor` maps a press through the same fit either way.
								className={cn("block h-full w-full object-contain", canDrive ? "cursor-crosshair" : "cursor-default")}
								style={{ borderRadius: drawn?.radius ?? 0 }}
								onLostPointerCapture={onLostPointerCapture}
								onPointerCancel={onPointerCancel}
								onPointerDown={onPointerDown}
								onPointerMove={onPointerMove}
								onPointerUp={onPointerUp}
							/>
						</div>
					) : (
						<p className="max-w-[36ch] px-4 text-center text-[12px] text-muted-foreground">
							{emptyReason(watching, devices.isSuccess, booted.length, devices.data?.defaultReason ?? "")}
						</p>
					)}

					{/* A stream that stopped has to say so over the last frame rather
					    than leave it to be mistaken for a live one. */}
					{chosen && (stream.state === "ended" || stream.state === "unsupported") ? (
						<p className="absolute inset-x-2 bottom-2 rounded-md bg-black/80 px-3 py-2 text-center text-[11px] text-white/85">
							{stream.message || "The stream ended."}
						</p>
					) : null}

					{problem ? (
						<p className="absolute inset-x-2 top-2 rounded-md bg-black/80 px-3 py-2 text-center text-[11px] text-error">
							{problem}
						</p>
					) : null}
				</div>

				<div className="flex shrink-0 flex-wrap items-center justify-center gap-2">
					{/* Always the same size. The row used to grow and shrink as driving
					    was switched on and off, which moved the screen under the
					    human's pointer; the buttons are simply disabled until a touch
					    is allowed to reach the device. Disabled is enforcement, not
					    decoration: nothing here can fire while driving is off. */}
					<div className="flex items-center gap-0.5 rounded-full border border-border bg-raised p-1">
						<PillButton
							disabled={!driving || busy}
							icon={House}
							label="Home"
							onClick={() => gesture.mutate({ kind: "button", name: "home" })}
						/>
						<PillButton
							disabled={!driving || busy}
							icon={Layers}
							label="App switcher"
							onClick={() => gesture.mutate({ kind: "button", name: "app-switcher" })}
						/>
						<DeviceMenu
							busy={claim.isPending || release.isPending}
							device={device}
							heldByOther={Boolean(heldByOther)}
							heldByThisSession={heldByThisSession}
							onClaim={() => device && claim.mutate(device.udid)}
							onRefresh={refreshDevices}
							onRelease={() => device && release.mutate(device.udid)}
							sessionId={sessionId}
						/>
					</div>

					{heldByThisSession ? (
						<SimpleTooltip
							label={
								<span className="block max-w-[220px]">
									Click to tap, drag to swipe. Every gesture takes the same gesture hold <code>ao sim tap</code> takes,
									so it waits for nobody and is refused if this session's agent is mid-gesture.
								</span>
							}
						>
							{/* serve-sim's own pointer pill, and the lease made visible: it is
							    only here when this session holds the device, and pressed is a
							    filled ground plus a ring rather than colour alone. */}
							<button
								aria-label={`Drive this device as @${sessionId}`}
								aria-pressed={driving}
								// Pressed is a filled accent with its own foreground token, plus
								// a border: a state carried by more than colour alone survives
								// being colour-blind, and aria-pressed carries it to a reader.
								className={cn(
									"flex h-9 w-9 items-center justify-center rounded-full border transition-colors",
									driving
										? "border-accent bg-accent text-accent-foreground"
										: "border-border bg-raised text-muted-foreground hover:text-foreground",
								)}
								onClick={() => setDriving((on) => !on)}
								type="button"
							>
								<MousePointer2 aria-hidden className="h-4 w-4" />
							</button>
						</SimpleTooltip>
					) : null}
				</div>
			</div>
		</TooltipProvider>
	);
}

/**
 * useStageSize measures the space the screen may have.
 *
 * Sizing in JS rather than CSS because no CSS rule both fills the space and
 * keeps the shape for every framebuffer: `max-width/max-height` refuses to
 * scale a small screen (a watch) up, and `width/height: 100%` with
 * `object-fit` fills the box but letterboxes inside it, which puts the
 * picture's edges somewhere other than the element's. The arithmetic itself
 * lives in `screen-fit`, where it is tested without a layout engine.
 */
function useStageSize(stageRef: React.RefObject<HTMLDivElement | null>): { width: number; height: number } | null {
	const [stage, setStage] = useState<{ width: number; height: number } | null>(null);
	useEffect(() => {
		const el = stageRef.current;
		// jsdom has no ResizeObserver, and a pane with no measurement falls back
		// to filling its box - which is what the tests see and what a browser
		// shows for the one frame before the observer first fires.
		if (!el || typeof ResizeObserver === "undefined") return;
		const observer = new ResizeObserver((entries) => {
			const rect = entries[0]?.contentRect;
			if (rect) setStage({ width: rect.width, height: rect.height });
		});
		observer.observe(el);
		return () => observer.disconnect();
	}, [stageRef]);
	return stage;
}

function PillButton({
	disabled,
	icon: Icon,
	label,
	onClick,
}: {
	disabled?: boolean;
	icon: typeof House;
	label: string;
	onClick: () => void;
}) {
	return (
		<SimpleTooltip label={label}>
			<button
				aria-label={label}
				className="flex h-7 w-7 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-overlay hover:text-foreground disabled:opacity-40 disabled:hover:bg-transparent"
				disabled={disabled}
				onClick={onClick}
				type="button"
			>
				<Icon aria-hidden className="h-4 w-4" />
			</button>
		</SimpleTooltip>
	);
}

/**
 * The pill above the screen: which device this is, and whether what you are
 * looking at is arriving. serve-sim's own header, with the one thing it does
 * not need - a choice of device, because AO refuses to guess when several are
 * booted.
 */
function DevicePill({
	booted,
	chosen,
	loading,
	onChoose,
	status,
	watching,
}: {
	booted: SimDevice[];
	chosen: string | null;
	loading: boolean;
	onChoose: (udid: string) => void;
	status: SimStreamStatus;
	watching: boolean;
}) {
	return (
		<div className="flex shrink-0 items-center gap-1 rounded-full border border-border bg-raised py-0.5 pl-1 pr-2.5">
			<Select disabled={booted.length === 0} onValueChange={onChoose} value={chosen ?? ""}>
				<SelectTrigger
					aria-label="Simulator to watch"
					className="h-7 max-w-[190px] border-0 bg-transparent px-2 text-[12px] font-medium text-foreground shadow-none focus:ring-0"
					size="sm"
				>
					<SelectValue placeholder={loading ? "Looking for simulators…" : "Choose a simulator"} />
				</SelectTrigger>
				<SelectContent>
					{booted.map((device) => (
						<SelectItem key={device.udid} value={device.udid}>
							{device.name} · {device.runtime}
						</SelectItem>
					))}
				</SelectContent>
			</Select>
			<Freshness chosen={Boolean(chosen)} status={status} watching={watching} />
		</div>
	);
}

// Measured, not eyeballed: `text-passive` is the repo's de-emphasised helper
// tone and comes out at 3.61:1 on dark and 2.74:1 on light - below WCAG AA for
// body text. That is the right weight for the guidance inside a popover, and
// the wrong weight for the one line that says whether what you are looking at
// is live. `text-muted-foreground` measures 7.57:1 and 5.60:1.
function Freshness({ status, watching, chosen }: { status: SimStreamStatus; watching: boolean; chosen: boolean }) {
	// The capture engine re-emits at five frames a second even on a still
	// screen, so frames arriving is what "live" means and a gap in them is a
	// real signal rather than a screen that happens not to be moving.
	const [now, setNow] = useState(() => Date.now());
	useEffect(() => {
		if (status.state !== "live") return;
		const timer = window.setInterval(() => setNow(Date.now()), 1_000);
		return () => window.clearInterval(timer);
	}, [status.state]);

	const age = status.lastFrameAt ? Math.max(0, now - status.lastFrameAt) : null;
	const stalled = status.state === "live" && age !== null && age > STALE_AFTER_MS;

	let label: string;
	let tone: string;
	let text: string;
	let title: string;
	if (!watching) {
		label = "paused";
		tone = "bg-passive";
		text = "text-muted-foreground";
		title = "This window is not focused, so nothing is being captured.";
	} else if (!chosen) {
		label = "idle";
		tone = "bg-passive";
		text = "text-muted-foreground";
		title = "Choose a simulator to start watching.";
	} else if (status.state === "connecting") {
		label = "connecting";
		tone = "bg-warning";
		text = "text-muted-foreground";
		title = "Connecting to the device…";
	} else if (status.state === "ended" || status.state === "unsupported") {
		label = "ended";
		tone = "bg-error";
		text = "text-error";
		title = status.message || "The stream ended.";
	} else if (stalled) {
		label = `no frames ${Math.round((age ?? 0) / 1000)}s`;
		tone = "bg-warning";
		text = "text-muted-foreground";
		title = "The device has sent no frames for a while, even though the stream is open.";
	} else if (status.state === "live") {
		label = "live";
		tone = "bg-success";
		text = "text-success-strong";
		title = "Frames are arriving from the device.";
	} else {
		label = "idle";
		tone = "bg-passive";
		text = "text-muted-foreground";
		title = "Nothing is being captured.";
	}

	return (
		<SimpleTooltip label={title}>
			<span
				className={cn("flex shrink-0 items-center gap-1.5 font-mono text-[11px] lowercase", text)}
				data-testid="sim-freshness"
			>
				<span aria-hidden className={cn("h-1.5 w-1.5 shrink-0 rounded-full", tone)} />
				{label}
			</span>
		</SimpleTooltip>
	);
}

/**
 * The lease, in the only place it needs to be: one menu, always reachable, and
 * never claiming to know more than AO does. AO knows its own leases and nothing
 * else, so a device it does not hold reads as unknown with the reason - never
 * as free.
 */
function DeviceMenu({
	busy,
	device,
	heldByOther,
	heldByThisSession,
	onClaim,
	onRefresh,
	onRelease,
	sessionId,
}: {
	busy: boolean;
	device: SimDevice | null;
	heldByOther: boolean;
	heldByThisSession: boolean;
	onClaim: () => void;
	onRefresh: () => void;
	onRelease: () => void;
	sessionId: string;
}) {
	const lease = device?.lease;
	return (
		<DropdownMenu>
			<DropdownMenuTrigger asChild>
				<button
					aria-label="Simulator and lease options"
					className="flex h-7 w-7 items-center justify-center rounded-full text-muted-foreground transition-colors hover:bg-overlay hover:text-foreground"
					type="button"
				>
					<MoreHorizontal aria-hidden className="h-4 w-4" />
				</button>
			</DropdownMenuTrigger>
			<DropdownMenuContent align="end" className="max-w-[280px]">
				{device ? (
					<>
						{/* Not DropdownMenuLabel: that is the menu's section-heading tone -
						    uppercase mono at `text-passive`, which measures 3.61:1 on dark
						    and 2.74:1 on light. Right for the word "LEASE", wrong for a
						    sentence a person has to read to know who is driving a device. */}
						<div className="whitespace-normal px-2 py-1.5 text-[11px] leading-snug">
							{heldByThisSession ? (
								<span className="text-success">Leased by this session (@{sessionId})</span>
							) : heldByOther ? (
								<span className="text-warning">
									Leased by @{lease?.holder}. Watching is always allowed; driving is not, until they release it.
								</span>
							) : (
								/* Never "free": AO knows its own leases and nothing else. */
								<span className="text-muted-foreground">Lease: unknown - {lease?.reason}</span>
							)}
						</div>
						{heldByThisSession ? (
							<DropdownMenuItem disabled={busy} onSelect={onRelease}>
								Release
							</DropdownMenuItem>
						) : heldByOther ? null : (
							<DropdownMenuItem disabled={busy} onSelect={onClaim}>
								Claim to drive
							</DropdownMenuItem>
						)}
						<DropdownMenuSeparator />
					</>
				) : null}
				<DropdownMenuItem onSelect={onRefresh}>Refresh simulators</DropdownMenuItem>
			</DropdownMenuContent>
		</DropdownMenu>
	);
}
