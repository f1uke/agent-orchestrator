import { render, screen } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { OpenInTargets } from "../../main/open-in-targets";
import { OpenInMenu } from "./OpenInMenu";

const detectTargets = vi.fn<(dir: string) => Promise<OpenInTargets>>();
const terminal = vi.fn<(dir: string) => Promise<void>>();
const finder = vi.fn<(dir: string) => Promise<void>>();
const editor = vi.fn<(dir: string) => Promise<void>>();
const xcode = vi.fn<(targetPath: string) => Promise<void>>();

vi.mock("../lib/bridge", () => ({
	aoBridge: {
		openIn: {
			detectTargets: (dir: string) => detectTargets(dir),
			terminal: (dir: string) => terminal(dir),
			finder: (dir: string) => finder(dir),
			editor: (dir: string) => editor(dir),
			xcode: (targetPath: string) => xcode(targetPath),
		},
	},
}));

const MAC_UA = "Mozilla/5.0 (Macintosh; Intel Mac OS X 10_15_7)";
const LINUX_UA = "Mozilla/5.0 (X11; Linux x86_64)";

function setUserAgent(ua: string) {
	Object.defineProperty(window.navigator, "userAgent", { value: ua, configurable: true });
}

const DIR = "/Users/dev/app";

beforeEach(() => {
	setUserAgent(MAC_UA);
	detectTargets.mockResolvedValue({ hasVSCode: false });
	terminal.mockResolvedValue(undefined);
	finder.mockResolvedValue(undefined);
	editor.mockResolvedValue(undefined);
	xcode.mockResolvedValue(undefined);
});

afterEach(() => {
	vi.clearAllMocks();
});

describe("OpenInMenu", () => {
	it("renders nothing without a directory", () => {
		const { container } = render(<OpenInMenu />);
		expect(container).toBeEmptyDOMElement();
	});

	it("renders nothing on a non-macOS platform", () => {
		setUserAgent(LINUX_UA);
		const { container } = render(<OpenInMenu directory={DIR} />);
		expect(container).toBeEmptyDOMElement();
	});

	it("shows the share-style trigger on macOS with a directory", () => {
		render(<OpenInMenu directory={DIR} />);
		expect(screen.getByRole("button", { name: "Open in…" })).toBeInTheDocument();
	});

	it("always offers Terminal and Finder; VS Code and Xcode only when detected", async () => {
		detectTargets.mockResolvedValue({
			hasVSCode: true,
			xcode: { name: "App.xcworkspace", path: `${DIR}/App.xcworkspace` },
		});
		const user = userEvent.setup();
		render(<OpenInMenu directory={DIR} />);

		await user.click(screen.getByRole("button", { name: "Open in…" }));

		expect(await screen.findByText("Open in Terminal")).toBeInTheDocument();
		expect(screen.getByText("Open in Finder")).toBeInTheDocument();
		expect(screen.getByText("Open in Visual Studio Code")).toBeInTheDocument();
		expect(screen.getByText("Open App.xcworkspace")).toBeInTheDocument();
	});

	it("hides VS Code and Xcode items when nothing is detected", async () => {
		detectTargets.mockResolvedValue({ hasVSCode: false });
		const user = userEvent.setup();
		render(<OpenInMenu directory={DIR} />);

		await user.click(screen.getByRole("button", { name: "Open in…" }));

		expect(await screen.findByText("Open in Terminal")).toBeInTheDocument();
		expect(screen.queryByText("Open in Visual Studio Code")).not.toBeInTheDocument();
		expect(screen.queryByText(/\.xcworkspace|\.xcodeproj/)).not.toBeInTheDocument();
	});

	it("shows a toast instead of crashing when a launch fails", async () => {
		terminal.mockRejectedValue(new Error("open failed"));
		const user = userEvent.setup();
		render(<OpenInMenu directory={DIR} />);

		await user.click(screen.getByRole("button", { name: "Open in…" }));
		await user.click(await screen.findByText("Open in Terminal"));

		expect(await screen.findByRole("status")).toHaveTextContent("Couldn't open in Terminal.");
	});
});
