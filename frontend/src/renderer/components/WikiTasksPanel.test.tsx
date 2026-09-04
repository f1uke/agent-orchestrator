import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";
import type { WikiTaskRow, WikiTasks, WikiTasksSettings } from "../hooks/useWiki";
import { WikiTaskTickError } from "../hooks/useWiki";
import { noteLabel, WikiTasksPanel } from "./WikiTasksPanel";

function row(over: Partial<WikiTaskRow> = {}): WikiTaskRow {
	return {
		id: over.id ?? "r1",
		path: "Areas/a.md",
		line: 4,
		raw: "- [ ] the row",
		text: "the row",
		...over,
	};
}

function tasks(over: Partial<WikiTasks> = {}): WikiTasks {
	return {
		configured: true,
		folders: ["Areas"],
		sections: [],
		ownerAliases: [],
		owners: [],
		tasks: [row()],
		scannedNotes: 1,
		truncated: false,
		...over,
	};
}

const SETTINGS: WikiTasksSettings = { folders: ["Areas"], sections: [], cutoff: "", ownerAliases: [] };

function panel(over: Partial<Parameters<typeof WikiTasksPanel>[0]> = {}) {
	return (
		<WikiTasksPanel
			tasks={tasks()}
			settings={SETTINGS}
			loading={false}
			error={null}
			onRefresh={vi.fn()}
			onComplete={vi.fn().mockResolvedValue({ moved: false })}
			onSaveSettings={vi.fn().mockResolvedValue(undefined)}
			savingSettings={false}
			settingsError={null}
			onOpenNote={vi.fn()}
			{...over}
		/>
	);
}

beforeEach(() => {
	window.localStorage.clear();
});

