import { beforeEach, describe, expect, it, vi } from "vitest";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { TeamWebControls } from "./TeamWebControls";

const { GET, POST } = vi.hoisted(() => ({ GET: vi.fn(), POST: vi.fn() }));

vi.mock("../../hooks/useApiReady", () => ({ useApiReady: () => true }));
vi.mock("../../lib/api-client", () => ({
	apiClient: { GET, POST },
	apiErrorMessage: (e: unknown) =>
		typeof e === "object" && e !== null && "message" in e
			? String((e as { message: unknown }).message)
			: "Request failed",
}));

const stopped = { running: false, sessions: 0, keyPresent: true, keyGeneration: 1, hosts: [] };
const running = {
	running: true,
	pid: 4242,
	url: "http://127.0.0.1:7420/",
	port: 7420,
	sessions: 3,
	keyPresent: true,
	keyGeneration: 1,
	hosts: ["box.tail1234.ts.net"],
};

beforeEach(() => {
	GET.mockReset().mockResolvedValue({ data: stopped });
	POST.mockReset().mockResolvedValue({ data: running });
});

function renderControls() {
	const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
	return render(
		<QueryClientProvider client={client}>
			<TeamWebControls />
		</QueryClientProvider>,
	);
}

describe("TeamWebControls", () => {
	it("shows the web as stopped, with no address, until it runs", async () => {
		renderControls();

		await waitFor(() => expect(screen.getByRole("switch")).toBeEnabled());
		expect(screen.getByRole("switch")).not.toBeChecked();
		expect(screen.getByText("Stopped")).toBeInTheDocument();
		expect(screen.queryByText("http://127.0.0.1:7420/")).not.toBeInTheDocument();
	});

	it("shows the address and how many people are signed in while it runs", async () => {
		GET.mockResolvedValue({ data: running });
		renderControls();

		await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
		expect(screen.getByText("http://127.0.0.1:7420/")).toBeInTheDocument();
		expect(screen.getByText("3")).toBeInTheDocument();
		expect(screen.getByText("Running")).toBeInTheDocument();
	});

	it("names the public hosts the web answers, or says there are none and how to add one", async () => {
		renderControls();
		await waitFor(() => expect(screen.getByRole("switch")).toBeEnabled());
		expect(screen.getByText(/None\. Add yours with ao release-web start --host/)).toBeInTheDocument();

		GET.mockResolvedValue({ data: running });
		renderControls();
		expect(await screen.findByText("box.tail1234.ts.net")).toBeInTheDocument();
	});

	it("starts the web from the switch and reads the status again", async () => {
		renderControls();
		await waitFor(() => expect(screen.getByRole("switch")).toBeEnabled());
		GET.mockResolvedValue({ data: running });

		await userEvent.click(screen.getByRole("switch"));

		expect(POST).toHaveBeenCalledWith("/api/v1/release-web/start");
		await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
		expect(GET.mock.calls.length).toBeGreaterThan(1);
	});

	it("stops it from the switch", async () => {
		GET.mockResolvedValue({ data: running });
		POST.mockResolvedValue({ data: stopped });
		renderControls();
		await waitFor(() => expect(screen.getByRole("switch")).toBeChecked());
		GET.mockResolvedValue({ data: stopped });

		await userEvent.click(screen.getByRole("switch"));

		expect(POST).toHaveBeenCalledWith("/api/v1/release-web/stop");
		await waitFor(() => expect(screen.getByText("Stopped")).toBeInTheDocument());
	});

	it("copies the key through the daemon, never receiving it", async () => {
		POST.mockResolvedValue({ data: { copied: true } });
		renderControls();
		await waitFor(() => expect(screen.getByRole("button", { name: "Copy key" })).toBeEnabled());

		await userEvent.click(screen.getByRole("button", { name: "Copy key" }));

		expect(POST).toHaveBeenCalledWith("/api/v1/release-web/copy-key");
		await waitFor(() => expect(screen.getByRole("button", { name: "Key copied" })).toBeInTheDocument());
	});

	it("will not copy a key that does not exist yet", async () => {
		GET.mockResolvedValue({ data: { running: false, sessions: 0, keyPresent: false, hosts: [] } });
		renderControls();

		await waitFor(() => expect(screen.getByRole("switch")).toBeEnabled());
		expect(screen.getByRole("button", { name: "Copy key" })).toBeDisabled();
	});

	it("asks before it rotates the key, because everyone is signed out", async () => {
		POST.mockResolvedValue({ data: { ...stopped, keyGeneration: 2 } });
		renderControls();
		await waitFor(() => expect(screen.getByRole("switch")).toBeEnabled());

		await userEvent.click(screen.getByRole("button", { name: "Rotate key" }));

		expect(POST).not.toHaveBeenCalled();
		expect(screen.getByText(/signed out/i)).toBeInTheDocument();

		await userEvent.click(screen.getByRole("button", { name: "Rotate key" }));

		expect(POST).toHaveBeenCalledWith("/api/v1/release-web/rotate-key");
		await waitFor(() => expect(screen.queryByText(/signed out/i)).not.toBeInTheDocument());
	});

	it("drops the rotation when the person cancels", async () => {
		renderControls();
		await waitFor(() => expect(screen.getByRole("switch")).toBeEnabled());

		await userEvent.click(screen.getByRole("button", { name: "Rotate key" }));
		await userEvent.click(screen.getByRole("button", { name: "Cancel" }));

		expect(POST).not.toHaveBeenCalled();
		expect(screen.queryByText(/signed out/i)).not.toBeInTheDocument();
	});

	it("shows a failed start inline and leaves the switch off", async () => {
		POST.mockResolvedValue({ error: { message: "release web: exited before listening, see web.log" } });
		renderControls();
		await waitFor(() => expect(screen.getByRole("switch")).toBeEnabled());

		await userEvent.click(screen.getByRole("switch"));

		expect(await screen.findByRole("alert")).toHaveTextContent("exited before listening");
		expect(screen.getByRole("switch")).not.toBeChecked();
	});

	it("says exposing it is done with Tailscale Funnel, by the person", async () => {
		renderControls();

		expect(screen.getByText(/Tailscale Funnel, by you/)).toBeInTheDocument();
	});
});
