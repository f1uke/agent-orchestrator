import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { useState } from "react";
import { describe, expect, it, vi } from "vitest";
import type { components } from "../../../api/schema";
import { ModelField, nextModelOnAgentChange } from "./ModelField";

type AgentInfo = components["schemas"]["AgentInfo"];

const OPENCODE: AgentInfo = {
	id: "opencode",
	label: "opencode",
	modelsOpenEnded: true,
	models: [
		{ id: "anthropic/claude-opus-4-8", label: "Claude Opus 4.8" },
		{ id: "openai/gpt-5.6", label: "GPT-5.6" },
	],
};

const CLAUDE: AgentInfo = {
	id: "claude-code",
	label: "Claude Code",
	models: [
		{ id: "opus", label: "Opus" },
		{ id: "sonnet", label: "Sonnet" },
	],
};

describe("nextModelOnAgentChange", () => {
	it("keeps a free-form value when the target agent is open-ended", () => {
		expect(nextModelOnAgentChange("openrouter/anthropic/claude-3.7", OPENCODE)).toBe("openrouter/anthropic/claude-3.7");
	});

	it("resets to Default when a fixed target does not list the value", () => {
		expect(nextModelOnAgentChange("anthropic/claude-opus-4-8", CLAUDE)).toBe("");
	});

	it("keeps a value the fixed target does list", () => {
		expect(nextModelOnAgentChange("opus", CLAUDE)).toBe("opus");
	});

	it("leaves an already-empty value untouched", () => {
		expect(nextModelOnAgentChange("", CLAUDE)).toBe("");
		expect(nextModelOnAgentChange("", OPENCODE)).toBe("");
	});
});

// A controlled wrapper so typing accumulates into a real value the way the
// settings form drives the field.
function Harness({ agent, initial = "" }: { agent: AgentInfo | undefined; initial?: string }) {
	const [value, setValue] = useState(initial);
	return <ModelField id="model" value={value} agent={agent} onChange={setValue} />;
}

describe("ModelField dispatch", () => {
	it("renders a typeable input for an open-ended agent", () => {
		render(<Harness agent={OPENCODE} />);
		expect(screen.getByRole("combobox").tagName).toBe("INPUT");
	});

	it("renders a fixed Select (button, not typeable) for a fixed-tier agent", () => {
		render(<Harness agent={CLAUDE} />);
		expect(screen.getByRole("combobox").tagName).toBe("BUTTON");
	});
});

describe("ModelCombobox (open-ended)", () => {
	it("shows the first catalog entry as the placeholder example", () => {
		render(<Harness agent={OPENCODE} />);
		expect(screen.getByRole("combobox")).toHaveAttribute("placeholder", "anthropic/claude-opus-4-8");
	});

	it("round-trips a custom id that is not in the suggestion list", async () => {
		const user = userEvent.setup();
		render(<Harness agent={OPENCODE} />);
		const input = screen.getByRole("combobox");
		await user.type(input, "openrouter/anthropic/claude-3.7");
		expect(input).toHaveValue("openrouter/anthropic/claude-3.7");
	});

	it("offers the catalog entries as suggestions", async () => {
		const user = userEvent.setup();
		render(<Harness agent={OPENCODE} />);
		await user.click(screen.getByRole("combobox"));
		expect(screen.getByText("Claude Opus 4.8")).toBeInTheDocument();
		expect(screen.getByText("GPT-5.6")).toBeInTheDocument();
	});

	it("clicking a suggestion sets its id", async () => {
		const user = userEvent.setup();
		render(<Harness agent={OPENCODE} />);
		const input = screen.getByRole("combobox");
		await user.click(input);
		await user.click(screen.getByText("GPT-5.6"));
		expect(input).toHaveValue("openai/gpt-5.6");
	});

	it("selecting Default clears the value to the agent default", async () => {
		const user = userEvent.setup();
		render(<Harness agent={OPENCODE} initial="anthropic/claude-opus-4-8" />);
		const input = screen.getByRole("combobox");
		await user.click(input);
		await user.click(screen.getByText(/Default \(opencode default\)/));
		expect(input).toHaveValue("");
	});

	it("calls onChange with the custom string as the user types", async () => {
		const user = userEvent.setup();
		const onChange = vi.fn();
		render(<ModelField id="model" value="" agent={OPENCODE} onChange={onChange} />);
		await user.type(screen.getByRole("combobox"), "x");
		expect(onChange).toHaveBeenCalledWith("x");
	});
});