describe("ticking a row", () => {
	/**
	 * 🗝 The row's text goes back to the daemon EXACTLY as it was drawn. This
	 * is what makes ticking the wrong line impossible: the daemon writes only
	 * to a line whose full text still equals it.
	 */
	it("sends the row's exact raw line back, untouched", async () => {
		const user = userEvent.setup();
		const onComplete = vi.fn().mockResolvedValue({ moved: false });
		const raw = "  - [ ] [@Someone] a row with trailing space  ";
		render(panel({ tasks: tasks({ tasks: [row({ raw, text: "a row", line: 9 })] }), onComplete }));

		await user.click(screen.getByRole("button", { name: /^Tick off:/ }));
		await waitFor(() => expect(onComplete).toHaveBeenCalledTimes(1));
		expect(onComplete.mock.calls[0][0].raw).toBe(raw);
		expect(onComplete.mock.calls[0][0].line).toBe(9);
		expect(onComplete.mock.calls[0][0].path).toBe("Areas/a.md");
	});

	/**
	 * 🗝 The failure the markdown rollup this replaces actually shipped: it
	 * regenerated itself without harvesting pending ticks and threw them away.
	 * Here a refetch replaces the rows underneath a tick that has not settled;
	 * the tick must survive it.
	 */
	it("keeps a tick when the list is refetched underneath it", async () => {
		const user = userEvent.setup();
		let release: (value: { moved: boolean }) => void = () => {};
		const onComplete = vi.fn().mockReturnValue(
			new Promise<{ moved: boolean }>((resolve) => {
				release = resolve;
			}),
		);
		const view = render(panel({ onComplete }));

		await user.click(screen.getByRole("button", { name: /^Tick off:/ }));
		// The poll lands mid-write: a NEW response object, same row, still
		// unchecked as far as the daemon knows.
		view.rerender(panel({ tasks: tasks({ tasks: [row()], scannedNotes: 2 }), onComplete }));

		release({ moved: false });
		// The tick still lands as done rather than being reset by the refetch.
		await waitFor(() => expect(screen.getByRole("button", { name: /^Ticked off:/ })).toBeTruthy());
	});

	it("does not send a second tick for a row already in flight", async () => {
		const user = userEvent.setup();
		const onComplete = vi.fn().mockReturnValue(new Promise(() => {}));
		render(panel({ onComplete }));

		const box = screen.getByRole("button", { name: /^Tick off:/ });
		await user.click(box);
		await waitFor(() => expect(onComplete).toHaveBeenCalledTimes(1));
		// The box is disabled while saving, so a second click cannot register.
		expect(box.hasAttribute("disabled")).toBe(true);
	});

	it("disables the refresh button while a tick is unwritten", async () => {
		const user = userEvent.setup();
		render(panel({ onComplete: vi.fn().mockReturnValue(new Promise(() => {})) }));

		expect(screen.getByRole("button", { name: "Re-read the tasks" }).hasAttribute("disabled")).toBe(false);
		await user.click(screen.getByRole("button", { name: /^Tick off:/ }));
		await waitFor(() =>
			expect(screen.getByRole("button", { name: "Re-read the tasks" }).hasAttribute("disabled")).toBe(true),
		);
	});

	/** A refusal is shown and stays shown — it is never a silent no-op. */
	it("shows a refusal in place, and the row stays unticked", async () => {
		const user = userEvent.setup();
		const onComplete = vi.fn().mockRejectedValue(
			new WikiTaskTickError({
				kind: "stale",
				title: "This row has changed in the note.",
				detail: "Nothing was written.",
			}),
		);
		render(panel({ onComplete }));

		await user.click(screen.getByRole("button", { name: /^Tick off:/ }));
		expect(await screen.findByText("This row has changed in the note.")).toBeTruthy();
		expect(screen.getByText("Nothing was written.")).toBeTruthy();
		// Still tickable: nothing was written, so the row is exactly as it was.
		expect(screen.getByRole("button", { name: /^Tick off:/ }).hasAttribute("disabled")).toBe(false);
	});

	it("says so when the row had moved", async () => {
		const user = userEvent.setup();
		render(panel({ onComplete: vi.fn().mockResolvedValue({ moved: true }) }));

		await user.click(screen.getByRole("button", { name: /^Tick off:/ }));
		expect(await screen.findByText(/had moved in the note/)).toBeTruthy();
	});

	/**
	 * There is deliberately no un-tick. This tab lists unchecked rows, so a
	 * ticked one leaves the list and there is nothing to un-tick from — and the
	 * exact-text guarantee does not extend to a `- [x]` line never displayed.
	 */
	it("offers no way to un-tick a row it has ticked", async () => {
		const user = userEvent.setup();
		render(panel());

		await user.click(screen.getByRole("button", { name: /^Tick off:/ }));
		const ticked = await screen.findByRole("button", { name: /^Ticked off:/ });
		expect(ticked.hasAttribute("disabled")).toBe(true);
	});
});

