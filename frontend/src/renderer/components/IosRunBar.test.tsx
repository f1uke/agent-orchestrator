import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import type { ReactNode } from "react";
import { beforeEach, describe, expect, it, vi } from "vitest";
import { IosRunBar } from "./IosRunBar";

const { getMock, postMock } = vi.hoisted(() => ({ getMock: vi.fn(), postMock: vi.fn() }));

vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, POST: postMock },
	apiErrorMessage: (error: unknown, fallback = "Request failed") =>
		(error as { message?: string } | null)?.message ?? fallback,
	getApiBaseUrl: () => "http://127.0.0.1:3001",
	subscribeApiBaseUrl: () => () => {},
}));

const SESSION = "mer-9";

const device = (udid: string, name: string, state: string) => ({
	udid,
	name,
	runtime: "iOS 26.3",
	runtimeIdentifier: "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
	state,
	available: true,
	lease: { state: "free" },
});

const project = (over: Record<string, unknown> = {}) => ({
	name: "Nter.xcworkspace",
	path: "/w/Nter.xcworkspace",
	kind: "workspace",
	schemes: ["Nter", "NterDev"],
	// Debug and Release, so the tests that are about something else get a
	// configuration without choosing one. The no-Debug project - which is the
	// real one this feature exists for - is its own test below.
	configurations: ["Debug", "Release"],
	...over,
});

function answer({ ios, devices }: { ios?: Record<string, unknown>; devices?: unknown[] } = {}) {
	getMock.mockImplementation((path: string) => {
		if (path === "/api/v1/sessions/{sessionId}/ios-project") {
			return Promise.resolve({ data: ios ?? { project: project() } });
		}
		return Promise.resolve({
			data: { devices: devices ?? [device("UDID-A", "iPhone 17 Pro Max", "Booted")], defaultUdid: null },
		});
	});
}

function Wrapper({ children }: { children: ReactNode }) {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return <QueryClientProvider client={client}>{children}</QueryClientProvider>;
}

function renderBar(onShowRun = vi.fn(), onShowAgent = vi.fn()) {
	render(
		<IosRunBar
			onShowAgent={onShowAgent}
			onShowRun={onShowRun}
			sessionId={SESSION}
			terminalTarget={{ kind: "worker" }}
		/>,
		{ wrapper: Wrapper },
	);
	return { onShowRun, onShowAgent };
}

