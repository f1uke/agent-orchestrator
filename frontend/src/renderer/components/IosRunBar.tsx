import { useEffect, useMemo, useState } from "react";
import { AlertTriangle, CheckCircle2, ChevronDown, Loader2, Play, Terminal, XCircle } from "lucide-react";
import {
	useIosProject,
	useRefreshIosProject,
	useStartIosRun,
	type IosProject,
	type IosRun,
} from "../hooks/useIosProject";
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
 * The app environment is TWO axes, not one, which is why there are two pickers
 * and not a merged one. The SCHEME says what to build; the CONFIGURATION says
 * which environment it is built for. On nter-ios-app the schemes are the app and
 * a library, and every environment it has - Dev, UAT, Production - is a
 * configuration. Both lists are read live from the project: there is nothing to
 * configure, and a scheme or configuration added this morning is offered this
 * morning.
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
	const [configuration, setConfiguration] = useState<string | null>(null);
	const [udid, setUdid] = useState<string | null>(null);

	// The device list is only asked for once the bar is really rendering, which
	// on a non-iOS project is never. `enabled` is what keeps a Go worktree from
	// polling simctl every five seconds for a control it does not have.
	const hasProject = Boolean(project?.name);
	const devices = useSimDevices(hasProject);
	const power = useSimPower(sessionId, setProblem);
	const start = useStartIosRun(sessionId, setProblem);
	// Opening either picker re-reads the project. The human's workflow is
	// `xcodegen` in the terminal and then straight to the dropdown, which the
	// 30-second poll notices far too late; an idle window still costs nothing,
	// because nothing calls this until a dropdown opens.
	const refresh = useRefreshIosProject(sessionId);

	// The ONE reading of the scheme list, and every use below goes through it.
	// The `?? []` is not redundant with the spec's `string[]`: a daemon that
	// answered `null` once already took the whole renderer down, and a type
	// cannot stop a payload from arriving malformed. Do not read
	// `project.schemes` anywhere else in this file.
	const schemes = useMemo(() => project?.schemes ?? [], [project?.schemes]);
	// Same treatment, same reason: the ONE reading of the configuration list.
	const configurations = useMemo(() => project?.configurations ?? [], [project?.configurations]);
	const booted = useMemo(() => (devices.data?.devices ?? []).filter((d) => d.state === "Booted"), [devices.data]);
	const allDevices = devices.data?.devices ?? [];

	// The scheme AO would build. A project with one scheme needs no choice made;
	// the run that is already going wins over both, so re-pressing Run after a
	// failed build rebuilds what failed rather than whatever was first in a list.
	const chosenScheme = scheme ?? run?.scheme ?? (schemes.length === 1 ? schemes[0] : null);
	// The configuration, in the same order of preference, ending at a default
	// that exists rather than an assumed Debug - see defaultConfiguration.
	const chosenConfiguration = configuration ?? run?.configuration ?? defaultConfiguration(configurations);
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
	useEffect(() => {
		if (configuration && configurations.length > 0 && !configurations.includes(configuration)) setConfiguration(null);
	}, [configuration, configurations]);

	if (isLoading && !data) return null;
	if (!project?.name) return null;

	const watchingRun = terminalTarget.kind === "run";
	const noSimulators = !devices.isLoading && allDevices.length === 0;
	const blocked = blockedReason({
		project,
		schemes,
		configurations,
		chosenScheme,
		chosenConfiguration,
		chosenUdid,
		noSimulators,
		booted: booted.length,
	});

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
					if (!chosenScheme || !chosenConfiguration) return;
					start.mutate(
						{ scheme: chosenScheme, configuration: chosenConfiguration, udid: chosenUdid ?? undefined },
						{ onSuccess: (started) => onShowRun(started.handleId) },
					);
				}}
				title={blocked ?? `Build ${chosenScheme} (${chosenConfiguration}) and run it`}
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

			<ChoicePicker
				chosen={chosenScheme}
				choices={schemes}
				empty="No schemes"
				label="Scheme to run"
				onChoose={setScheme}
				onOpen={refresh.mutate}
				placeholder="Choose a scheme"
				reason={project.schemesError ?? ""}
				refreshing={refresh.isPending}
				width="w-[150px]"
			/>
			<ChoicePicker
				chosen={chosenConfiguration}
				choices={configurations}
				empty="No configurations"
				label="Build configuration to run"
				onChoose={setConfiguration}
				onOpen={refresh.mutate}
				placeholder="Choose a configuration"
				reason={project.configurationsError ?? ""}
				refreshing={refresh.isPending}
				width="w-[180px]"
			/>

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
				{/*
				 * A run's own warning outranks the chip beside it: the run
				 * WORKED, so the chip says "Ran NterApp (Dev)" in the ordinary
				 * tone, and the only thing wrong is the app it left on the
				 * device. That combination shipped a build with no entitlements
				 * from this very button and took three hours to trace, so it
				 * gets the same treatment as a refusal rather than a tooltip
				 * nobody hovers.
				 */}
				{!problem && run?.warning ? <Problem message={run.warning} /> : null}
				{run ? <RunChip onShowAgent={onShowAgent} onShowRun={onShowRun} run={run} watching={watchingRun} /> : null}
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
	configurations,
	chosenScheme,
	chosenConfiguration,
	chosenUdid,
	noSimulators,
	booted,
}: {
	project: IosProject;
	/** The scheme list the component already normalised - never the raw payload. */
	schemes: string[];
	/** Likewise the configurations. */
	configurations: string[];
	chosenScheme: string | null;
	chosenConfiguration: string | null;
	chosenUdid: string | null;
	noSimulators: boolean;
	booted: number;
}): string | null {
	if (noSimulators) return "This machine has no iOS Simulators installed, so there is nothing to run the app on.";
	if (schemes.length === 0) {
		return project.schemesError || `${project.name} listed no schemes, so there is nothing to build.`;
	}
	if (!chosenScheme) return "Choose which scheme to run.";
	// 🗝 No configuration, no build - never a fallback to Debug. nter-ios-app has
	// no Debug configuration at all, and building one there fails three minutes
	// in with an empty PODS_ROOT. A blocked button that says which choice is
	// missing costs a click; a doomed build costs the whole wait.
	if (configurations.length === 0) {
		return (
			project.configurationsError ||
			`${project.name} listed no build configurations, so there is nothing safe to build.`
		);
	}
	if (!chosenConfiguration) return "Choose which build configuration to run - it is the app's environment.";
	if (!chosenUdid) {
		// Several booted is the ambiguity; several installed and none booted is
		// the other one, and they want different words - the second asks the
		// human which multi-gigabyte device to start.
		return booted > 1 ? "Choose which simulator to run on." : "Choose which simulator to boot and run on.";
	}
	return null;
}

