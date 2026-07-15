import { render, screen, fireEvent } from "@testing-library/react";
import { it, expect, vi, beforeEach } from "vitest";
import { MediaLightbox, MediaThumb, type MediaItem } from "./MediaLightbox";

beforeEach(() => {
	// jsdom lacks these; the object-URL loader needs them.
	global.URL.createObjectURL = vi.fn(() => "blob:mock");
	global.URL.revokeObjectURL = vi.fn();
	vi.stubGlobal(
		"fetch",
		vi.fn(async () => ({ ok: true, blob: async () => new Blob(["x"]) })) as unknown as typeof fetch,
	);
});

const items: MediaItem[] = [
	{ id: "1", filename: "a.png", mime: "image/png", src: "http://d/a" },
	{ id: "2", filename: "b.png", mime: "image/png", src: "http://d/b" },
];

it("pages next with the arrow control (wraps)", () => {
	const onIndexChange = vi.fn();
	render(
		<MediaLightbox items={items} index={0} onIndexChange={onIndexChange} onClose={() => {}} triggerRef={{ current: null }} />,
	);
	fireEvent.click(screen.getByLabelText("Next evidence"));
	expect(onIndexChange).toHaveBeenCalledWith(1);
});

it("shows the counter only when there is more than one item", () => {
	const { rerender } = render(
		<MediaLightbox items={items} index={0} onIndexChange={() => {}} onClose={() => {}} triggerRef={{ current: null }} />,
	);
	expect(screen.getByText("1 / 2")).toBeInTheDocument();
	rerender(
		<MediaLightbox
			items={[items[0]]}
			index={0}
			onIndexChange={() => {}}
			onClose={() => {}}
			triggerRef={{ current: null }}
		/>,
	);
	expect(screen.queryByText(/\/ 1$/)).not.toBeInTheDocument();
	expect(screen.queryByLabelText("Next evidence")).not.toBeInTheDocument();
});

it("closes on Esc via the dialog", () => {
	const onClose = vi.fn();
	render(
		<MediaLightbox items={items} index={0} onIndexChange={() => {}} onClose={onClose} triggerRef={{ current: null }} />,
	);
	fireEvent.keyDown(document.activeElement || document.body, { key: "Escape" });
	expect(onClose).toHaveBeenCalled();
});

it("MediaThumb renders an openable button once bytes load", async () => {
	render(<MediaThumb item={items[0]} onOpen={() => {}} style={{ width: 100, height: 100 }} />);
	expect(await screen.findByRole("button", { name: /View a\.png/i })).toBeInTheDocument();
});
