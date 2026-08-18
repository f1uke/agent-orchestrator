/**
 * A drag, sent while the finger is still moving.
 *
 * A pointer produces moves as fast as the display refreshes and the daemon
 * answers each one over loopback, so the two are not the same speed. The rule
 * here is the one the frame stream already uses in the other direction: send
 * one at a time.
 *
 * 🗝 What is NOT dropped while one is in flight is the motion itself. Keeping
 * only the newest position looked right - the screen tracks the finger instead
 * of replaying where it has been - and it is right whenever the daemon answers
 * in a millisecond or two, which is the ordinary case. It is catastrophic when
 * it does not: a `drag-begin` that takes a few hundred milliseconds outlives
 * the whole drag, every move in between collapses into one slot, `end` then
 * takes that slot, and the device receives a touch-down and a touch-up with
 * NOTHING between them. iOS cannot read a scroll out of two points, so the
 * scroll does not happen at all - not slowly, not partially.
 *
 * ⚠ Measured on a real device while a human drove the pane by hand: of 26
 * drags, 11 collapsed to two requests and zero moves, and the correlation was
 * exact - every collapse had a begin of 181-526 ms, every working drag had one
 * of 1-9 ms. It happens with a recording open (the recorder makes a slow begin
 * common) and it happens without one (measured at 151 ms on an app the
 * accessibility service cannot read).
 *
 * So the moves are QUEUED, and the queue is what keeps the drag a drag. When
 * the path is fast the queue never holds more than one point and the behaviour
 * is exactly what it was; when the path is slow the motion arrives a moment
 * late instead of not at all. Late is worse than immediate. Nothing is far
 * worse than late.
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

/**
 * How much unsent motion may wait. At a pointer's ~16 ms sampling this is
 * about a second of drag, far more than any stall seen on this path, and it
 * bounds how far behind the finger the touch can ever get.
 */
export const MAX_PENDING_MOVES = 64;

export class DragStream {
	private readonly send: DragSender;
	private readonly onError: (error: unknown) => void;

	/** Whether a touch is down as far as this side knows. */
	private active = false;
	private inFlight = false;
	/** The begin that has not been sent yet. */
	private opening: DragPoint | null = null;
	/**
	 * Positions not yet sent, oldest first.
	 *
	 * Bounded, because a backlog that grows without limit would put the touch
	 * further behind the finger the longer the drag went on - the failure the
	 * single slot was there to prevent. At the cap the newest position replaces
	 * the newest queued one, which degrades exactly to the old behaviour after
	 * a second of unsent motion rather than throwing the drag away in the first
	 * hundred milliseconds.
	 */
	private pending: DragPoint[] = [];
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
		this.pending = [];
		void this.pump();
	}

	move(point: DragPoint): void {
		if (!this.active) return;
		this.last = point;
		if (this.pending.length >= MAX_PENDING_MOVES) {
			this.pending[this.pending.length - 1] = point;
		} else {
			this.pending.push(point);
		}
		void this.pump();
	}

	end(point: DragPoint): void {
		if (!this.active) return;
		// ⚠ The FIRST end wins, and the guard is load-bearing rather than
		// defensive. A drag is ended twice on every ordinary release: the pane's
		// pointerup ends it where the finger actually left, and the browser then
		// fires `lostpointercapture` - which the pane also has to treat as an
		// end, because a capture the OS takes back mid-drag would otherwise
		// leave a finger down forever. That second end carries no real position,
		// only a fallback.
		//
		// Until this guard existed, `active` stayed true until the queued end
		// was actually SENT, so the fallback arrived first and overwrote the
		// real one: every drag in the Device tab lifted at the middle of the
		// screen, and every drag a recording captured was written into the flow
		// as a swipe from wherever it started to 50%,50%. It was invisible
		// because the touch did end and the drag did work - only its last
		// position was wrong.
		if (this.closing) return;
		this.last = point;
		this.closing = point;
		// ⚠ The queued moves are NOT dropped here, and that reversal is the
		// fix. Discarding them looked harmless - the end carries where the
		// finger really left, so the last unsent move is redundant - but on a
		// slow begin the queue holds the entire drag, and dropping it is what
		// turned a scroll into a touch-down and a touch-up. The end is
		// appended after them instead, so the device sees the motion and then
		// the release, in the order the human made it.
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
		this.pending = [];
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
		if (this.pending.length > 0) {
			return { step: "drag-move", point: this.pending.shift()! };
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
