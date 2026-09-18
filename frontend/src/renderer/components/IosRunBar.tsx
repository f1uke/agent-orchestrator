import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, ChevronDown, Loader2, Play, Terminal } from "lucide-react";
import { useIosProject, useStartIosRun, type IosProject } from "../hooks/useIosProject";
import { useSimDevices } from "../hooks/useSimDevices";
import { useSimPower } from "../hooks/useSimPower";
import type { Task } from "../lib/crew";
import { cn } from "../lib/utils";
import type { TerminalTarget } from "../types/terminal";
import { SimDevicePicker } from "./SimDevicePicker";
import { Popover, PopoverContent, PopoverTrigger } from "./ui/popover";

/**
 * The iOS run bar: pick a simulator and an app environment, press Run, watch it
 * build in the terminal underneath.
 *
 * 🗝 Why it is absent rather than disabled on a non-iOS project. The bar's whole
 * visibility test is whether this session's WORKTREE holds an `.xcodeproj` or
 * `.xcworkspace` at its root - not what kind of session it is, so an
 * orchestrator and a worker on the same project both get it. A Go repository
 * gets no bar at all: a disabled strip on every non-iOS session would be a row
 * of controls that can never do anything, permanently occupying the space above
 * every terminal in the app.
 *
 * ⚠ Nothing here talks to a simulator, and that is deliberate. Run POSTs to the
 * daemon, which starts `ao sim run` in a pane; the lease, the two-simulator boot
 * cap and the install all live in that command. A button that reimplemented any
 * of them would be a second answer to "may I have this device", and the two
 * would eventually disagree - which is precisely the failure the lease exists to
 * prevent.
 *
 * The environments are Xcode SCHEMES, read live from the project. There is
 * nothing to configure: a scheme added this morning is offered this morning.
 */