describe("IosRunBar", () => {
	beforeEach(() => {
		getMock.mockReset();
		postMock.mockReset();
	});

	// The bar's whole visibility test. A disabled strip on every non-iOS session
	// would be a row of controls that can never do anything, permanently above
	// every terminal in the app.
	it("does not render at all on a project with no Xcode project", async () => {
		answer({ ios: { project: { name: "", path: "", kind: "", schemes: [], configurations: [] } } });
		renderBar();

		await waitFor(() => expect(getMock).toHaveBeenCalled());
		expect(screen.queryByTestId("ios-run-bar")).not.toBeInTheDocument();
		// And nothing asked simctl for a device list it has no control for.
		expect(getMock).not.toHaveBeenCalledWith("/api/v1/sim/devices", expect.anything());
	});

	it("offers the project's schemes and runs the one that is chosen", async () => {
		answer();
		postMock.mockResolvedValue({
			data: {
				run: {
					handleId: "iosrun-mer-9",
					scheme: "NterDev",
					udid: "UDID-A",
					running: true,
					startedAt: "2026-09-18T10:00:00Z",
				},
			},
		});
		const { onShowRun } = renderBar();

		await screen.findByTestId("ios-run-bar");
		await userEvent.click(screen.getByRole("button", { name: "Scheme to run" }));
		await userEvent.click(await screen.findByRole("button", { name: "NterDev" }));
		await userEvent.click(screen.getByRole("button", { name: "Run NterDev" }));

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/ios-runs",
				expect.objectContaining({ body: { scheme: "NterDev", configuration: "Debug", udid: "UDID-A" } }),
			),
		);
		// The build output has to be where the human is looking, or it is a
		// three-minute operation with no visible progress at all.
		await waitFor(() => expect(onShowRun).toHaveBeenCalledWith("iosrun-mer-9"));
	});

	// The brief's case: no simulator on the machine. Run says WHY rather than
	// being greyed out for a reason the human has to guess.
	it("refuses to run when the machine has no simulators, and says so", async () => {
		answer({ devices: [], ios: { project: project({ schemes: ["Nter"] }) } });
		renderBar();

		const run = await screen.findByRole("button", { name: /^Run/ });
		await waitFor(() => expect(run.title).toMatch(/no iOS Simulators installed/i));
		expect(run).toBeDisabled();
	});

	// Two booted devices is the ambiguity `ao sim` refuses; the bar must not
	// pre-empt it by guessing, because the wrong guess installs onto the device
	// a crewmate is verifying on.
	it("will not guess between two booted simulators", async () => {
		answer({
			devices: [device("UDID-A", "iPhone 17 Pro Max", "Booted"), device("UDID-B", "iPad Pro", "Booted")],
			ios: { project: project({ schemes: ["Nter"] }) },
		});
		renderBar();

		const run = await screen.findByRole("button", { name: /^Run/ });
		await waitFor(() => expect(run.title).toMatch(/Choose which simulator to run on/i));
		expect(run).toBeDisabled();
	});

	// A single SHUT-DOWN device is fine: `ao sim run` boots it on the way
	// through, so refusing here would be a dead end the CLI does not have.
	it("runs on the machine's only simulator even when it is shut down", async () => {
		answer({
			devices: [device("UDID-A", "iPhone 17 Pro Max", "Shutdown")],
			ios: { project: project({ schemes: ["Nter"] }) },
		});
		renderBar();

		const run = await screen.findByRole("button", { name: "Run Nter" });
		await waitFor(() => expect(run).toBeEnabled());
	});

	it("says why there is nothing to build when the project listed no schemes", async () => {
		answer({ ios: { project: project({ schemes: [], schemesError: "xcodebuild is not installed" }) } });
		renderBar();

		const run = await screen.findByRole("button", { name: "Run" });
		await waitFor(() => expect(run.title).toBe("xcodebuild is not installed"));
		expect(run).toBeDisabled();
	});

	// A malformed payload must not take the whole renderer down. `schemes: null`
	// is what a daemon before the array fix answers for a CocoaPods workspace
	// with no `Pods/`, and every read of it in the bar has to survive that.
	it("renders its no-schemes state rather than throwing when schemes is null", async () => {
		answer({ ios: { project: project({ schemes: null, schemesError: "This project has no schemes." }) } });
		renderBar();

		await screen.findByTestId("ios-run-bar");
		const run = await screen.findByRole("button", { name: "Run" });
		await waitFor(() => expect(run.title).toBe("This project has no schemes."));
		expect(run).toBeDisabled();
		expect(screen.getByRole("button", { name: "Scheme to run" })).toHaveTextContent("No schemes");
	});

	// A refusal from the daemon - a crewmate holding the device is the common
	// one - is a fact about the control it sits beside, not a toast that goes.
	it("keeps a refusal beside the control that caused it", async () => {
		answer({ ios: { project: project({ schemes: ["Nter"] }) } });
		postMock.mockResolvedValue({ error: { message: "simulator is leased by @agent-orchestrator-105" } });
		renderBar();

		const run = await screen.findByRole("button", { name: "Run Nter" });
		await waitFor(() => expect(run).toBeEnabled());
		await userEvent.click(run);

		expect(await screen.findByText(/leased by @agent-orchestrator-105/)).toBeInTheDocument();
	});

	// Without this, a run starts, the terminal switches, the human goes back to
	// the agent, and the build output becomes unreachable.
	it("offers a way back to a running build, and back to the agent from it", async () => {
		const run = {
			handleId: "iosrun-mer-9",
			scheme: "NterDev",
			configuration: "Debug",
			udid: "UDID-A",
			state: "running",
			startedAt: "2026-09-18T10:00:00Z",
		};
		answer({ ios: { project: project(), run } });
		const { onShowRun } = renderBar();

		await userEvent.click(await screen.findByRole("button", { name: /Running NterDev \(Debug\)/ }));
		expect(onShowRun).toHaveBeenCalledWith("iosrun-mer-9");

		const onShowAgent = vi.fn();
		render(
			<IosRunBar
				onShowAgent={onShowAgent}
				onShowRun={vi.fn()}
				sessionId={SESSION}
				terminalTarget={{ kind: "run", handleId: "iosrun-mer-9" }}
			/>,
			{ wrapper: Wrapper },
		);
		await userEvent.click(await screen.findByRole("button", { name: "Back to agent" }));
		expect(onShowAgent).toHaveBeenCalled();
	});

	// A finished build's pane stays on screen on purpose - its keep-alive shell
	// outlives the command - so a failed build is still readable afterwards.
	it("still points at the output once the build has finished", async () => {
		answer({
			ios: {
				project: project(),
				run: {
					handleId: "iosrun-mer-9",
					scheme: "NterDev",
					configuration: "Debug",
					udid: "UDID-A",
					state: "succeeded",
					summary: "Built NterDev (Debug) and launched com.example.Nter on iPhone 17 Pro Max.",
					startedAt: "2026-09-18T10:00:00Z",
					finishedAt: "2026-09-18T10:03:00Z",
				},
			},
		});
		renderBar();

		expect(await screen.findByRole("button", { name: /Ran NterDev \(Debug\)/ })).toBeInTheDocument();
	});

	// 🗝 The gap this closes: a build that succeeded and `building NterApp failed
	// (exit status 65)` used to look identical here - a chip that had stopped
	// spinning. The failure names BOTH axes, because on a project whose
	// environments are configurations, "NterApp failed" is half a sentence.
	it("says a run failed, names what it was building, and leads to the output", async () => {
		const onShowRun = vi.fn();
		answer({
			ios: {
				project: project({ schemes: ["NterApp"], configurations: ["Dev", "UAT"] }),
				run: {
					handleId: "iosrun-mer-9",
					scheme: "NterApp",
					configuration: "UAT",
					udid: "UDID-A",
					state: "failed",
					summary: "building NterApp failed (exit status 65). The compiler's output is above.",
					startedAt: "2026-09-18T10:00:00Z",
					finishedAt: "2026-09-18T10:03:00Z",
				},
			},
		});
		renderBar(onShowRun);

		const chip = await screen.findByRole("button", { name: /NterApp \(UAT\) failed/ });
		// The bar says WHICH run failed and points at the output; the compiler's
		// own errors stay in the terminal rather than being restated here.
		expect(chip.title).toMatch(/exit status 65/);
		await userEvent.click(chip);
		expect(onShowRun).toHaveBeenCalledWith("iosrun-mer-9");
	});

	// The defect this guard exists for reached a real user through this button:
	// the build was green, the app was on screen, and it had no entitlements, so
	// logging in closed its own auth page and left them logged out with no error
	// anywhere. A success the bar could not qualify is how that stayed invisible.
	it("says so when a run that succeeded installed an app that cannot reach the Keychain", async () => {
		answer({
			ios: {
				project: project({ schemes: ["NterApp"], configurations: ["Dev", "UAT"] }),
				run: {
					handleId: "iosrun-mer-9",
					scheme: "NterApp",
					configuration: "Dev",
					udid: "UDID-A",
					state: "succeeded",
					summary: "Built NterApp (Dev) and launched com.finnomena.app.finnomena on iPhone 17 Pro Max.",
					warning: "NterApp.app was never code signed - codesign reports its executable as linker-signed.",
					startedAt: "2026-09-18T10:00:00Z",
					finishedAt: "2026-09-18T10:03:00Z",
				},
			},
		});
		renderBar();

		// The run still reads as the success it was - the build compiled and the
		// app launched - and the warning sits beside it rather than replacing it.
		expect(await screen.findByRole("button", { name: /Ran NterApp \(Dev\)/ })).toBeInTheDocument();
		expect(await screen.findByText(/was never code signed/)).toBeInTheDocument();
	});

	// The ordinary run says nothing. A warning that is always there is furniture.
	it("shows no warning for a run that installed a properly signed app", async () => {
		answer({
			ios: {
				project: project(),
				run: {
					handleId: "iosrun-mer-9",
					scheme: "NterDev",
					configuration: "Debug",
					udid: "UDID-A",
					state: "succeeded",
					summary: "Built NterDev (Debug) and launched com.example.Nter on iPhone 17 Pro Max.",
					startedAt: "2026-09-18T10:00:00Z",
					finishedAt: "2026-09-18T10:03:00Z",
				},
			},
		});
		renderBar();

		await screen.findByRole("button", { name: /Ran NterDev \(Debug\)/ });
		expect(screen.queryByText(/never code signed/)).not.toBeInTheDocument();
	});

	// A run that ended without reporting - Ctrl-C in the pane, a tmux server
	// that went away - is not a failed build, and must not be called one.
	it("does not call a stopped run a failure", async () => {
		answer({
			ios: {
				project: project(),
				run: {
					handleId: "iosrun-mer-9",
					scheme: "NterDev",
					configuration: "Debug",
					udid: "UDID-A",
					state: "stopped",
					summary: "The run ended without reporting how it went. Its output is still in the pane.",
					startedAt: "2026-09-18T10:00:00Z",
				},
			},
		});
		renderBar();

		expect(await screen.findByRole("button", { name: /NterDev \(Debug\) stopped/ })).toBeInTheDocument();
		expect(screen.queryByRole("button", { name: /failed/ })).not.toBeInTheDocument();
	});

	// 🗝 The regression this feature closes, at the control that caused it.
	// nter-ios-app's configurations are Dev, Mock-api, Mock-local, Production,
	// Release and UAT - there is no Debug - and pressing Run with the old bar
	// started a build that could not work.
	it("will not run a project that has no Debug until a configuration is chosen", async () => {
		answer({
			ios: {
				project: project({
					schemes: ["NterApp"],
					configurations: ["Dev", "Mock-api", "Mock-local", "Production", "Release", "UAT"],
				}),
			},
		});
		renderBar();

		const run = await screen.findByRole("button", { name: "Run NterApp" });
		await waitFor(() => expect(run.title).toMatch(/Choose which build configuration/i));
		expect(run).toBeDisabled();

		// And once the human chooses, it is that configuration that gets built.
		await userEvent.click(screen.getByRole("button", { name: "Build configuration to run" }));
		await userEvent.click(await screen.findByRole("button", { name: "UAT" }));
		postMock.mockResolvedValue({
			data: {
				run: {
					handleId: "iosrun-mer-9",
					scheme: "NterApp",
					configuration: "UAT",
					udid: "UDID-A",
					running: true,
					startedAt: "2026-09-18T10:00:00Z",
				},
			},
		});
		await waitFor(() => expect(run).toBeEnabled());
		await userEvent.click(run);

		await waitFor(() =>
			expect(postMock).toHaveBeenCalledWith(
				"/api/v1/sessions/{sessionId}/ios-runs",
				expect.objectContaining({ body: { scheme: "NterApp", configuration: "UAT", udid: "UDID-A" } }),
			),
		);
	});

	// A project that HAS Debug keeps Xcode's own default, so the ordinary case
	// is still one click.
	it("defaults to Debug where the project has one", async () => {
		answer({ ios: { project: project({ schemes: ["Nter"] }) } });
		renderBar();

		const run = await screen.findByRole("button", { name: "Run Nter" });
		await waitFor(() => expect(run).toBeEnabled());
		expect(screen.getByRole("button", { name: "Build configuration to run" })).toHaveTextContent("Debug");
	});

	// The configuration list can fail on its own, and the bar must say which
	// question failed rather than blaming the schemes.
	it("says why there is nothing safe to build when no configuration could be read", async () => {
		answer({
			ios: {
				project: project({
					schemes: ["Nter"],
					configurations: [],
					configurationsError: "No build configurations could be read from this project.",
				}),
			},
		});
		renderBar();

		const run = await screen.findByRole("button", { name: "Run Nter" });
		await waitFor(() => expect(run.title).toBe("No build configurations could be read from this project."));
		expect(run).toBeDisabled();
		expect(screen.getByRole("button", { name: "Build configuration to run" })).toHaveTextContent("No configurations");
	});

	// Malformed payloads must not take the renderer down - `schemes: null` did
	// exactly that once, and `configurations` is the same kind of field.
	it("renders rather than throwing when configurations is null", async () => {
		answer({ ios: { project: project({ configurations: null }) } });
		renderBar();

		await screen.findByTestId("ios-run-bar");
		expect(screen.getByRole("button", { name: "Build configuration to run" })).toHaveTextContent("No configurations");
	});

	// The human's real loop is `xcodegen` in the terminal and then straight to
	// the picker. A 30-second poll notices that far too late, so the click
	// itself re-reads the project.
	it("re-reads the project when a picker is opened", async () => {
		answer({ ios: { project: project({ schemes: ["Nter"] }) } });
		renderBar();

		await screen.findByTestId("ios-run-bar");
		const refreshes = () =>
			getMock.mock.calls.filter(
				(call: unknown[]) =>
					call[0] === "/api/v1/sessions/{sessionId}/ios-project" &&
					(call[1] as { params?: { query?: { refresh?: boolean } } })?.params?.query?.refresh === true,
			).length;
		expect(refreshes()).toBe(0);

		await userEvent.click(screen.getByRole("button", { name: "Scheme to run" }));
		await waitFor(() => expect(refreshes()).toBe(1));
		await userEvent.keyboard("{Escape}");
		await userEvent.click(screen.getByRole("button", { name: "Build configuration to run" }));
		await waitFor(() => expect(refreshes()).toBe(2));
	});
});
