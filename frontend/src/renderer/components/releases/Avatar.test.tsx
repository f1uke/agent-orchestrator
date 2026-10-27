import { fireEvent, render, waitFor } from "@testing-library/react";
import { afterEach, describe, expect, it, vi } from "vitest";
import { setApiBaseUrl } from "../../lib/api-client";
import { Avatar } from "./Avatar";

afterEach(() => {
	setApiBaseUrl(null);
	vi.unstubAllGlobals();
	vi.restoreAllMocks();
});

describe("Avatar", () => {
	it("points the img straight at a same-origin avatar route, which the team web's img-src 'self' allows", () => {
		const fetchSpy = vi.fn(() => new Promise<Response>(() => {}));
		vi.stubGlobal("fetch", fetchSpy);
		setApiBaseUrl(window.location.origin);
		const { container } = render(<Avatar accountId="557058:face" name="Ada Example" />);
		const img = container.querySelector("img");
		expect(img?.getAttribute("src")).toBe(`${window.location.origin}/api/v1/release-trains/avatars/557058%3Aface`);
		expect(fetchSpy).not.toHaveBeenCalled();
	});

	it("falls back to initials when a same-origin avatar fails to load", () => {
		setApiBaseUrl(window.location.origin);
		const { container } = render(<Avatar accountId="557058:none" name="Ada Example" />);
		fireEvent.error(container.querySelector("img") as HTMLImageElement);
		expect(container.querySelector("img")).toBeNull();
		expect(container.textContent).toBe("AE");
	});

	it("loads a cross-origin avatar through fetch into a blob URL, as the app's loopback daemon needs", async () => {
		vi.stubGlobal(
			"fetch",
			vi.fn(async () => ({ ok: true, blob: async () => new Blob(["x"]) })) as unknown as typeof fetch,
		);
		vi.spyOn(URL, "createObjectURL").mockReturnValue("blob:app://renderer/1");
		vi.spyOn(URL, "revokeObjectURL").mockImplementation(() => {});
		setApiBaseUrl("http://127.0.0.1:3001");
		const { container } = render(<Avatar accountId="557058:face" name="Ada Example" />);
		await waitFor(() => expect(container.querySelector("img")?.getAttribute("src")).toBe("blob:app://renderer/1"));
	});
});
