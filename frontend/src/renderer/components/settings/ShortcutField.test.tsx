import { describe, expect, it, vi } from "vitest";
import { fireEvent, render, screen } from "@testing-library/react";
import { useState } from "react";
import { ShortcutField } from "./ShortcutField";

vi.mock("../../lib/platform", () => ({ isMacPlatform: () => true }));

function Harness({ initial = "Ctrl+KeyI", onChange }: { initial?: string; onChange?: (v: string) => void }) {
	const [value, setValue] = useState(initial);
	return (
		<ShortcutField
			id="reindent"
			value={value}
			defaultValue="Ctrl+KeyI"
			other={{ shortcut: "Ctrl+Shift+KeyI", what: "Format Document" }}
			onChange={(v) => {
				setValue(v);
				onChange?.(v);
			}}
		/>
	);
}

const field = () => screen.getByTestId("reindent-field");
const press = (init: Partial<KeyboardEventInit> & { code: string; key: string }) => fireEvent.keyDown(field(), init);

describe("ShortcutField", () => {
	it("draws the binding as Mac keycaps", () => {
		render(<Harness />);
		expect(field()).toHaveTextContent("⌃I");
	});

	it("records the next chord pressed after a click", () => {
		const onChange = vi.fn();
		render(<Harness onChange={onChange} />);
		fireEvent.click(field());
		expect(field()).toHaveTextContent("Press a shortcut…");
		press({ code: "ShiftLeft", key: "Shift", shiftKey: true });
		expect(onChange).not.toHaveBeenCalled();
		press({ code: "KeyK", key: "K", ctrlKey: true, shiftKey: true });
		expect(onChange).toHaveBeenCalledWith("Ctrl+Shift+KeyK");
		expect(field()).toHaveTextContent("⌃⇧K");
	});

	it("refuses a plain key, and the other editor shortcut, and keeps listening", () => {
		const onChange = vi.fn();
		render(<Harness onChange={onChange} />);
		fireEvent.click(field());
		press({ code: "KeyJ", key: "j" });
		expect(screen.getByText(/a plain key would fire while you type/)).toBeInTheDocument();
		press({ code: "KeyI", key: "I", ctrlKey: true, shiftKey: true });
		expect(screen.getByText("⌃⇧I is already Format Document.")).toBeInTheDocument();
		expect(onChange).not.toHaveBeenCalled();
		expect(field()).toHaveTextContent("Press a shortcut…");
	});

	it("cancels on Escape and unbinds on Delete", () => {
		const onChange = vi.fn();
		render(<Harness onChange={onChange} />);
		fireEvent.click(field());
		press({ code: "Escape", key: "Escape" });
		expect(onChange).not.toHaveBeenCalled();
		expect(field()).toHaveTextContent("⌃I");
		fireEvent.click(field());
		press({ code: "Delete", key: "Delete" });
		expect(onChange).toHaveBeenCalledWith("");
		expect(field()).toHaveTextContent("Not set");
		fireEvent.click(screen.getByRole("button", { name: "Reset to ⌃I" }));
		expect(onChange).toHaveBeenLastCalledWith("Ctrl+KeyI");
	});

	it("says which of two bindings wins when something else answers to the same keys", () => {
		render(<Harness initial="Meta+KeyI" />);
		expect(screen.getByText("⌘I is also Trigger Suggest. Inside the editor, this one wins.")).toBeInTheDocument();
	});
});
