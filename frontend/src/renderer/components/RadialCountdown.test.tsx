import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import { RadialCountdown } from "./RadialCountdown";

const SIZE = 28;
const STROKE_WIDTH = 3;
const radius = (SIZE - STROKE_WIDTH) / 2;
const circumference = 2 * Math.PI * radius;

describe("RadialCountdown", () => {
	it("offsets the arc by half the circumference at fraction 0.5", () => {
		const { getByTestId } = render(<RadialCountdown fraction={0.5} size={SIZE} />);
		const arc = getByTestId("radial-progress-arc");
		const offset = Number(arc.getAttribute("stroke-dashoffset"));
		expect(offset).toBeCloseTo(circumference / 2, 3);
	});

	it("fully offsets the arc at fraction 0 (empty ring)", () => {
		const { getByTestId } = render(<RadialCountdown fraction={0} size={SIZE} />);
		const arc = getByTestId("radial-progress-arc");
		expect(Number(arc.getAttribute("stroke-dashoffset"))).toBeCloseTo(circumference, 3);
	});

	it("clamps fraction above 1", () => {
		const { getByTestId } = render(<RadialCountdown fraction={1.4} size={SIZE} />);
		const arc = getByTestId("radial-progress-arc");
		expect(Number(arc.getAttribute("stroke-dashoffset"))).toBeCloseTo(0, 3);
	});

	it("renders no progress arc when indeterminate", () => {
		const { queryByTestId } = render(<RadialCountdown fraction={0.5} indeterminate />);
		expect(queryByTestId("radial-progress-arc")).toBeNull();
	});
});
