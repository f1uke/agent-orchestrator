// Native OS notifications, owned by the main process.
//
// On macOS (Electron 22+) notifications go through Apple's UNUserNotification
// framework, which has two consequences this module is built around:
//   1. The app must be code-signed for notifications to appear at all; an
//      unsigned binary emits a `failed` event instead of showing anything. We
//      surface that failure via `logError` so a silently-dropped notification
//      leaves a diagnostic instead of nothing.
//   2. The renderer hands us a notification, we show it, and the click handler
//      must survive until the user (eventually) interacts with it. A Notification
//      created and immediately dropped by an IPC handler is eligible for GC the
//      moment the handler returns, which can drop the pending toast and always
//      loses the `click` listener. We retain each live notification in a map keyed
//      by id until it is clicked, closed, or fails, so it cannot be collected early.
//
// The Electron `Notification` class is adapted to `NativeNotificationHandle` at
// the call site (see main.ts) so this module stays free of the Electron import
// and is unit-testable with a fake handle.

export type NativeNotificationRoute = {
	kind: "session" | "pr";
	prUrl?: string;
	sessionId?: string;
	projectId?: string;
};

export type NativeNotificationInput = {
	id: string;
	title: string;
	body?: string;
	// Where a click should take the user. Carried through the IPC round-trip so
	// click routing does not depend on the renderer's unread-notification cache
	// still holding the entry (it may not, e.g. after the app was reopened and a
	// persisted banner is clicked).
	route?: NativeNotificationRoute;
};

// Delivered to the renderer over the "notifications:click" channel when a native
// notification is clicked. Carries the route so the renderer can navigate without
// depending on its unread-notification cache still holding the entry.
export type NativeNotificationClickPayload = {
	id: string;
	route?: NativeNotificationRoute;
};

// The subset of Electron's Notification instance this module drives. Kept
// explicit (rather than reusing Electron's overloaded event API) so a fake can
// implement it directly in tests.
export interface NativeNotificationHandle {
	show(): void;
	close(): void;
	onClick(listener: () => void): void;
	onClose(listener: () => void): void;
	onFailed(listener: (error: unknown) => void): void;
}

export type NativeNotifierDeps = {
	isSupported: () => boolean;
	createNotification: (options: { title: string; body?: string }) => NativeNotificationHandle;
	// Invoked when the user clicks a notification. Receives the original input so
	// the caller can focus the window and route to `input.route`.
	onActivate: (input: NativeNotificationInput) => void;
	logError: (message: string, error: unknown) => void;
};

export type NativeNotifier = {
	show(input: NativeNotificationInput): void;
};

export function createNativeNotifier(deps: NativeNotifierDeps): NativeNotifier {
	// Retains every currently-visible notification so V8 cannot collect it before
	// the user clicks it. Entries are released on click/close/failure.
	const active = new Map<string, NativeNotificationHandle>();

	return {
		show(input) {
			if (!input.id || !input.title || !deps.isSupported()) return;

			// Collapse a repeat for the same id onto a single visible toast.
			active.get(input.id)?.close();

			const handle = deps.createNotification({ title: input.title, body: input.body });
			const release = () => {
				if (active.get(input.id) === handle) active.delete(input.id);
			};

			handle.onClick(() => {
				release();
				deps.onActivate(input);
			});
			handle.onClose(release);
			handle.onFailed((error) => {
				release();
				deps.logError(`native notification "${input.id}" failed to display`, error);
			});

			active.set(input.id, handle);
			handle.show();
		},
	};
}
