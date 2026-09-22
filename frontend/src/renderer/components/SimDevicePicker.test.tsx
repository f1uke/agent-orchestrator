import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { describe, expect, it, vi } from "vitest";
import { SimDevicePicker } from "./SimDevicePicker";
import type { SimDevice } from "../hooks/useSimDevices";

const device = (overrides: Partial<SimDevice> = {}): SimDevice =>
	({
		udid: "UDID-A",
		name: "iPhone 17 Pro Max",
		runtime: "iOS 26.3",
		runtimeIdentifier: "com.apple.CoreSimulator.SimRuntime.iOS-26-3",
		state: "Booted",
		available: true,
		default: false,
		lease: { state: "unknown", reason: "no AO session holds this device" },
		...overrides,
	}) as SimDevice;

function open(devices: SimDevice[], overrides: { chosen?: string | null; holderNames?: Map<string, string> } = {}) {
	const onPower = vi.fn();
	const onChoose = vi.fn();
	render(
		<SimDevicePicker
			chosen={overrides.chosen ?? null}
			devices={devices}
			holderNames={overrides.holderNames}
			loading={false}
			onChoose={onChoose}
			onPower={onPower}
			sessionId="p-1"
		/>,
	);
	return { onChoose, onPower };
}

const held = (holder: string) => ({ state: "held", holder }) as SimDevice["lease"];

const openPicker = () => userEvent.click(screen.getByRole("button", { name: /simulator to watch/i }));

