export type TerminalTarget =
	| { kind: "worker" }
	| {
			kind: "reviewer";
			handleId: string;
			harness: string;
	  }
	/**
	 * The iOS run pane: `ao sim run` building, installing and launching, in a
	 * terminal of its own. Like the reviewer target it is a bare runtime handle
	 * rather than a session - nothing sweeps it, it has no board card, and the
	 * pane's keep-alive shell outlives the build so a failure is still readable.
	 */
	| { kind: "run"; handleId: string };

/**
 * The runtime handle a target attaches to, when the target carries one of its
 * own. The worker target does not: it uses the session's.
 *
 * It exists so a third target could not be added by pattern-matching `reviewer`
 * in five places and missing the sixth, which is how the run pane's terminal
 * first attached to the agent's handle instead of its own.
 */
export function targetHandleId(target: TerminalTarget | undefined): string | undefined {
	return target && target.kind !== "worker" ? target.handleId : undefined;
}