export function IosRunBar({
	onShowRun,
	onShowAgent,
	sessionId,
	task,
	terminalTarget,
}: {
	/** Point the terminal at the run pane - what Run does, and what the chip re-does. */
	onShowRun: (handleId: string) => void;
	/** Point the terminal back at the agent. */
	onShowAgent: () => void;
	sessionId: string;
	/** This session's task, so a device held by the crewmate is named by its role. */
	task?: Task;
	terminalTarget: TerminalTarget;
}) {
	const { data, isLoading } = useIosProject(sessionId);
	const project = data?.project;
	const run = data?.run;
	const [problem, setProblem] = useState("");
	const [scheme, setScheme] = useState<string | null>(null);
	const [udid, setUdid] = useState<string | null>(null);

	// The device list is only asked for once the bar is really rendering, which
	// on a non-iOS project is never. `enabled` is what keeps a Go worktree from
	// polling simctl every five seconds for a control it does not have.
	const hasProject = Boolean(project?.name);
	const devices = useSimDevices(hasProject);
	const power = useSimPower(sessionId, setProblem);
	const start = useStartIosRun(sessionId, setProblem);

	// The ONE reading of the scheme list, and every use below goes through it.
	// The `?? []` is not redundant with the spec's `string[]`: a daemon that
	// answered `null` once already took the whole renderer down, and a type
	// cannot stop a payload from arriving malformed. Do not read
	// `project.schemes` anywhere else in this file.
	const schemes = useMemo(() => project?.schemes ?? [], [project?.schemes]);
	const booted = useMemo(() => (devices.data?.devices ?? []).filter((d) => d.state === "Booted"), [devices.data]);
	const allDevices = devices.data?.devices ?? [];

	// The scheme AO would build. A project with one scheme needs no choice made;
	// the run that is already going wins over both, so re-pressing Run after a
	// failed build rebuilds what failed rather than whatever was first in a list.
	const chosenScheme = scheme ?? run?.scheme ?? (schemes.length === 1 ? schemes[0] : null);
	// The device, in the same order of preference: what was picked, what the last
	// run used, then the machine's one obvious candidate.
	//
	// "Obvious" is `resolveSimBootTarget`'s rule, not a new one: the single
	// BOOTED device if there is exactly one, else the machine's only simulator
	// even when it is shut down - `ao sim run` boots it on the way through, so
	// refusing here would be a dead end the CLI itself does not have. What it
	// never does is choose between two booted devices: that is the ambiguity
	// `ao sim` refuses, and the wrong guess installs onto the device a crewmate
	// is verifying on.
	const chosenUdid =
		udid ?? run?.udid ?? (booted.length === 1 ? booted[0].udid : allDevices.length === 1 ? allDevices[0].udid : null);

	// A scheme that vanished (renamed in Xcode, or a different project checked
	// out) must not stay selected: pressing Run would be refused by the daemon
	// for a scheme the human can no longer see in the list.
	useEffect(() => {
		if (scheme && schemes.length > 0 && !schemes.includes(scheme)) setScheme(null);
	}, [scheme, schemes]);

	if (isLoading && !data) return null;
	if (!project?.name) return null;

	const watchingRun = terminalTarget.kind === "run";
	const noSimulators = !devices.isLoading && allDevices.length === 0;
	const blocked = blockedReason({ project, schemes, chosenScheme, chosenUdid, noSimulators, booted: booted.length });

	return (
		<div
			className="flex shrink-0 flex-wrap items-center gap-1.5 border-b border-border bg-raised px-2 py-1.5"
			data-testid="ios-run-bar"
		>
			<button
				aria-label={chosenScheme ? `Run ${chosenScheme}` : "Run"}
				className={cn(
					"flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2.5 text-[12px] font-medium transition-colors",
					blocked ? "cursor-not-allowed text-passive" : "bg-accent text-accent-foreground hover:brightness-110",
				)}
				disabled={Boolean(blocked) || start.isPending}
				onClick={() => {
					if (!chosenScheme) return;
					start.mutate(
						{ scheme: chosenScheme, udid: chosenUdid ?? undefined },
						{ onSuccess: (started) => onShowRun(started.handleId) },
					);
				}}
				title={blocked ?? undefined}
				type="button"
			>
				{start.isPending ? (
					<Loader2 aria-hidden className="size-3.5 animate-spin motion-reduce:animate-none" />
				) : (
					// Filled, not lucide's outline. At 14px a hollow triangle on the
					// accent fill reads as an outline of a button rather than the
					// play mark every run control in every tool uses.
					<Play aria-hidden className="size-3.5" fill="currentColor" />
				)}
				Run
			</button>

			<SchemePicker chosen={chosenScheme} onChoose={setScheme} reason={project.schemesError ?? ""} schemes={schemes} />

			<span aria-hidden className="h-4 w-px shrink-0 bg-border" />

			<SimDevicePicker
				chosen={chosenUdid}
				devices={allDevices}
				loading={devices.isLoading}
				onChoose={setUdid}
				onPower={power.mutate}
				sessionId={sessionId}
				task={task}
			/>

			<div className="ml-auto flex min-w-0 items-center gap-2">
				{problem ? <Problem message={problem} /> : null}
				{run ? (
					// The one control that says where the build output IS. Without it,
					// a run started, the terminal switched, the human went back to the
					// agent and the build became unreachable.
					<button
						className={cn(
							"flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-[11px] transition-colors hover:bg-overlay",
							watchingRun ? "text-foreground" : "text-muted-foreground",
						)}
						onClick={() => (watchingRun ? onShowAgent() : onShowRun(run.handleId))}
						type="button"
					>
						{run.running ? (
							<Loader2 aria-hidden className="size-3.5 animate-spin text-accent motion-reduce:animate-none" />
						) : (
							<Terminal aria-hidden className="size-3.5" />
						)}
						{watchingRun ? "Back to agent" : run.running ? `Running ${run.scheme}` : `${run.scheme} output`}
					</button>
				) : null}
			</div>
		</div>
	);
}

