import { useEffect, useState } from "react";
import {
	IDLE_COUNTDOWN_THRESHOLD_MS,
	type IdleCountdown,
	idleCountdown,
	type WorkspaceSession,
} from "../types/workspace";

// Longest single setTimeout we arm; distant deadlines re-arm in hops so we never
// exceed setTimeout's safe range and stay responsive if the deadline moves.
const MAX_SLEEP_MS = 12 * 60 * 60 * 1000; // 12h

/**
 * Live idle-suspend countdown for a board card / sidebar row. It recomputes each
 * second only WHILE the deadline is within the display window ({@link
 * IDLE_COUNTDOWN_THRESHOLD_MS}); far-from-expiry sessions sleep on a single
 * timeout (no per-second churn) until the chip should appear, so a board full of
 * fresh sessions is not re-rendering every second for nothing. Returns null when
 * nothing should show — suspended, no deadline, or still far out.
 */
export function useIdleCountdown(session: Pick<WorkspaceSession, "idleCloseAt" | "isSuspended">): IdleCountdown | null {
	const [now, setNow] = useState(() => Date.now());
	// Suspended sessions show a paused affordance, not a countdown, so they arm no
	// timers here (idleCountdown also returns null for them).
	const deadlineIso = session.isSuspended ? undefined : session.idleCloseAt;

	useEffect(() => {
		if (!deadlineIso) return;
		const deadline = Date.parse(deadlineIso);
		if (Number.isNaN(deadline)) return;

		let intervalId: number | undefined;
		let timeoutId: number | undefined;
		const tick = () => setNow(Date.now());
		const arm = () => {
			const remaining = deadline - Date.now();
			if (remaining > IDLE_COUNTDOWN_THRESHOLD_MS) {
				// Far out: sleep until the deadline enters the display window, then
				// re-evaluate. Capped so a 70h-away deadline re-arms in hops.
				const wait = Math.min(remaining - IDLE_COUNTDOWN_THRESHOLD_MS + 250, MAX_SLEEP_MS);
				timeoutId = window.setTimeout(() => {
					tick();
					arm();
				}, wait);
			} else {
				// In-window (or past): tick once per second while it counts down.
				tick();
				intervalId = window.setInterval(tick, 1000);
			}
		};
		arm();
		return () => {
			if (intervalId !== undefined) window.clearInterval(intervalId);
			if (timeoutId !== undefined) window.clearTimeout(timeoutId);
		};
	}, [deadlineIso]);

	return idleCountdown(session, now);
}
