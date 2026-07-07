import { beforeEach, describe, expect, it, vi } from "vitest";
import {
	createNativeNotifier,
	type NativeNotificationHandle,
	type NativeNotificationInput,
} from "./native-notifications";

class FakeHandle implements NativeNotificationHandle {
	shown = false;
	closed = false;
	clickCb: (() => void) | undefined;
	closeCb: (() => void) | undefined;
	failedCb: ((error: unknown) => void) | undefined;

	constructor(public options: { title: string; body?: string }) {}

	show() {
		this.shown = true;
	}
	close() {
		this.closed = true;
		this.closeCb?.();
	}
	onClick(cb: () => void) {
		this.clickCb = cb;
	}
	onClose(cb: () => void) {
		this.closeCb = cb;
	}
	onFailed(cb: (error: unknown) => void) {
		this.failedCb = cb;
	}
}

function setup(overrides: { isSupported?: () => boolean } = {}) {
	const created: FakeHandle[] = [];
	const onActivate = vi.fn();
	const logError = vi.fn();
	const notifier = createNativeNotifier({
		isSupported: overrides.isSupported ?? (() => true),
		createNotification: (options) => {
			const handle = new FakeHandle(options);
			created.push(handle);
			return handle;
		},
		onActivate,
		logError,
	});
	return { notifier, created, onActivate, logError };
}

function input(overrides: Partial<NativeNotificationInput> = {}): NativeNotificationInput {
	return {
		id: "ntf_1",
		title: "checkout needs input",
		body: "The agent is waiting for your response.",
		route: { kind: "session", sessionId: "sess-1", projectId: "proj-1" },
		...overrides,
	};
}

describe("createNativeNotifier", () => {
	let notifier: ReturnType<typeof setup>["notifier"];
	let created: FakeHandle[];
	let onActivate: ReturnType<typeof vi.fn>;
	let logError: ReturnType<typeof vi.fn>;

	beforeEach(() => {
		({ notifier, created, onActivate, logError } = setup());
	});

	it("shows a native notification with the title and body", () => {
		notifier.show(input());

		expect(created).toHaveLength(1);
		expect(created[0].shown).toBe(true);
		expect(created[0].options).toEqual({ title: "checkout needs input", body: "The agent is waiting for your response." });
	});

	it("ignores notifications missing an id or title", () => {
		notifier.show(input({ id: "" }));
		notifier.show(input({ title: "" }));

		expect(created).toHaveLength(0);
	});

	it("does not show when native notifications are unsupported", () => {
		const unsupported = setup({ isSupported: () => false });
		unsupported.notifier.show(input());

		expect(unsupported.created).toHaveLength(0);
	});

	it("routes to the notification target when clicked", () => {
		notifier.show(input());
		created[0].clickCb?.();

		expect(onActivate).toHaveBeenCalledTimes(1);
		expect(onActivate).toHaveBeenCalledWith(input());
	});

	it("logs a diagnostic when the native notification fails to display", () => {
		notifier.show(input());
		const error = new Error("Unsigned binaries can not display notifications");
		created[0].failedCb?.(error);

		expect(logError).toHaveBeenCalledTimes(1);
		const [message, loggedError] = logError.mock.calls[0];
		expect(message).toContain("ntf_1");
		expect(loggedError).toBe(error);
	});

	it("retains one live notification per id, replacing an earlier one with the same id", () => {
		notifier.show(input());
		const first = created[0];
		notifier.show(input());

		expect(created).toHaveLength(2);
		expect(first.closed).toBe(true);
	});

	it("releases the retained reference once clicked so a later toast does not re-close it", () => {
		notifier.show(input());
		const first = created[0];
		first.clickCb?.();

		notifier.show(input());

		expect(created).toHaveLength(2);
		expect(first.closed).toBe(false);
	});
});
