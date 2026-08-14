/**
 * A drag, sent while the finger is still moving.
 *
 * A pointer produces moves as fast as the display refreshes and the daemon
 * answers each one over loopback, so the two are not the same speed. The rule
 * here is the one the frame stream already uses in the other direction: send
 * one at a time, and while one is in flight keep only the newest position. A
 * queue would make the screen lag further behind the finger the longer you
 * drag, which is the exact complaint this replaces.
 *
 * Sending one at a time is also what makes the order safe. Requests that
 * overlap can complete out of order, and a drag whose moves arrive shuffled
 * puts the finger somewhere the human never took it.
 *
 * `begin` and `end` are never coalesced away: they are the touch going down and
 * coming up, and the daemon holds the device for exactly the span between them.
 */

export type DragPoint = { x: number; y: number };
export type DragStep = "drag-begin" | "drag-move" | "drag-end";

export type DragSender = (step: DragStep, point: DragPoint) => Promise<void>;

export class DragStream {
	private readonly send: DragSender;
	private readonly onError: (error: unknown) => void;

	/** Whether a touch is down as far as this side knows. */
	private active = false;
	private inFlight = false;
	/** The begin that has not been sent yet. */
	private opening: DragPoint | null = null;
	/** The newest position not yet sent. Overwritten, never queued. */
	private pending: DragPoint | null = null;
	/** Where the finger came up, once it has. */
	private closing: DragPoint | null = null;
	/** The last position this side sent or was asked to send. */
	private last: DragPoint | null = null;

	constructor(send: DragSender, onError: (error: unknown) => void = () => {}) {
		this.send = send;
		this.onError = onError;
	}

	get isDragging(): boolean {
		return this.active;
	}

	begin(point: DragPoint): void {
		// A touch still open here means the last one's end never happened - a
		// pointer the browser took back, a window switch mid-drag. Silently
		// ignoring the new press is how a pane stops responding to drags until
		// it is remounted, so the old touch is closed and this one starts. The
		// daemon recovers the same way, and its watchdog is the backstop if even
		// this end does not arrive.
		if (this.active) this.end(this.last ?? point);
		this.active = true;
		this.last = point;
		this.opening = point;
		this.closing = null;
		this.pending = null;
		void this.pump();
	}

	move(point: DragPoint): void {
		if (!this.active) return;
		this.last = point;
		this.pending = point;
		void this.pump();
	}

	end(point: DragPoint): void {
		if (!this.active) return;
		this.last = point;
		this.closing = point;
		// A drag that ends before its last move went out ends where the finger
		// actually left, so a move still pending is not worth sending.
		this.pending = null;
		void this.pump();
	}

	/**
	 * cancel gives up locally without telling the device. Only for a stream that
	 * is being torn down anyway - the daemon's watchdog is what lifts the finger
	 * in that case, and it is the guarantee that survives this page going away.
	 */
	cancel(): void {
		this.active = false;
		this.opening = null;
		this.pending = null;
		this.closing = null;
		this.last = null;
	}

	private async pump(): Promise<void> {
		if (this.inFlight) return;
		const next = this.take();
		if (!next) return;
		this.inFlight = true;
		try {
			await this.send(next.step, next.point);
		} catch (error) {
			// One failed step ends the drag here: the daemon lifts the finger on
			// the same failure, and continuing to send moves for a touch that is
			// no longer down would be moves with no begin behind them.
			this.cancel();
			this.onError(error);
			return;
		} finally {
			this.inFlight = false;
		}
		void this.pump();
	}

	/** The one step worth sending now, in the only order that is meaningful. */
	private take(): { step: DragStep; point: DragPoint } | null {
		if (this.opening) {
			const point = this.opening;
			this.opening = null;
			return { step: "drag-begin", point };
		}
		if (this.pending) {
			const point = this.pending;
			this.pending = null;
			return { step: "drag-move", point };
		}
		if (this.closing) {
			const point = this.closing;
			this.closing = null;
			this.active = false;
			return { step: "drag-end", point };
		}
		return null;
	}
}