describe("SimDevicePicker", () => {
	// The whole point of the change: a shut-down device used to be invisible,
	// so with nothing booted the tab was a dead end.
	it("lists devices that are not booted, with enough identity to tell them apart", async () => {
		open([
			device({ udid: "UDID-A", name: "iPhone 17 Pro Max", state: "Shutdown" }),
			device({ udid: "UDID-B", name: "iPhone 17 Pro", state: "Shutdown", runtime: "iOS 18.2" }),
		]);
		await openPicker();

		const first = screen.getByTestId("sim-device-UDID-A");
		expect(within(first).getByText("iPhone 17 Pro Max")).toBeInTheDocument();
		expect(within(first).getByText(/iOS 26\.3/)).toBeInTheDocument();
		// Same model name, different runtime: the runtime is what tells them
		// apart, so it is on the row rather than in a tooltip.
		expect(within(screen.getByTestId("sim-device-UDID-B")).getByText(/iOS 18\.2/)).toBeInTheDocument();
	});

	it("boots the first device with one press", async () => {
		const { onPower } = open([device({ state: "Shutdown" })]);
		await openPicker();
		await userEvent.click(screen.getByRole("button", { name: /^boot$/i }));

		expect(onPower).toHaveBeenCalledWith({ udid: "UDID-A", state: "booted" });
		expect(screen.queryByTestId("sim-power-confirm")).not.toBeInTheDocument();
	});

	// ⚠ The memory guard. A booted simulator costs roughly 4 GB and this machine
	// has hit a true OOM at three, so a device is never added to a machine that
	// is already carrying one without asking.
	it("asks before booting an additional device", async () => {
		const { onPower } = open([
			device({ udid: "UDID-A", state: "Booted" }),
			device({ udid: "UDID-B", name: "iPhone 17 Pro", state: "Shutdown" }),
		]);
		await openPicker();
		await userEvent.click(within(screen.getByTestId("sim-device-UDID-B")).getByRole("button", { name: /^boot$/i }));

		expect(onPower).not.toHaveBeenCalled();
		expect(screen.getByTestId("sim-power-confirm")).toHaveTextContent(/one is already up/i);

		await userEvent.click(screen.getByRole("button", { name: /boot anyway/i }));
		expect(onPower).toHaveBeenCalledWith({ udid: "UDID-B", state: "booted" });
	});

	// Two already booted is the count this machine actually died at, so the
	// question stops being about memory in general and names what happened.
	it("names the OOM once two are already booted", async () => {
		open([
			device({ udid: "UDID-A", state: "Booted" }),
			device({ udid: "UDID-B", name: "iPhone 17 Pro", state: "Booted" }),
			device({ udid: "UDID-C", name: "iPad Pro", state: "Shutdown" }),
		]);
		await openPicker();
		await userEvent.click(within(screen.getByTestId("sim-device-UDID-C")).getByRole("button", { name: /^boot$/i }));

		expect(screen.getByTestId("sim-power-confirm")).toHaveTextContent(/run this machine out of memory/i);
	});

	it("cancelling the warning boots nothing", async () => {
		const { onPower } = open([
			device({ udid: "UDID-A", state: "Booted" }),
			device({ udid: "UDID-B", name: "iPhone 17 Pro", state: "Shutdown" }),
		]);
		await openPicker();
		await userEvent.click(within(screen.getByTestId("sim-device-UDID-B")).getByRole("button", { name: /^boot$/i }));
		await userEvent.click(screen.getByRole("button", { name: /cancel/i }));

		expect(onPower).not.toHaveBeenCalled();
		expect(screen.queryByTestId("sim-power-confirm")).not.toBeInTheDocument();
	});

	// The count is on the header at all times, not only at the moment of
	// danger: a number somebody only sees when it is already too late is not a
	// guard, it is an apology.
	it("shows how many are booted, and what each costs, before anything is pressed", async () => {
		open([device({ udid: "UDID-A", state: "Booted" }), device({ udid: "UDID-B", state: "Shutdown" })]);
		await openPicker();

		const header = screen.getByTestId("sim-booted-count");
		expect(header).toHaveTextContent(/1 booted/i);
		expect(header).toHaveTextContent(/~4 GB/i);
	});

	it("shuts a booted device down, after asking", async () => {
		const { onPower } = open([device({ state: "Booted" })]);
		await openPicker();
		await userEvent.click(screen.getByRole("button", { name: /^shut down$/i }));

		expect(onPower).not.toHaveBeenCalled();
		expect(screen.getByTestId("sim-power-confirm")).toHaveTextContent(/everything running on it is lost/i);

		await userEvent.click(within(screen.getByTestId("sim-power-confirm")).getByRole("button", { name: /shut down/i }));
		expect(onPower).toHaveBeenCalledWith({ udid: "UDID-A", state: "shutdown", confirmHolder: undefined });
	});

	// 🗝 Taking a device away from another session names them - and the name
	// goes into the request, because the daemon refuses a shutdown that does
	// not match the live holder.
	it("names the holder when shutting down somebody else's device", async () => {
		const { onPower } = open([
			device({ state: "Booted", lease: { state: "held", holder: "p-9" } as SimDevice["lease"] }),
		]);
		await openPicker();
		expect(screen.getByTestId("sim-device-UDID-A")).toHaveTextContent(/leased by @p-9/i);

		await userEvent.click(screen.getByRole("button", { name: /^shut down$/i }));
		expect(screen.getByTestId("sim-power-confirm")).toHaveTextContent(/@p-9 is leasing it/i);

		await userEvent.click(within(screen.getByTestId("sim-power-confirm")).getByRole("button", { name: /shut down/i }));
		expect(onPower).toHaveBeenCalledWith({ udid: "UDID-A", state: "shutdown", confirmHolder: "p-9" });
	});

	// This session's own device is not somebody else's, so nothing is named.
	it("does not name this session as a holder to itself", async () => {
		const { onPower } = open([
			device({ state: "Booted", lease: { state: "held", holder: "p-1" } as SimDevice["lease"] }),
		]);
		await openPicker();
		await userEvent.click(screen.getByRole("button", { name: /^shut down$/i }));
		await userEvent.click(within(screen.getByTestId("sim-power-confirm")).getByRole("button", { name: /shut down/i }));

		expect(onPower).toHaveBeenCalledWith({ udid: "UDID-A", state: "shutdown", confirmHolder: undefined });
	});

	it("choosing a booted device watches it without touching its power", async () => {
		const { onChoose, onPower } = open([
			device({ udid: "UDID-A", state: "Booted" }),
			device({ udid: "UDID-B", name: "iPhone 17 Pro", state: "Booted" }),
		]);
		await openPicker();
		await userEvent.click(screen.getByRole("button", { name: /watch iPhone 17 Pro$/i }));

		expect(onChoose).toHaveBeenCalledWith("UDID-B");
		expect(onPower).not.toHaveBeenCalled();
	});

	// A boot takes tens of seconds, so the control has to say it is working and
	// say when it will give up.
	it("shows a boot in flight with its elapsed time and its deadline", async () => {
		vi.useFakeTimers({ shouldAdvanceTime: true });
		vi.setSystemTime(new Date("2026-08-20T09:00:12Z"));
		open([
			device({
				state: "Shutdown",
				power: { op: "boot", state: "running", startedAt: "2026-08-20T09:00:00Z" },
			} as Partial<SimDevice>),
		]);
		await openPicker();

		const running = screen.getByTestId("sim-power-running");
		expect(running).toHaveTextContent("0:12");
		expect(running).toHaveTextContent("2:00");
		// Nothing to press while it is working: the row cannot be asked twice.
		expect(screen.queryByRole("button", { name: /^boot$/i })).not.toBeInTheDocument();
		vi.useRealTimers();
	});

	// The failure this whole control exists to avoid is a spinner that never
	// resolves and never explains.
	it("reports a boot that failed, with the machine's own reason", async () => {
		open([
			device({
				state: "Shutdown",
				power: {
					op: "boot",
					state: "failed",
					startedAt: "2026-08-20T09:00:00Z",
					reason: "the simulator did not finish booting within 2m0s",
				},
			} as Partial<SimDevice>),
		]);
		await openPicker();

		expect(screen.getByTestId("sim-device-UDID-A")).toHaveTextContent(/did not finish booting within 2m0s/i);
		// And it can be tried again rather than being a terminal state.
		expect(screen.getByRole("button", { name: /^boot$/i })).toBeInTheDocument();
	});

	// The slimming phase takes tens of seconds. Without a label the pane just looks
	// stuck on a device that is already up.
	it("names the slimming phase instead of looking frozen", async () => {
		open([
			device({
				state: "Booted",
				power: { op: "boot", state: "running", phase: "slimming", startedAt: "2026-08-20T09:00:00Z" },
			} as Partial<SimDevice>),
		]);
		await openPicker();

		expect(screen.getByTestId("sim-power-running")).toHaveTextContent(/slimming/i);
	});

	// The boot worked. The device is stock. Saying nothing is how an agent ends up
	// trusting a push that was never delivered.
	it("warns that a booted device came up stock", async () => {
		open([
			device({
				state: "Booted",
				power: {
					op: "boot",
					state: "warned",
					startedAt: "2026-08-20T09:00:00Z",
					profile: "skipped",
					profileReason: "simslim is not on PATH",
				},
			} as Partial<SimDevice>),
		]);
		await openPicker();

		const stock = screen.getByTestId("sim-power-stock");
		expect(stock).toHaveTextContent(/stock/i);
		expect(stock).toHaveTextContent(/not on PATH/i);
		// The reason is somebody else's words, dropped mid-paragraph, so the row
		// has to punctuate it. Without this the row reads "...not on PATH
		// Features this project expects...".
		expect(stock).toHaveTextContent(/not on PATH\. Features/);
	});

	// The machine sometimes punctuates its own reason - a shell's stderr often
	// ends in a full stop - and a second one appended blind reads as a typo.
	it("does not double a full stop the reason already has", async () => {
		open([
			device({
				state: "Booted",
				power: {
					op: "boot",
					state: "warned",
					startedAt: "2026-08-20T09:00:00Z",
					profile: "failed",
					profileReason: "simslim: unknown daemon com.apple.nope.",
				},
			} as Partial<SimDevice>),
		]);
		await openPicker();

		expect(screen.getByTestId("sim-power-stock")).toHaveTextContent(/com\.apple\.nope\. Features/);
	});

	it("says nothing extra when the profile landed", async () => {
		open([
			device({
				state: "Booted",
				power: { op: "boot", state: "running", phase: "booting", startedAt: "2026-08-20T09:00:00Z" },
			} as Partial<SimDevice>),
		]);
		await openPicker();

		expect(screen.queryByTestId("sim-power-stock")).not.toBeInTheDocument();
	});

	// 🗝 Being taken has to be legible while somebody is SCANNING the list to pick
	// a device, which is a moment nobody spends hovering. It used to be the tail of
	// `iOS 26.3 · watching · leased by …` - the end of the line that truncates
	// first - with the holder's name only in a tooltip.
	it("tags a leased device rather than burying it at the end of a line", async () => {
		open([device({ state: "Booted", lease: held("p-9") })]);
		await openPicker();

		const tag = within(screen.getByTestId("sim-device-UDID-A")).getByTestId("sim-lease-tag");
		expect(tag).toHaveTextContent(/leased by @p-9/i);
	});

	// ⚠ The two leases mean opposite things to the hand about to click: one device
	// is this session's to drive, the other will refuse it. A row that read the
	// same either way would be worse than one that said nothing.
	it("does not say this session's own device the way it says somebody else's", async () => {
		open([
			device({ udid: "UDID-A", state: "Booted", lease: held("p-1") }),
			device({ udid: "UDID-B", name: "iPhone 17 Pro", state: "Booted", lease: held("p-9") }),
		]);
		await openPicker();

		const mine = within(screen.getByTestId("sim-device-UDID-A")).getByTestId("sim-lease-tag");
		const theirs = within(screen.getByTestId("sim-device-UDID-B")).getByTestId("sim-lease-tag");
		expect(mine).toHaveTextContent(/^yours$/i);
		expect(theirs).toHaveTextContent(/leased by @p-9/i);
		expect(mine.className).not.toEqual(theirs.className);
		// And a screen reader is told the same thing, which an aria-label on the
		// row would otherwise swallow.
		expect(screen.getByRole("button", { name: /watch iPhone 17 Pro, leased by @p-9/i })).toBeInTheDocument();
	});

	// The id is long enough to truncate the row it sits on and says nothing about
	// what that session is doing. The board name is shorter and answers both.
	it("names a holder by its board name where the board has one", async () => {
		open([device({ state: "Booted", lease: held("nter-ios-app-77") })], {
			holderNames: new Map([["nter-ios-app-77", "OA landing advisor"]]),
		});
		await openPicker();

		expect(screen.getByTestId("sim-lease-tag")).toHaveTextContent(/leased by OA landing advisor/i);
		// The raw id is never lost - taking the device away needs it, and so does
		// anybody about to go looking for that session.
		expect(screen.getByRole("button", { name: /watch iPhone 17 Pro Max/i })).toHaveAttribute(
			"title",
			"Leased by @nter-ios-app-77",
		);
	});

	// A device nobody holds gets no tag at all: a row per device saying "free"
	// would make the one that is taken harder to spot, not easier.
	it("says nothing about a lease nobody holds", async () => {
		open([device({ state: "Booted" })]);
		await openPicker();
		expect(screen.queryByTestId("sim-lease-tag")).not.toBeInTheDocument();
	});

	it("says so when the machine has no simulators at all", async () => {
		open([]);
		await openPicker();
		expect(screen.getByText(/no iOS Simulators installed/i)).toBeInTheDocument();
	});
});