/**
 * Why Run cannot be pressed, in the words that say what to do about it - or null
 * when it can. Every branch names the missing thing rather than leaving a button
 * greyed out for a reason the human has to guess.
 */
function blockedReason({
	project,
	schemes,
	chosenScheme,
	chosenUdid,
	noSimulators,
	booted,
}: {
	project: IosProject;
	/** The scheme list the component already normalised - never the raw payload. */
	schemes: string[];
	chosenScheme: string | null;
	chosenUdid: string | null;
	noSimulators: boolean;
	booted: number;
}): string | null {
	if (noSimulators) return "This machine has no iOS Simulators installed, so there is nothing to run the app on.";
	if (schemes.length === 0) {
		return project.schemesError || `${project.name} listed no schemes, so there is nothing to build.`;
	}
	if (!chosenScheme) return "Choose which scheme to run.";
	if (!chosenUdid) {
		// Several booted is the ambiguity; several installed and none booted is
		// the other one, and they want different words - the second asks the
		// human which multi-gigabyte device to start.
		return booted > 1 ? "Choose which simulator to run on." : "Choose which simulator to boot and run on.";
	}
	return null;
}

/**
 * The app environment: the project's Xcode schemes, read live.
 *
 * It is the same shape as the device picker beside it rather than a shadcn
 * Select, so the two controls in one strip read as a pair - and for the same
 * layout reason that picker gives: the trigger is a FIXED width, so choosing a
 * long scheme name cannot shift the device picker sideways underneath it.
 */
function SchemePicker({
	chosen,
	onChoose,
	reason,
	schemes,
}: {
	chosen: string | null;
	onChoose: (scheme: string) => void;
	/** Why the list is empty, when it is. */
	reason: string;
	schemes: string[];
}) {
	const [open, setOpen] = useState(false);
	return (
		<Popover onOpenChange={setOpen} open={open}>
			<PopoverTrigger asChild>
				<button
					aria-label="Scheme to run"
					className="flex h-7 w-[150px] shrink-0 items-center gap-1 rounded-md px-2 text-[12px] font-medium text-foreground transition-colors hover:bg-overlay"
					type="button"
				>
					<span className={cn("min-w-0 flex-1 truncate text-left", !chosen && "text-muted-foreground")}>
						{chosen ?? (schemes.length === 0 ? "No schemes" : "Choose a scheme")}
					</span>
					<ChevronDown aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
				</button>
			</PopoverTrigger>
			<PopoverContent align="start" className="w-[260px] p-1">
				{schemes.length === 0 ? (
					<p className="px-2 py-3 text-[11px] leading-snug text-muted-foreground">
						{reason || "This project listed no schemes."}
					</p>
				) : (
					schemes.map((scheme) => (
						<button
							className={cn(
								"flex w-full items-center rounded-md px-2 py-1.5 text-left text-[12px] text-foreground transition-colors hover:bg-overlay",
								scheme === chosen && "bg-overlay",
							)}
							key={scheme}
							onClick={() => {
								onChoose(scheme);
								setOpen(false);
							}}
							type="button"
						>
							<span className="min-w-0 truncate">{scheme}</span>
						</button>
					))
				)}
			</PopoverContent>
		</Popover>
	);
}

/**
 * A refusal from the daemon, in its own words. It sits in the bar rather than in
 * a toast because the commonest one is a crewmate holding the device, which is a
 * fact about the control it is beside - not an event that happened once.
 */
function Problem({ message }: { message: string }) {
	return (
		<p className="flex min-w-0 items-center gap-1.5 text-[11px] leading-snug text-error" role="status">
			<AlertTriangle aria-hidden className="size-3.5 shrink-0" />
			<span className="min-w-0 truncate" title={message}>
				{message}
			</span>
		</p>
	);
}