describe("the cutoff", () => {
	const withCutoff = tasks({
		cutoff: "2026-06-01",
		tasks: [row({ id: "old", fromDate: "2026-01-01" }), row({ id: "new", created: "2026-09-01" })],
	});

	/**
	 * 🗝 A filtered backlog must never read as a destroyed one. The strip says
	 * what is hidden, that the rows are still in the notes, and offers them.
	 */
	it("says what it hid, and that the rows are still in the notes", () => {
		render(panel({ tasks: withCutoff }));
		expect(screen.getByText(/1 row\s+before 2026-06-01 is hidden/)).toBeTruthy();
		expect(screen.getByText(/It is still in your notes/)).toBeTruthy();
	});

	it("shows the hidden rows on request", async () => {
		const user = userEvent.setup();
		render(panel({ tasks: withCutoff }));
		expect(screen.getAllByRole("button", { name: /^Tick off:/ })).toHaveLength(1);

		await user.click(screen.getByRole("button", { name: "Show them" }));
		expect(screen.getAllByRole("button", { name: /^Tick off:/ })).toHaveLength(2);
		expect(screen.getByRole("button", { name: "Hide them" })).toBeTruthy();
	});

	/**
	 * The other half of the same promise: a row the cutoff cannot date is KEPT,
	 * and the strip says so. Silence here would read as "the cutoff hid nothing"
	 * while the list never emptied, and the reader would have no way to tell the
	 * two apart.
	 */
	it("says how many rows it could not date, and keeps them", () => {
		render(
			panel({
				tasks: tasks({
					cutoff: "2026-06-01",
					tasks: [row({ id: "old", fromDate: "2026-01-01" }), row({ id: "undated" })],
				}),
			}),
		);
		expect(screen.getByText(/1 row carries no date of its own, so the cutoff leaves it here/)).toBeTruthy();
		expect(screen.getAllByRole("button", { name: /^Tick off:/ })).toHaveLength(1);
	});

	it("says it even when the cutoff hid nothing at all", () => {
		render(panel({ tasks: tasks({ cutoff: "2026-06-01", tasks: [row({ id: "a" }), row({ id: "b" })] }) }));
		expect(screen.getByText(/2 rows carry no date of their own/)).toBeTruthy();
		expect(screen.queryByRole("button", { name: "Show them" })).toBeNull();
	});

	/**
	 * A due date is a promise about the future; the cutoff asks how old a row
	 * is. An overdue row is never hidden for being old.
	 */
	it("does not hide a row that carries only a due date", () => {
		render(panel({ tasks: tasks({ cutoff: "2026-06-01", tasks: [row({ id: "due", due: "2026-01-01" })] }) }));
		expect(screen.queryByText(/is hidden/)).toBeNull();
		expect(screen.getAllByRole("button", { name: /^Tick off:/ })).toHaveLength(1);
	});

	it("says nothing when there is no cutoff at all", () => {
		render(panel());
		expect(screen.queryByText(/still in your notes/)).toBeNull();
		expect(screen.queryByText(/no date of its own/)).toBeNull();
	});
});

describe("the owner filter", () => {
	const mixed = tasks({
		ownerAliases: ["Fluke"],
		owners: ["Someone"],
		tasks: [row({ id: "mine", text: "my row" }), row({ id: "theirs", owner: "Someone", text: "their row" })],
	});

	it("narrows to the reader's rows and remembers the choice", async () => {
		const user = userEvent.setup();
		const first = render(panel({ tasks: mixed }));
		expect(screen.getByText("their row")).toBeTruthy();

		await user.click(screen.getByRole("button", { name: "Mine" }));
		expect(screen.queryByText("their row")).toBeNull();
		expect(screen.getByText("my row")).toBeTruthy();
		first.unmount();

		render(panel({ tasks: mixed }));
		expect(screen.queryByText("their row")).toBeNull();
	});
});

describe("with nothing configured", () => {
	/**
	 * An unconfigured tab explains what to set. It never silently scans the
	 * whole vault, which would drag in every checkbox that was never a task.
	 */
	it("shows the form rather than an empty list", () => {
		render(panel({ tasks: tasks({ configured: false, folders: [], tasks: [] }) }));
		expect(screen.getByText(/reads the unchecked/)).toBeTruthy();
		// And there is no way to dismiss it: there is no list to go back to.
		expect(screen.queryByRole("button", { name: "Cancel" })).toBeNull();
	});
});

/**
 * A vault that keeps one task note per project names them all `_tasks.md`, so
 * the filename alone tells the reader nothing about which project a row is
 * from. The folder is what distinguishes them.
 */
describe("noteLabel", () => {
	it("includes the note's folder", () => {
		expect(noteLabel("Areas/frontier/_tasks.md")).toBe("frontier/_tasks");
		expect(noteLabel("Projects/Webview-Zoom/_tasks.md")).toBe("Webview-Zoom/_tasks");
	});

	it("is just the name for a note at the vault root", () => {
		expect(noteLabel("inbox.md")).toBe("inbox");
	});
});