/**
 * Which configuration to build when nobody has said, or null when there is no
 * safe answer and the human has to choose.
 *
 * 🗝 The same rule the daemon and `ao sim run` apply, so all three agree about
 * what Run means: the only one is used without asking, a project that HAS Debug
 * gets Debug because that is what Xcode's Run button builds, and anything else
 * is a question rather than a guess. Picking Dev over UAT for somebody is
 * picking which backend their app talks to.
 */
function defaultConfiguration(configurations: string[]): string | null {
	if (configurations.length === 1) return configurations[0];
	return configurations.find((configuration) => configuration.toLowerCase() === "debug") ?? null;
}

/**
 * One of the run bar's two live lists: the scheme to build, or the
 * configuration to build it for.
 *
 * It is one component used twice rather than two, because the pair has to read
 * as a pair - and it is the same shape as the device picker beside it rather
 * than a shadcn Select for the same reason. The trigger is a FIXED width, so
 * choosing a long name cannot shift the controls after it sideways.
 *
 * Opening it re-reads the project (onOpen). That is the moment the answer
 * matters: the human's loop is `xcodegen` in the terminal, then this dropdown.
 */
function ChoicePicker({
	chosen,
	choices,
	empty,
	label,
	onChoose,
	onOpen,
	placeholder,
	reason,
	refreshing,
	width,
}: {
	chosen: string | null;
	choices: string[];
	/** What the trigger says when the list is empty. */
	empty: string;
	/** The control's name, for the trigger's aria-label. */
	label: string;
	onChoose: (choice: string) => void;
	onOpen: () => void;
	placeholder: string;
	/** Why the list is empty, when it is. */
	reason: string;
	refreshing: boolean;
	/**
	 * The trigger's fixed width. Fixed so that choosing a long name cannot shift
	 * the controls after it sideways, and per-picker because the two
	 * placeholders are different lengths - "Choose a configuration" truncated to
	 * "Choose a configur…" reads as a bug rather than as a prompt.
	 */
	width: string;
}) {
	const [open, setOpen] = useState(false);
	return (
		<Popover
			onOpenChange={(next) => {
				setOpen(next);
				if (next) onOpen();
			}}
			open={open}
		>
			<PopoverTrigger asChild>
				<button
					aria-label={label}
					className={cn(
						"flex h-7 shrink-0 items-center gap-1 rounded-md px-2 text-[12px] font-medium text-foreground transition-colors hover:bg-overlay",
						width,
					)}
					type="button"
				>
					<span className={cn("min-w-0 flex-1 truncate text-left", !chosen && "text-muted-foreground")}>
						{chosen ?? (choices.length === 0 ? empty : placeholder)}
					</span>
					<ChevronDown aria-hidden className="size-3.5 shrink-0 text-muted-foreground" />
				</button>
			</PopoverTrigger>
			<PopoverContent align="start" className="w-[260px] p-1">
				{choices.length === 0 ? (
					<p className="px-2 py-3 text-[11px] leading-snug text-muted-foreground">
						{refreshing ? "Reading the project…" : reason || "This project listed nothing to choose from."}
					</p>
				) : (
					choices.map((choice) => (
						<button
							className={cn(
								"flex w-full items-center rounded-md px-2 py-1.5 text-left text-[12px] text-foreground transition-colors hover:bg-overlay",
								choice === chosen && "bg-overlay",
							)}
							key={choice}
							onClick={() => {
								onChoose(choice);
								setOpen(false);
							}}
							type="button"
						>
							<span className="min-w-0 truncate">{choice}</span>
						</button>
					))
				)}
				{choices.length > 0 && refreshing ? (
					// Said out loud rather than left to a list that silently grows: the
					// human opened this BECAUSE they just changed the project, so "is it
					// looking?" is the question the control has to answer.
					<p className="flex items-center gap-1.5 px-2 pt-1.5 pb-1 text-[11px] text-muted-foreground">
						<Loader2 aria-hidden className="size-3 animate-spin motion-reduce:animate-none" />
						Re-reading the project…
					</p>
				) : null}
			</PopoverContent>
		</Popover>
	);
}

