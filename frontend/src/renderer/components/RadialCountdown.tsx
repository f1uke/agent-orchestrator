import { cn } from "../lib/utils";

const STROKE_WIDTH = 3;

export interface RadialCountdownProps {
	/** Fraction of the current cycle elapsed, 0..1. Drives the arc length. */
	fraction: number;
	/** Outer diameter in pixels. */
	size?: number;
	/**
	 * When true the ring renders as a muted full track with no progress arc,
	 * for loops that have never run or are paused/disabled.
	 */
	indeterminate?: boolean;
	className?: string;
}

/**
 * A small SVG progress ring counting down to a loop's next run. The accent arc
 * fills clockwise from the top as the cycle elapses (fraction 0 -> 1); at fire it
 * rolls over to a fresh empty ring. Purely presentational and aria-hidden - the
 * accessible label lives on the surrounding row text.
 */
export function RadialCountdown({ fraction, size = 28, indeterminate = false, className }: RadialCountdownProps) {
	const radius = (size - STROKE_WIDTH) / 2;
	const circumference = 2 * Math.PI * radius;
	const clamped = Math.min(1, Math.max(0, fraction));
	const dashOffset = circumference * (1 - clamped);
	const center = size / 2;

	return (
		<svg
			width={size}
			height={size}
			viewBox={`0 0 ${size} ${size}`}
			className={cn("shrink-0", className)}
			aria-hidden="true"
		>
			<circle
				cx={center}
				cy={center}
				r={radius}
				fill="none"
				strokeWidth={STROKE_WIDTH}
				className="text-border"
				stroke="currentColor"
			/>
			{!indeterminate && (
				<circle
					cx={center}
					cy={center}
					r={radius}
					fill="none"
					strokeWidth={STROKE_WIDTH}
					strokeLinecap="round"
					stroke="currentColor"
					strokeDasharray={circumference}
					strokeDashoffset={dashOffset}
					transform={`rotate(-90 ${center} ${center})`}
					className="text-[color:var(--accent)] transition-[stroke-dashoffset] duration-1000 ease-linear"
					data-testid="radial-progress-arc"
				/>
			)}
		</svg>
	);
}
