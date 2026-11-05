import { render, screen, within } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, describe, expect, it, vi } from "vitest";
import { aoBridge } from "../lib/bridge";
import type { RemoteControl } from "../lib/chat-fold";
import { RemoteControlChip } from "./ChatRemoteControl";
import { TooltipProvider } from "./ui/tooltip";

const URL = "https://claude.ai/code/session_example";
const remote = (over: Partial<RemoteControl>): RemoteControl => ({ state: "connected", url: URL, detail: "", ...over });

const renderChip = (value: RemoteControl) =>
	render(
		<TooltipProvider delayDuration={0}>
			<RemoteControlChip remote={value} />
		</TooltipProvider>,
	);

afterEach(() => vi.restoreAllMocks());

describe("RemoteControlChip", () => {
	it("shows nothing while Remote Control is off", () => {
		const { container } = renderChip({ state: "off", url: "", detail: "" });
		expect(container).toBeEmptyDOMElement();
	});

	it("says it is connecting, with nothing to open before claude gave a link", () => {
		renderChip(remote({ state: "connecting", url: "" }));
		expect(screen.getByText("Remote connecting")).toBeVisible();
		expect(screen.queryByRole("button")).toBeNull();
	});

	it("opens the link, a QR code of it and a copy button once connected", async () => {
		const user = userEvent.setup();
		renderChip(remote({}));
		await user.click(screen.getByRole("button", { name: "Remote connected" }));
		const pop = await screen.findByRole("dialog", { name: "Remote Control" });
		expect(within(pop).getByText(URL)).toBeVisible();
		const qr = within(pop).getByRole("img", { name: "QR code for the session link" });
		expect(qr.querySelector("path")?.getAttribute("d")).toMatch(/^M\d+ \d+h\d+v1h-\d+z/);
		const write = vi.spyOn(aoBridge.clipboard, "writeText").mockResolvedValue();
		await user.click(within(pop).getByRole("button", { name: "Copy link" }));
		expect(write).toHaveBeenCalledWith(URL);
		expect(await within(pop).findByRole("button", { name: "Copied" })).toBeVisible();
	});

	it("keeps the link reachable while the bridge reconnects", async () => {
		const user = userEvent.setup();
		renderChip(remote({ state: "reconnecting" }));
		await user.click(screen.getByRole("button", { name: "Remote reconnecting" }));
		expect(await screen.findByRole("button", { name: "Copy link" })).toBeVisible();
	});

	it("gives claude's reason as the tooltip when it failed, and offers no stale link", async () => {
		const user = userEvent.setup();
		renderChip(remote({ state: "failed", detail: "Remote Control is not available for this account." }));
		const chip = screen.getByLabelText("Remote failed: Remote Control is not available for this account.");
		expect(chip).toHaveTextContent("Remote failed");
		expect(screen.queryByRole("button")).toBeNull();
		await user.hover(chip);
		expect(await screen.findByRole("tooltip")).toHaveTextContent("Remote Control is not available for this account.");
	});

	it("still says why when claude gave no reason", () => {
		renderChip({ state: "failed", url: "", detail: "" });
		expect(screen.getByLabelText("Remote failed: Claude gave no reason")).toBeVisible();
	});
});
