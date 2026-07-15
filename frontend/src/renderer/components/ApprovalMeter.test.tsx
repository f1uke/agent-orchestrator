import { render } from "@testing-library/react";
import { describe, expect, it } from "vitest";
import type { ApprovalProgress } from "../lib/pr-display";
import { ApprovalMeter } from "./ApprovalMeter";

const progress = (over: Partial<ApprovalProgress> = {}): ApprovalProgress => ({
	approved: 1,
	required: 2,
	remaining: 1,
	met: false,
	source: "ao",
	...over,
});

function pips(container: HTMLElement) {
	return Array.from(container.querySelectorAll("[data-pip]"));
}

describe("ApprovalMeter", () => {
	it("draws one pip per required approval, filling the approved ones", () => {
		const { container } = render(<ApprovalMeter progress={progress({ approved: 1, required: 2 })} />);
		const all = pips(container);
		expect(all).toHaveLength(2);
		expect(all.filter((p) => p.getAttribute("data-on") === "true")).toHaveLength(1);
	});

	it("marks the meter met at the threshold", () => {
		const { container } = render(
			<ApprovalMeter progress={progress({ approved: 2, required: 2, met: true, remaining: 0 })} />,
		);
		expect(container.querySelector("[data-met='true']")).not.toBeNull();
		expect(pips(container).filter((p) => p.getAttribute("data-on") === "true")).toHaveLength(2);
	});

	it("caps filled pips at the threshold when over", () => {
		const { container } = render(
			<ApprovalMeter progress={progress({ approved: 3, required: 2, met: true, remaining: 0 })} />,
		);
		const all = pips(container);
		expect(all).toHaveLength(2);
		expect(all.filter((p) => p.getAttribute("data-on") === "true")).toHaveLength(2);
	});

	it("renders no pips when the threshold is unknown (count-only)", () => {
		const { container } = render(<ApprovalMeter progress={progress({ required: null })} />);
		expect(pips(container)).toHaveLength(0);
	});

	it("renders no pips when the threshold exceeds the pip ceiling", () => {
		const { container } = render(<ApprovalMeter progress={progress({ approved: 2, required: 6, remaining: 4 })} />);
		expect(pips(container)).toHaveLength(0);
	});
});