/**
 * How the run went, and the way to its output.
 *
 * 🗝 It says WHICH of four things happened, because "not running any more" is
 * not an outcome: a build that succeeded and `building NterApp failed (exit
 * status 65)` used to look identical here - a chip that had stopped spinning.
 * The four are the daemon's, not this component's: running is the command still
 * alive in its pane, succeeded and failed are what `ao sim run` reported as it
 * exited, and stopped is a run that ended without reporting one.
 *
 * ⚠ It never restates the error. The compiler's output is in the pane, in a
 * real terminal with scrollback, and this chip's job is to say a run failed and
 * be the way back to it - which is also why it survives the human going to
 * another session and coming back: the daemon keeps the verdict on disk.
 */
function RunChip({
	onShowAgent,
	onShowRun,
	run,
	watching,
}: {
	onShowAgent: () => void;
	onShowRun: (handleId: string) => void;
	run: IosRun;
	/** Whether the terminal is already pointed at this run's pane. */
	watching: boolean;
}) {
	// The configuration rides along with the scheme in the label: on a project
	// whose environments ARE its configurations, "NterApp failed" leaves out
	// half of what failed.
	const built = run.configuration ? `${run.scheme} (${run.configuration})` : run.scheme;
	const state = {
		running: {
			icon: <Loader2 aria-hidden className="size-3.5 animate-spin text-accent motion-reduce:animate-none" />,
			label: `Running ${built}`,
			tone: "text-muted-foreground",
		},
		succeeded: {
			icon: <CheckCircle2 aria-hidden className="size-3.5 text-success" />,
			label: `Ran ${built}`,
			tone: "text-muted-foreground",
		},
		failed: {
			icon: <XCircle aria-hidden className="size-3.5 text-error" />,
			label: `${built} failed`,
			tone: "text-error",
		},
		stopped: {
			icon: <Terminal aria-hidden className="size-3.5" />,
			label: `${built} stopped`,
			tone: "text-muted-foreground",
		},
	}[run.state];
	return (
		<button
			className={cn(
				"flex h-7 shrink-0 items-center gap-1.5 rounded-md px-2 text-[11px] transition-colors hover:bg-overlay",
				watching ? "text-foreground" : state.tone,
			)}
			onClick={() => (watching ? onShowAgent() : onShowRun(run.handleId))}
			// The one line the daemon carries up from the command - "building
			// NterApp failed (exit status 65)" - rather than a tooltip that
			// repeats the label.
			title={run.summary || state.label}
			type="button"
		>
			{state.icon}
			{watching ? "Back to agent" : state.label}
		</button>
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
